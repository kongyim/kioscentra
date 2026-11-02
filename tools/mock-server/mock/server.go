// Package mock implements an in-memory, development-only central server.
// It is deliberately not a production identity authority or deployment service.
package mock

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"kiosentra/packages/contracts"
)

type Options struct {
	EnrollmentToken, AdminToken string
	Now                         func() time.Time
}
type device struct {
	Credential           contracts.Credential
	PublicKey            string
	Reports              []Report
	Jobs                 map[string]*JobRecord
	Seen                 map[string]receipt
	RotationID, OldToken string
	RotationUntil        time.Time
}
type receipt struct {
	Hash   [32]byte
	Status int
	Body   any
}
type Report struct {
	Path       string          `json:"path"`
	ReceivedAt time.Time       `json:"receivedAt"`
	Body       json.RawMessage `json:"body"`
}
type JobRecord struct {
	Job    contracts.Job        `json:"job"`
	Ack    *contracts.Ack       `json:"ack"`
	Status *contracts.JobStatus `json:"status"`
}
type DeviceView struct {
	DeviceID string      `json:"deviceId"`
	Reports  []Report    `json:"reports"`
	Jobs     []JobRecord `json:"jobs"`
}
type Server struct {
	release *contracts.Release
	mu      sync.Mutex
	options Options
	devices map[string]*device
	keys    map[string]string
	policy  contracts.Policy
}

