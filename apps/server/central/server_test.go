package central

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	return Options{DatabasePath: filepath.Join(t.TempDir(), "central.db"), EnrollmentToken: strings.Repeat("e", 32), AdminToken: strings.Repeat("a", 32), CredentialKey: bytes.Repeat([]byte{3}, 32)}
}
func openTest(t *testing.T, o Options) *Server {
	t.Helper()
	s, err := Open(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func request(s http.Handler, method, path, token, id string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	if raw, ok := body.([]byte); ok {
		b = raw
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if id != "" {
		r.Header.Set("Idempotency-Key", id)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func expect(t *testing.T, w *httptest.ResponseRecorder, status int, out any) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
}
func enrollment(o Options, key byte) contracts.Enrollment {
	return contracts.Enrollment{EnrollmentToken: o.EnrollmentToken, PublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{key}, 32)), Hostname: "kiosk-01", AgentVersion: "test"}
}
func enrollTest(t *testing.T, s *Server, o Options, key byte) contracts.Credential {
	t.Helper()
	var c contracts.Credential
	expect(t, request(s, "POST", "/api/v1/enrollments", "", "", enrollment(o, key)), 200, &c)
	return c
}
func heartbeat(now time.Time) contracts.Heartbeat {
	return contracts.Heartbeat{AgentVersion: "test", Status: "online", LastActivity: now, UptimeSeconds: 5, Health: "ok"}
}

func TestRestartPreservesIdentityHistoryAndReplay(t *testing.T) {
	o := testOptions(t)
	s := openTest(t, o)
	c := enrollTest(t, s, o, 1)
	id := contracts.ID()
	h := heartbeat(time.Now().UTC())
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, id, h), 204, nil)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, o)
	replay := enrollTest(t, s, o, 1)
	if replay != c {
		t.Fatal("enrollment retry changed credential after restart")
	}
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, id, h), 204, nil)
	h.Health = "changed"
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, id, h), 409, nil)
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, contracts.ID(), h), 204, nil)
	var reports ReportPage
	expect(t, request(s, "GET", "/api/v1/admin/devices/"+c.DeviceID+"/reports", o.AdminToken, "", nil), 200, &reports)
	if len(reports.Items) != 2 {
		t.Fatalf("replay duplicated history: %d", len(reports.Items))
	}
	var detail DeviceDetail
	w := request(s, "GET", "/api/v1/admin/devices/"+c.DeviceID, o.AdminToken, "", nil)
	expect(t, w, 200, &detail)
	if !detail.Online || detail.LastSeenAt == nil || !bytes.Contains(detail.Heartbeat, []byte("changed")) {
		t.Fatalf("missing device projection: %+v", detail)
	}
	for _, secret := range []string{c.AccessToken, o.AdminToken, o.EnrollmentToken, digest([]byte(c.AccessToken)), enrollment(o, 1).PublicKey} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("admin response exposed a secret or public key")
		}
	}
	var storedHash string
	if err := s.db.QueryRow("SELECT token_hash FROM devices").Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if storedHash == c.AccessToken || storedHash != digest([]byte(c.AccessToken)) {
		t.Fatal("credential not stored as hash")
	}
}
func TestValidationAuthenticationAndExpiry(t *testing.T) {
	o := testOptions(t)
	now := time.Now().UTC()
	o.Now = func() time.Time { return now }
	s := openTest(t, o)
	c := enrollTest(t, s, o, 1)
	v := enrollment(o, 2)
	v.EnrollmentToken = "wrong"
	expect(t, request(s, "POST", "/api/v1/enrollments", "", "", v), 401, nil)
	expect(t, request(s, "POST", "/api/v1/enrollments", "", "", []byte(`{"hostname":"x"}`)), 400, nil)
	for _, token := range []string{"", o.EnrollmentToken, o.AdminToken, "wrong"} {
		expect(t, request(s, "GET", "/api/v1/agent/jobs", token, "", nil), 401, nil)
	}
	for _, token := range []string{"", c.AccessToken, o.EnrollmentToken} {
		expect(t, request(s, "GET", "/api/v1/admin/devices", token, "", nil), 401, nil)
	}
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, "", heartbeat(now)), 400, nil)
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, contracts.ID(), []byte(`{"status":"online"}`)), 400, nil)
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, contracts.ID(), bytes.Repeat([]byte("x"), contracts.MaxBody+1)), 400, nil)
	expect(t, request(s, "POST", "/api/v1/agent/metrics", c.AccessToken, contracts.ID(), contracts.Metrics{CollectedAt: now, CPUPercent: 101, Processes: []contracts.ProcessMetric{}}), 400, nil)
	expect(t, request(s, "POST", "/dev/jobs", o.AdminToken, "", nil), 404, nil)
	expect(t, request(s, "GET", "/api/v1/agent/jobs?limit=101", c.AccessToken, "", nil), 400, nil)
	expect(t, request(s, "GET", "/api/v1/agent/jobs?cursor=bad", c.AccessToken, "", nil), 400, nil)
	var policy contracts.Policy
	expect(t, request(s, "GET", "/api/v1/agent/policy", c.AccessToken, "", nil), 200, &policy)
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	expect(t, request(s, "GET", "/api/v1/agent/policy", c.AccessToken, "", nil), 401, nil)
	expect(t, request(s, "POST", "/api/v1/enrollments", "", "", enrollment(o, 1)), 409, nil)
}
func TestPaginationInventoryIsolationAndOffline(t *testing.T) {
	o := testOptions(t)
	now := time.Now().UTC()
	o.Now = func() time.Time { return now }
	s := openTest(t, o)
	a := enrollTest(t, s, o, 1)
	b := enrollTest(t, s, o, 2)
	inv := contracts.Inventory{CollectedAt: now, Hostname: "kiosk-01", OS: "windows", Architecture: "amd64", Software: []contracts.Software{}, Runtimes: []contracts.Software{}, Networks: []contracts.Network{}}
	id := contracts.ID()
	for _, c := range []contracts.Credential{a, b} {
		expect(t, request(s, "PUT", "/api/v1/agent/inventory", c.AccessToken, id, inv), 204, nil)
	}
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", a.AccessToken, id, heartbeat(now)), 204, nil)
	expect(t, request(s, "POST", "/api/v1/agent/policy/report", a.AccessToken, contracts.ID(), contracts.PolicyReport{Revision: "central-mvp-1", ObservedAt: now, Compliance: "unknown", Drift: []string{}, Errors: []string{}}), 204, nil)
	var page DevicePage
	expect(t, request(s, "GET", "/api/v1/admin/devices?limit=1", o.AdminToken, "", nil), 200, &page)
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("missing device cursor")
	}
	first := page.Items[0].DeviceID
	expect(t, request(s, "GET", "/api/v1/admin/devices?limit=1&cursor="+page.NextCursor, o.AdminToken, "", nil), 200, &page)
	if len(page.Items) != 1 || page.Items[0].DeviceID == first || page.NextCursor != "" {
		t.Fatal("bad second device page")
	}
	var reports ReportPage
	expect(t, request(s, "GET", "/api/v1/admin/devices/"+a.DeviceID+"/reports?limit=1", o.AdminToken, "", nil), 200, &reports)
	if len(reports.Items) != 1 || reports.NextCursor == "" {
		t.Fatal("missing reports cursor")
	}
	expect(t, request(s, "GET", "/api/v1/admin/devices/"+a.DeviceID+"/reports?cursor="+reports.NextCursor, o.AdminToken, "", nil), 200, &reports)
	if len(reports.Items) != 2 || reports.NextCursor != "" {
		t.Fatal("bad second report page")
	}
	expect(t, request(s, "GET", "/api/v1/admin/devices/"+b.DeviceID+"/reports", o.AdminToken, "", nil), 200, &reports)
	if len(reports.Items) != 1 {
		t.Fatal("cross-device history leaked")
	}
	now = now.Add(91 * time.Second)
	var detail DeviceDetail
	expect(t, request(s, "GET", "/api/v1/admin/devices/"+a.DeviceID, o.AdminToken, "", nil), 200, &detail)
	if detail.Online || detail.Inventory == nil || detail.PolicyReport == nil {
		t.Fatal("incorrect offline state or missing projection")
	}
	for _, path := range []string{"devices?limit=0", "devices?cursor=bad", "devices/" + a.DeviceID + "/reports?cursor=-1"} {
		expect(t, request(s, "GET", "/api/v1/admin/"+path, o.AdminToken, "", nil), 400, nil)
	}
	expect(t, request(s, "GET", "/api/v1/admin/devices/"+contracts.ID(), o.AdminToken, "", nil), 404, nil)
}
func TestConcurrentReplayAndAtomicFailure(t *testing.T) {
	o := testOptions(t)
	s := openTest(t, o)
	c := enrollTest(t, s, o, 1)
	id := contracts.ID()
	h := heartbeat(time.Now().UTC())
	var wg sync.WaitGroup
	codes := make(chan int, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, id, h).Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 204 {
			t.Fatalf("concurrent request: %d", code)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM reports").Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate writes: %d %v", count, err)
	}
	// Fail the second write to verify report/receipt insertion is rolled back too.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_seen BEFORE UPDATE ON devices BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	nextID := contracts.ID()
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, nextID, h), 503, nil)
	if err := s.db.QueryRow("SELECT COUNT(*) FROM reports").Scan(&count); err != nil || count != 1 {
		t.Fatal("partial report committed", count, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_seen"); err != nil {
		t.Fatal(err)
	}
	expect(t, request(s, "POST", "/api/v1/agent/heartbeat", c.AccessToken, nextID, h), 204, nil)
}
func TestWrongKeyAndFutureSchemaFailClosed(t *testing.T) {
	o := testOptions(t)
	s := openTest(t, o)
	enrollTest(t, s, o, 1)
	s.Close()
	bad := o
	bad.CredentialKey = bytes.Repeat([]byte{4}, 32)
	if other, err := Open(bad); err == nil {
		other.Close()
		t.Fatal("wrong key accepted")
	}
	s = openTest(t, o)
	if _, err := s.db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if other, err := Open(o); err == nil {
		other.Close()
		t.Fatal("future schema accepted")
	}
}

