package central

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

func jobOptions(t *testing.T) Options {
	o := testOptions(t)
	o.SimulationPrivateKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	return o
}
func createTestJob(t *testing.T, s *Server, o Options, c contracts.Credential) JobRecord {
	t.Helper()
	var j JobRecord
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, contracts.ID(), CreateJob{c.DeviceID, "2.0", "success", 300}), 201, &j)
	return j
}
func jobStatus(state string, now time.Time) contracts.JobStatus {
	return contracts.JobStatus{State: state, Progress: 50, Message: "simulation", UpdatedAt: now}
}
func TestJobsPersistentReplayCancellationAndRetry(t *testing.T) {
	o := jobOptions(t)
	now := time.Now().UTC()
	o.Now = func() time.Time { return now }
	s := openTest(t, o)
	c := enrollTest(t, s, o, 1)
	other := enrollTest(t, s, o, 2)
	body := CreateJob{c.DeviceID, "2.0", "success", 300}
	key := contracts.ID()
	var j JobRecord
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, key, body), 201, &j)
	if err := contracts.VerifySimulation(c.DeviceID, j.Job, ed25519.PrivateKey(o.SimulationPrivateKey).Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, o)
	var replay JobRecord
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, key, body), 201, &replay)
	if replay.Job.ID != j.Job.ID {
		t.Fatal("duplicate job after restart")
	}
	changed := body
	changed.TargetVersion = "3.0"
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, key, changed), 409, nil)
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, contracts.ID(), body), 409, nil)
	var page contracts.JobPage
	expect(t, request(s, "GET", "/api/v1/agent/jobs", c.AccessToken, "", nil), 200, &page)
	if len(page.Items) != 1 {
		t.Fatal(page)
	}
	expect(t, request(s, "GET", "/api/v1/agent/jobs", other.AccessToken, "", nil), 200, &page)
	if len(page.Items) != 0 {
		t.Fatal("cross device jobs")
	}
	path := "/api/v1/agent/jobs/" + j.Job.ID
	expect(t, request(s, "POST", path+"/ack", other.AccessToken, contracts.ID(), contracts.Ack{Accepted: true, Reason: "test"}), 404, nil)
	expect(t, request(s, "POST", path+"/status", c.AccessToken, contracts.ID(), jobStatus("accepted", now)), 409, nil)
	ackKey := contracts.ID()
	ack := contracts.Ack{Accepted: true, Reason: "simulation"}
	expect(t, request(s, "POST", path+"/ack", c.AccessToken, ackKey, ack), 204, nil)
	expect(t, request(s, "POST", path+"/ack", c.AccessToken, ackKey, ack), 204, nil)
	expect(t, request(s, "POST", path+"/status", c.AccessToken, contracts.ID(), jobStatus("succeeded", now)), 409, nil)
	expect(t, request(s, "POST", path+"/status", c.AccessToken, contracts.ID(), jobStatus("accepted", now)), 204, nil)
	renewKey := contracts.ID()
	var lease contracts.Lease
	expect(t, request(s, "POST", path+"/renew", c.AccessToken, renewKey, contracts.Renew{LeaseSeconds: 300}), 200, &lease)
	s.Close()
	s = openTest(t, o)
	now = now.Add(time.Second)
	var again contracts.Lease
	expect(t, request(s, "POST", path+"/renew", c.AccessToken, renewKey, contracts.Renew{LeaseSeconds: 300}), 200, &again)
	if again != lease {
		t.Fatal("renewal replay extended lease")
	}
	cancelKey := contracts.ID()
	expect(t, request(s, "POST", "/api/v1/admin/jobs/"+j.Job.ID+"/cancel", o.AdminToken, cancelKey, struct{}{}), 200, &replay)
	if replay.State != "cancellation_requested" {
		t.Fatal(replay.State)
	}
	expect(t, request(s, "POST", path+"/renew", c.AccessToken, renewKey, contracts.Renew{LeaseSeconds: 300}), 410, nil)
	expect(t, request(s, "POST", path+"/renew", c.AccessToken, contracts.ID(), contracts.Renew{LeaseSeconds: 300}), 410, nil)
	expect(t, request(s, "POST", "/api/v1/admin/jobs/"+j.Job.ID+"/retry", o.AdminToken, contracts.ID(), struct{}{}), 409, nil)
	// One in-flight step may complete before cancellation is observed; retain the truth.
	expect(t, request(s, "POST", path+"/status", c.AccessToken, contracts.ID(), jobStatus("downloading", now)), 204, nil)
	statusKey := contracts.ID()
	expect(t, request(s, "POST", path+"/status", c.AccessToken, statusKey, jobStatus("cancelled", now)), 204, nil)
	expect(t, request(s, "POST", path+"/status", c.AccessToken, statusKey, jobStatus("cancelled", now)), 204, nil)
	expect(t, request(s, "POST", path+"/status", c.AccessToken, contracts.ID(), jobStatus("failed", now)), 409, nil)
	retryKey := contracts.ID()
	expect(t, request(s, "POST", "/api/v1/admin/jobs/"+j.Job.ID+"/retry", o.AdminToken, retryKey, struct{}{}), 201, &replay)
	if replay.RetryOf != j.Job.ID || replay.Job.ID == j.Job.ID {
		t.Fatal("retry overwrote history")
	}
	var detail struct {
		JobRecord
		Events []json.RawMessage `json:"events"`
	}
	expect(t, request(s, "GET", "/api/v1/admin/jobs/"+j.Job.ID, o.AdminToken, "", nil), 200, &detail)
	if detail.State != "cancelled" || len(detail.Events) != 7 {
		t.Fatalf("history state=%s count=%d", detail.State, len(detail.Events))
	}
	// Two records can be traversed without duplicates.
	var adminPage struct {
		Items      []JobRecord `json:"items"`
		NextCursor string      `json:"nextCursor"`
	}
	expect(t, request(s, "GET", "/api/v1/admin/jobs?deviceId="+c.DeviceID+"&limit=1", o.AdminToken, "", nil), 200, &adminPage)
	if len(adminPage.Items) != 1 || adminPage.NextCursor == "" {
		t.Fatal(adminPage)
	}
	first := adminPage.Items[0].Job.ID
	expect(t, request(s, "GET", "/api/v1/admin/jobs?deviceId="+c.DeviceID+"&limit=1&cursor="+adminPage.NextCursor, o.AdminToken, "", nil), 200, &adminPage)
	if len(adminPage.Items) != 1 || adminPage.Items[0].Job.ID == first || adminPage.NextCursor != "" {
		t.Fatal(adminPage)
	}
}
func TestJobsAtomicRollbackConcurrentCreateAndMigration(t *testing.T) {
	o := jobOptions(t)
	s := openTest(t, o)
	c := enrollTest(t, s, o, 1)
	if _, err := s.db.Exec("DROP TABLE job_receipts; DROP TABLE jobs; PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, o)
	if _, err := s.db.Exec("CREATE TRIGGER fail_job_receipt BEFORE INSERT ON job_receipts BEGIN SELECT RAISE(FAIL,'injected'); END"); err != nil {
		t.Fatal(err)
	}
	key := contracts.ID()
	body := CreateJob{c.DeviceID, "2.0", "success", 300}
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, key, body), 503, nil)
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count)
	if count != 0 {
		t.Fatal("partial job commit")
	}
	s.db.Exec("DROP TRIGGER fail_job_receipt")
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, key, body)
			var j JobRecord
			if w.Code != 201 {
				ids <- "error"
				return
			}
			json.Unmarshal(w.Body.Bytes(), &j)
			ids <- j.Job.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if !contracts.ValidID(id) {
			t.Fatal(id)
		}
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("concurrent duplicate")
		}
	}
	path := "/api/v1/agent/jobs/" + first + "/ack"
	ackKey := contracts.ID()
	ack := contracts.Ack{Accepted: true, Reason: "test"}
	s.db.Exec("CREATE TRIGGER fail_job_receipt BEFORE INSERT ON job_receipts BEGIN SELECT RAISE(FAIL,'injected'); END")
	expect(t, request(s, "POST", path, c.AccessToken, ackKey, ack), 503, nil)
	var j JobRecord
	expect(t, request(s, "GET", "/api/v1/admin/jobs/"+first, o.AdminToken, "", nil), 200, &j)
	if j.Ack != nil {
		t.Fatal("partial ack")
	}
	s.db.Exec("DROP TRIGGER fail_job_receipt")
	expect(t, request(s, "POST", path, c.AccessToken, ackKey, ack), 204, nil)
}
func TestJobsDisabledInvalidExpiredRejectedAndPendingCancel(t *testing.T) {
	o := testOptions(t)
	s := openTest(t, o)
	c := enrollTest(t, s, o, 1)
	body := CreateJob{c.DeviceID, "2.0", "success", 30}
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, contracts.ID(), body), 409, nil)
	s.Close()
	o.SimulationPrivateKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{4}, 32))
	now := time.Now().UTC()
	o.Now = func() time.Time { return now }
	s = openTest(t, o)
	expect(t, request(s, "POST", "/api/v1/admin/jobs", c.AccessToken, contracts.ID(), body), 401, nil)
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.EnrollmentToken, contracts.ID(), body), 401, nil)
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, "", body), 400, nil)
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, contracts.ID(), []byte(`{"kind":"agent_update"}`)), 400, nil)
	invalid := body
	invalid.Scenario = "shell"
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, contracts.ID(), invalid), 400, nil)
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, contracts.ID(), []byte(strings.Repeat("x", contracts.MaxBody+1))), 400, nil)
	var j JobRecord
	expect(t, request(s, "POST", "/api/v1/admin/jobs", o.AdminToken, contracts.ID(), body), 201, &j)
	now = now.Add(31 * time.Second)
	expect(t, request(s, "POST", "/api/v1/agent/jobs/"+j.Job.ID+"/ack", c.AccessToken, contracts.ID(), contracts.Ack{Reason: "test"}), 410, nil)
	expect(t, request(s, "GET", "/api/v1/admin/jobs/"+j.Job.ID, o.AdminToken, "", nil), 200, &j)
	if j.State != "expired" {
		t.Fatal(j.State)
	}
	j = createTestJob(t, s, o, c)
	expect(t, request(s, "POST", "/api/v1/agent/jobs/"+j.Job.ID+"/ack", c.AccessToken, contracts.ID(), contracts.Ack{Reason: "disabled"}), 204, nil)
	expect(t, request(s, "GET", "/api/v1/admin/jobs/"+j.Job.ID, o.AdminToken, "", nil), 200, &j)
	if j.State != "rejected" {
		t.Fatal(j.State)
	}
	j = createTestJob(t, s, o, c)
	expect(t, request(s, "POST", "/api/v1/admin/jobs/"+j.Job.ID+"/cancel", o.AdminToken, contracts.ID(), struct{}{}), 200, &j)
	if j.State != "cancelled" {
		t.Fatal(j.State)
	}
	var page contracts.JobPage
	expect(t, request(s, "GET", "/api/v1/agent/jobs", c.AccessToken, "", nil), 200, &page)
	if len(page.Items) != 0 {
		t.Fatal("cancelled job dispatched")
	}
}