func New(o Options) (*Server, error) {
	if len(o.EnrollmentToken) < 16 || len(o.AdminToken) < 16 || o.EnrollmentToken == o.AdminToken {
		return nil, fmt.Errorf("distinct enrollment and admin tokens of at least 16 characters required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Server{options: o, devices: map[string]*device{}, keys: map[string]string{}, policy: contracts.Policy{SchemaVersion: 1, Revision: "mock-1", Arguments: []string{}, MonitorIntervalSeconds: 30, RestartPolicy: "never", StartupEntries: []string{}}}, nil
}
func (s *Server) now() time.Time { return s.options.Now().UTC() }
func equal(a, b string) bool     { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func fail(w http.ResponseWriter, status int, code string) {
	write(w, status, contracts.Error{Code: code, Message: http.StatusText(status), RequestID: contracts.ID()})
}
func parse[T interface{ Validate() error }](b []byte) (T, error) {
	var v T
	if e := contracts.Decode(strings.NewReader(string(b)), &v); e != nil {
		return v, e
	}
	return v, v.Validate()
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(r.URL.Path, "/dev/") {
		s.dev(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		fail(w, 404, "not_found")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if path == "/enrollments" && r.Method == "POST" {
		b, e := read(r)
		if e != nil {
			fail(w, 400, "invalid_request")
			return
		}
		v, e := parse[contracts.Enrollment](b)
		if e != nil {
			fail(w, 400, "invalid_request")
			return
		}
		if !equal(v.EnrollmentToken, s.options.EnrollmentToken) {
			fail(w, 401, "invalid_enrollment_token")
			return
		}
		if id, ok := s.keys[v.PublicKey]; ok {
			write(w, 200, s.devices[id].Credential)
			return
		}
		if len(s.devices) >= 1000 {
			fail(w, 503, "mock_capacity")
			return
		}
		c := contracts.Credential{DeviceID: contracts.ID(), AccessToken: token(), ExpiresAt: s.now().Add(24 * time.Hour)}
		s.devices[c.DeviceID] = &device{Credential: c, PublicKey: v.PublicKey, Reports: []Report{}, Jobs: map[string]*JobRecord{}, Seen: map[string]receipt{}}
		s.keys[v.PublicKey] = c.DeviceID
		write(w, 200, c)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		fail(w, 401, "invalid_device_credential")
		return
	}
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var d *device
	for _, candidate := range s.devices {
		if equal(auth, candidate.Credential.AccessToken) && candidate.Credential.ExpiresAt.After(s.now()) {
			d = candidate
			break
		}
		if path == "/agent/credentials/rotate" && r.Method == "POST" && candidate.RotationUntil.After(s.now()) && equal(auth, candidate.OldToken) && r.Header.Get("Idempotency-Key") == candidate.RotationID {
			d = candidate
			break
		}
	}
	if d == nil {
		fail(w, 401, "invalid_device_credential")
		return
	}
	if r.Method == "GET" {
		s.get(w, r, path, d)
		return
	}
	b, e := read(r)
	if e != nil {
		fail(w, 400, "invalid_request")
		return
	}
	id := r.Header.Get("Idempotency-Key")
	if !contracts.ValidID(id) {
		fail(w, 400, "idempotency_key_required")
		return
	}
	key := r.Method + " " + path + " " + id
	hash := sha256.Sum256(b)
	if old, ok := d.Seen[key]; ok {
		if old.Hash != hash {
			fail(w, 409, "idempotency_conflict")
			return
		}
		write(w, old.Status, old.Body)
		return
	}
	if len(d.Seen) >= 10000 {
		fail(w, 503, "mock_capacity")
		return
	}
	finish := func(status int, v any) { d.Seen[key] = receipt{hash, status, v}; write(w, status, v) }
	if path == "/agent/credentials/rotate" && r.Method == "POST" {
		v, e := parse[contracts.Rotation](b)
		if e != nil || v.PublicKey != d.PublicKey {
			fail(w, 400, "invalid_rotation")
			return
		}
		d.OldToken = d.Credential.AccessToken
		d.RotationID = id
		d.RotationUntil = s.now().Add(5 * time.Minute)
		d.Credential.AccessToken = token()
		d.Credential.ExpiresAt = s.now().Add(24 * time.Hour)
		finish(200, d.Credential)
		return
	}
	if strings.HasPrefix(path, "/agent/jobs/") && r.Method == "POST" {
		s.jobAction(w, path, b, d, finish)
		return
	}
	var valid error
	switch r.Method + " " + path {
	case "POST /agent/heartbeat":
		_, valid = parse[contracts.Heartbeat](b)
	case "POST /agent/metrics":
		_, valid = parse[contracts.Metrics](b)
	case "PUT /agent/inventory":
		_, valid = parse[contracts.Inventory](b)
	case "POST /agent/policy/report":
		_, valid = parse[contracts.PolicyReport](b)
	case "POST /agent/update-status":
		_, valid = parse[contracts.UpdateStatus](b)
	default:
		fail(w, 404, "not_found")
		return
	}
	if valid != nil {
		fail(w, 400, "invalid_request")
		return
	}
	if len(d.Reports) >= 1000 {
		d.Reports = d.Reports[1:]
	}
	d.Reports = append(d.Reports, Report{Path: path, ReceivedAt: s.now(), Body: append(json.RawMessage{}, b...)})
	finish(204, nil)
}
func read(r *http.Request) ([]byte, error) {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return nil, fmt.Errorf("JSON required")
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, contracts.MaxBody+1))
	if len(b) > contracts.MaxBody {
		return nil, fmt.Errorf("body too large")
	}
	return b, e
}
func (s *Server) get(w http.ResponseWriter, r *http.Request, path string, d *device) {
	switch path {
	case "/agent/policy":
		etag := `"` + s.policy.Revision + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(304)
			return
		}
		write(w, 200, s.policy)
	case "/agent/releases/latest":
		if s.release == nil {
			write(w, 200, nilRelease())
		} else {
			write(w, 200, s.release)
		}
	case "/agent/jobs":
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > 100 {
				fail(w, 400, "invalid_limit")
				return
			}
			limit = n
		}
		cursor := r.URL.Query().Get("cursor")
		if cursor != "" && !contracts.ValidID(cursor) {
			fail(w, 400, "invalid_cursor")
			return
		}
		ids := []string{}
		for id, j := range d.Jobs {
			if j.Ack == nil && j.Job.LeaseExpiresAt.After(s.now()) && id > cursor {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		out := contracts.JobPage{Items: []contracts.Job{}}
		for i, id := range ids {
			if i == limit {
				out.NextCursor = ids[i-1]
				break
			}
			out.Items = append(out.Items, d.Jobs[id].Job)
		}
		write(w, 200, out)
	default:
		fail(w, 404, "not_found")
	}
}
func nilRelease() any { return json.RawMessage("null") }
func (s *Server) jobAction(w http.ResponseWriter, path string, b []byte, d *device, finish func(int, any)) {
	parts := strings.Split(strings.TrimPrefix(path, "/agent/jobs/"), "/")
	if len(parts) != 2 || !contracts.ValidID(parts[0]) {
		fail(w, 404, "not_found")
		return
	}
	j, ok := d.Jobs[parts[0]]
	if !ok {
		fail(w, 404, "job_not_found")
		return
	}
	if !j.Job.LeaseExpiresAt.After(s.now()) {
		fail(w, 410, "lease_expired")
		return
	}
	switch parts[1] {
	case "ack":
		v, e := parse[contracts.Ack](b)
		if e != nil {
			fail(w, 400, "invalid_request")
			return
		}
		if j.Ack != nil {
			fail(w, 409, "already_acknowledged")
			return
		}
		j.Ack = &v
		finish(204, nil)
	case "status":
		v, e := parse[contracts.JobStatus](b)
		if e != nil {
			fail(w, 400, "invalid_request")
			return
		}
		if j.Ack == nil || !j.Ack.Accepted {
			fail(w, 409, "job_not_accepted")
			return
		}
		if j.Status != nil && (j.Status.State == "succeeded" || j.Status.State == "failed" || j.Status.State == "cancelled" || j.Status.State == "rolled_back") {
			fail(w, 409, "job_terminal")
			return
		}
		j.Status = &v
		finish(204, nil)
	case "renew":
		v, e := parse[contracts.Renew](b)
		if e != nil {
			fail(w, 400, "invalid_request")
			return
		}
		if j.Ack == nil || !j.Ack.Accepted {
			fail(w, 409, "job_not_accepted")
			return
		}
		if j.Status != nil && (j.Status.State == "succeeded" || j.Status.State == "failed" || j.Status.State == "cancelled" || j.Status.State == "rolled_back") {
			fail(w, 409, "job_terminal")
			return
		}
		j.Job.LeaseExpiresAt = s.now().Add(time.Duration(v.LeaseSeconds) * time.Second)
		finish(200, contracts.Lease{LeaseExpiresAt: j.Job.LeaseExpiresAt})
	default:
		fail(w, 404, "not_found")
	}
}

// CreateJob is the authenticated development HTTP request, not a production job API.
type CreateJob struct {
	DeviceID      string `json:"deviceId"`
	Kind          string `json:"kind"`
	TargetVersion string `json:"targetVersion"`
	LeaseSeconds  int    `json:"leaseSeconds"`
}

func (v CreateJob) Validate() error {
	if !contracts.ValidID(v.DeviceID) || (v.Kind != "deployment" && v.Kind != "agent_update" && v.Kind != "diagnostics") || v.LeaseSeconds < 30 || v.LeaseSeconds > 3600 || len(v.TargetVersion) > 256 {
		return fmt.Errorf("invalid development job")
	}
	return nil
}
func (s *Server) dev(w http.ResponseWriter, r *http.Request) {
	if !equal(r.Header.Get("Authorization"), "Bearer "+s.options.AdminToken) {
		fail(w, 401, "invalid_admin_token")
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /dev/state":
		views := []DeviceView{}
		for id, d := range s.devices {
			v := DeviceView{DeviceID: id, Reports: d.Reports, Jobs: []JobRecord{}}
			for _, j := range d.Jobs {
				v.Jobs = append(v.Jobs, *j)
			}
			views = append(views, v)
		}
		write(w, 200, views)
	case "POST /dev/jobs":
		b, e := read(r)
		if e != nil {
			fail(w, 400, "invalid_request")
			return
		}
		v, e := parse[CreateJob](b)
		if e != nil {
			fail(w, 400, "invalid_request")
			return
		}
		d, ok := s.devices[v.DeviceID]
		if !ok {
			fail(w, 404, "device_not_found")
			return
		}
		if len(d.Jobs) >= 1000 {
			fail(w, 503, "mock_capacity")
			return
		}
		j := contracts.Job{ID: contracts.ID(), Kind: v.Kind, CreatedAt: s.now(), LeaseExpiresAt: s.now().Add(time.Duration(v.LeaseSeconds) * time.Second), TargetVersion: v.TargetVersion}
		if v.Kind != "diagnostics" {
			digest := sha256.Sum256([]byte("simulation only"))
			j.Release = &contracts.Release{Version: v.TargetVersion, PackageType: "simulation", URL: "https://invalid.example/simulation-only", SHA256: hex.EncodeToString(digest[:]), Signature: base64.StdEncoding.EncodeToString(make([]byte, 64)), KeyID: "mock-untrusted", SizeBytes: 15}
		}
		if j.Kind == "agent_update" {
			s.release = j.Release
		}
		d.Jobs[j.ID] = &JobRecord{Job: j}
		write(w, 201, j)
	default:
		fail(w, 404, "not_found")
	}
}