func TestDatabaseURIPaths(t *testing.T) {
	for _, path := range []string{"/tmp/central ?#%.db", "C:/private state/central.db"} {
		uri, err := url.Parse(databaseURI(path))
		if err != nil {
			t.Fatal(err)
		}
		if uri.Host != "" || strings.TrimPrefix(uri.Path, "/") != strings.TrimPrefix(path, "/") {
			t.Fatalf("database path changed: %s", uri)
		}
	}
}

func TestAdminFiltersAndNewestReportPagination(t *testing.T) {
	o := testOptions(t)
	s := openTest(t, o)
	a := enrollTest(t, s, o, 1)
	b := enrollTest(t, s, o, 2)
	if _, err := s.db.Exec("UPDATE devices SET hostname='BRANCH-NORTH' WHERE id=?", a.DeviceID); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		expect(t, request(s, "POST", "/api/v1/agent/heartbeat", a.AccessToken, contracts.ID(), heartbeat(time.Now().UTC())), 204, nil)
	}
	var page DevicePage
	expect(t, request(s, "GET", "/api/v1/admin/devices?status=offline", o.AdminToken, "", nil), 200, &page)
	if page.Total != 2 || page.Online != 1 || page.Offline != 1 || len(page.Items) != 1 || page.Items[0].DeviceID != b.DeviceID {
		t.Fatalf("offline filter/counts: %+v", page)
	}
	expect(t, request(s, "GET", "/api/v1/admin/devices?search=north&status=online", o.AdminToken, "", nil), 200, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].DeviceID != a.DeviceID {
		t.Fatalf("search: %+v", page)
	}
	expect(t, request(s, "GET", "/api/v1/admin/devices?search=%25", o.AdminToken, "", nil), 200, &page)
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("search treated as wildcard")
	}
	expect(t, request(s, "GET", "/api/v1/admin/devices?status=invalid", o.AdminToken, "", nil), 400, nil)
	var history ReportPage
	path := "/api/v1/admin/devices/" + a.DeviceID + "/reports?order=desc&limit=2"
	expect(t, request(s, "GET", path, o.AdminToken, "", nil), 200, &history)
	if len(history.Items) != 2 || history.Items[0].Sequence <= history.Items[1].Sequence || history.NextCursor == "" {
		t.Fatal("newest ordering", history)
	}
	last := history.Items[1].Sequence
	expect(t, request(s, "GET", path+"&cursor="+history.NextCursor, o.AdminToken, "", nil), 200, &history)
	if len(history.Items) != 1 || history.Items[0].Sequence >= last || history.NextCursor != "" {
		t.Fatal("older cursor", history)
	}
	expect(t, request(s, "GET", "/api/v1/admin/devices/"+a.DeviceID+"/reports?order=invalid", o.AdminToken, "", nil), 400, nil)
}