func TestJobCompletionRacingCancellationAndRevokedReplay(t *testing.T) {
	o := jobOptions(t)
	s := openTest(t, o)
	c := enrollTest(t, s, o, 1)
	j := createTestJob(t, s, o, c)
	path := "/api/v1/agent/jobs/" + j.Job.ID
	expect(t, request(s, "POST", path+"/ack", c.AccessToken, contracts.ID(), contracts.Ack{Accepted: true, Reason: "test"}), 204, nil)
	for _, state := range []string{"accepted", "downloading", "verifying", "installing", "validating"} {
		expect(t, request(s, "POST", path+"/status", c.AccessToken, contracts.ID(), jobStatus(state, time.Now().UTC())), 204, nil)
	}
	key := contracts.ID()
	empty := struct{}{}
	expect(t, request(s, "POST", "/api/v1/admin/jobs/"+j.Job.ID+"/cancel", o.AdminToken, key, empty), 200, nil)
	s.Close()
	s = openTest(t, o)
	expect(t, request(s, "POST", "/api/v1/admin/jobs/"+j.Job.ID+"/cancel", o.AdminToken, key, empty), 200, nil)
	statusKey := contracts.ID()
	status := jobStatus("succeeded", time.Now().UTC())
	expect(t, request(s, "POST", path+"/status", c.AccessToken, statusKey, status), 204, nil)
	s.Close()
	s = openTest(t, o)
	expect(t, request(s, "POST", path+"/status", c.AccessToken, statusKey, status), 204, nil)
	var current JobRecord
	expect(t, request(s, "GET", "/api/v1/admin/jobs/"+j.Job.ID, o.AdminToken, "", nil), 200, &current)
	if current.State != "succeeded" || current.CancelRequestedAt == nil {
		t.Fatal("completion race history lost")
	}
	expect(t, request(s, "POST", "/api/v1/admin/jobs/"+j.Job.ID+"/retry", o.AdminToken, contracts.ID(), empty), 409, nil)
}

