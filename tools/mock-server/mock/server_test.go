package mock

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kiosentra/packages/contracts"
)

func TestProtocolSecurityReplayAndLeases(t *testing.T) {
	now := time.Now().UTC()
	s, e := New(Options{EnrollmentToken: "test-enrollment-token", AdminToken: "test-administrator-token", Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, token, key string, body any) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		r := httptest.NewRecorder()
		s.ServeHTTP(r, req)
		return r
	}
	expect := func(r *httptest.ResponseRecorder, status int) {
		t.Helper()
		if r.Code != status {
			t.Fatalf("want %d got %d: %s", status, r.Code, r.Body.String())
		}
	}
	pub := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	enroll := contracts.Enrollment{EnrollmentToken: "test-enrollment-token", PublicKey: pub, Hostname: "kiosk", AgentVersion: "test"}
	r := call("POST", "/api/v1/enrollments", "", "", enroll)
	expect(r, 200)
	var c contracts.Credential
	if e = json.Unmarshal(r.Body.Bytes(), &c); e != nil {
		t.Fatal(e)
	}
	r = call("POST", "/api/v1/enrollments", "", "", enroll)
	expect(r, 200)
	var same contracts.Credential
	json.Unmarshal(r.Body.Bytes(), &same)
	if same != c {
		t.Fatal("enrollment not idempotent")
	}
	heartbeat := contracts.Heartbeat{AgentVersion: "test", Status: "online", LastActivity: now, Health: "ok"}
	id := contracts.ID()
	expect(call("POST", "/api/v1/agent/heartbeat", "test-administrator-token", id, heartbeat), 401)
	expect(call("POST", "/api/v1/agent/heartbeat", c.AccessToken, "", heartbeat), 400)
	expect(call("POST", "/api/v1/agent/heartbeat", c.AccessToken, id, heartbeat), 204)
	expect(call("POST", "/api/v1/agent/heartbeat", c.AccessToken, id, heartbeat), 204)
	if len(s.devices[c.DeviceID].Reports) != 1 {
		t.Fatal("duplicate telemetry")
	}
	heartbeat.Health = "changed"
	expect(call("POST", "/api/v1/agent/heartbeat", c.AccessToken, id, heartbeat), 409)
	expect(call("POST", "/api/v1/agent/metrics", c.AccessToken, contracts.ID(), map[string]any{"cpuPercent": 200}), 400)
	expect(call("GET", "/dev/state", c.AccessToken, "", nil), 401)
	r = call("POST", "/dev/jobs", "test-administrator-token", "", CreateJob{DeviceID: c.DeviceID, Kind: "deployment", TargetVersion: "2", LeaseSeconds: 60})
	expect(r, 201)
	var j contracts.Job
	json.Unmarshal(r.Body.Bytes(), &j)
	path := "/api/v1/agent/jobs/" + j.ID
	expect(call("GET", "/api/v1/agent/jobs?limit=0", c.AccessToken, "", nil), 400)
	expect(call("POST", path+"/status", c.AccessToken, contracts.ID(), contracts.JobStatus{State: "succeeded", Progress: 100, Message: "test", UpdatedAt: now}), 409)
	expect(call("POST", path+"/ack", c.AccessToken, j.ID, contracts.Ack{Accepted: true, Reason: "test"}), 204)
	expect(call("POST", path+"/ack", c.AccessToken, j.ID, contracts.Ack{Accepted: true, Reason: "test"}), 204)
	expect(call("POST", path+"/renew", c.AccessToken, contracts.ID(), contracts.Renew{LeaseSeconds: 120}), 200)
	// A different device must not address this job.
	enroll.PublicKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	r = call("POST", "/api/v1/enrollments", "", "", enroll)
	expect(r, 200)
	var other contracts.Credential
	json.Unmarshal(r.Body.Bytes(), &other)
	expect(call("POST", path+"/ack", other.AccessToken, contracts.ID(), contracts.Ack{Reason: "reject"}), 404)
	now = now.Add(121 * time.Second)
	expect(call("POST", path+"/renew", c.AccessToken, contracts.ID(), contracts.Renew{LeaseSeconds: 120}), 410)
	rotationID := contracts.ID()
	r = call("POST", "/api/v1/agent/credentials/rotate", c.AccessToken, rotationID, contracts.Rotation{PublicKey: pub})
	expect(r, 200)
	var rotated contracts.Credential
	json.Unmarshal(r.Body.Bytes(), &rotated)
	if rotated.AccessToken == c.AccessToken {
		t.Fatal("credential did not rotate")
	}
	expect(call("POST", "/api/v1/agent/credentials/rotate", c.AccessToken, rotationID, contracts.Rotation{PublicKey: pub}), 200)
	expect(call("GET", "/api/v1/agent/policy", c.AccessToken, "", nil), 401)
	expect(call("GET", "/api/v1/agent/policy", rotated.AccessToken, "", nil), 200)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/policy", nil)
	req.Header.Set("Authorization", "Bearer "+rotated.AccessToken)
	req.Header.Set("If-None-Match", `"mock-1"`)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	expect(rr, 304)
	now = now.Add(24 * time.Hour)
	expect(call("GET", "/api/v1/agent/policy", rotated.AccessToken, "", nil), 401)
}