func TestSimulationKeyValidation(t *testing.T) {
	for _, key := range [][]byte{[]byte("short"), bytes.Repeat([]byte{1}, 64)} {
		o := testOptions(t)
		o.SimulationPrivateKey = key
		s, err := Open(o)
		if err == nil {
			s.Close()
			t.Fatal("invalid key accepted")
		}
	}
}

func TestJobHistoryPagination(t *testing.T) {
	o := jobOptions(t)
	s := openTest(t, o)
	c := enrollTest(t, s, o, 1)
	j := createTestJob(t, s, o, c)
	agentPath := "/api/v1/agent/jobs/" + j.Job.ID
	path := "/api/v1/admin/jobs/" + j.Job.ID
	expect(t, request(s, "POST", agentPath+"/ack", c.AccessToken, contracts.ID(), contracts.Ack{Accepted: true, Reason: "simulation"}), 204, nil)
	for i := 0; i < 103; i++ {
		expect(t, request(s, "POST", agentPath+"/renew", c.AccessToken, contracts.ID(), contracts.Renew{LeaseSeconds: 300}), 200, nil)
	}
	type page struct {
		Events []struct {
			Sequence int64 `json:"sequence"`
		} `json:"events"`
		NextCursor       string `json:"nextCursor"`
		HistoryTruncated bool   `json:"historyTruncated"`
	}
	var first page
	expect(t, request(s, "GET", path, o.AdminToken, "", nil), 200, &first)
	if len(first.Events) != 100 || !first.HistoryTruncated || first.NextCursor == "" {
		t.Fatalf("bad default page: %+v", first)
	}
	// Restart and append a new receipt: older-page traversal must remain stable.
	s.Close()
	s = openTest(t, o)
	expect(t, request(s, "POST", agentPath+"/renew", c.AccessToken, contracts.ID(), contracts.Renew{LeaseSeconds: 300}), 200, nil)
	seen := map[int64]bool{}
	previous := int64(1<<63 - 1)
	for _, e := range first.Events {
		seen[e.Sequence] = true
		previous = e.Sequence
	}
	cursor := first.NextCursor
	for cursor != "" {
		var next page
		expect(t, request(s, "GET", path+"?limit=2&cursor="+cursor, o.AdminToken, "", nil), 200, &next)
		if len(next.Events) > 2 || next.HistoryTruncated != (next.NextCursor != "") {
			t.Fatalf("bad page: %+v", next)
		}
		for _, e := range next.Events {
			if seen[e.Sequence] || e.Sequence >= previous {
				t.Fatal("duplicate or unordered history")
			}
			seen[e.Sequence] = true
			previous = e.Sequence
		}
		cursor = next.NextCursor
	}
	if len(seen) != 105 {
		t.Fatalf("lost history: got %d events", len(seen))
	}
	var empty page
	expect(t, request(s, "GET", path+"?cursor=1", o.AdminToken, "", nil), 200, &empty)
	if len(empty.Events) != 0 || empty.NextCursor != "" || empty.HistoryTruncated {
		t.Fatal("expected empty final page")
	}
	for _, q := range []string{"limit=0", "limit=101", "limit=no", "cursor=0", "cursor=-1", "cursor=no", "cursor=9223372036854775808"} {
		expect(t, request(s, "GET", path+"?"+q, o.AdminToken, "", nil), 400, nil)
	}
	expect(t, request(s, "GET", path+"?limit=2", c.AccessToken, "", nil), 401, nil)
}
