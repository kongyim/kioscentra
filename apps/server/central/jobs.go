package central

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kiosentra/packages/contracts"
)

const jobsSchema = `
CREATE TABLE IF NOT EXISTS jobs (
 id TEXT PRIMARY KEY, device_id TEXT NOT NULL REFERENCES devices(id), body BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS jobs_device ON jobs(device_id,id);
CREATE TABLE IF NOT EXISTS job_receipts (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, actor TEXT NOT NULL, path TEXT NOT NULL,
 request_id TEXT NOT NULL, body_hash TEXT NOT NULL, job_id TEXT NOT NULL REFERENCES jobs(id),
 status INTEGER NOT NULL, response BLOB NOT NULL, payload BLOB NOT NULL, received_at TEXT NOT NULL,
 UNIQUE(actor,path,request_id)
);
CREATE INDEX IF NOT EXISTS job_receipts_job ON job_receipts(job_id,sequence);
`

type CreateJob struct {
	DeviceID      string `json:"deviceId"`
	TargetVersion string `json:"targetVersion"`
	Scenario      string `json:"scenario"`
	LeaseSeconds  int    `json:"leaseSeconds"`
}

func (v CreateJob) Validate() error {
	if !contracts.ValidID(v.DeviceID) || strings.TrimSpace(v.TargetVersion) == "" || len(v.TargetVersion) > 256 || v.LeaseSeconds < 30 || v.LeaseSeconds > 3600 {
		return errors.New("invalid simulation job")
	}
	_, err := contracts.SimulationScenario("https://simulation.invalid/" + v.Scenario)
	return err
}

type JobRecord struct {
	Job                 contracts.Job        `json:"job"`
	DeviceID            string               `json:"deviceId"`
	Scenario            string               `json:"scenario"`
	InitialLeaseSeconds int                  `json:"initialLeaseSeconds"`
	RetryOf             string               `json:"retryOf,omitempty"`
	Ack                 *contracts.Ack       `json:"ack"`
	Status              *contracts.JobStatus `json:"status"`
	CancelRequestedAt   *time.Time           `json:"cancelRequestedAt"`
	State               string               `json:"state"`
}

func (s *Server) jobState(j JobRecord) string {
	if j.Status != nil && contracts.TerminalState(j.Status.State) {
		return j.Status.State
	}
	if j.Ack != nil && !j.Ack.Accepted {
		return "rejected"
	}
	if j.CancelRequestedAt != nil {
		if j.Ack == nil {
			return "cancelled"
		}
		return "cancellation_requested"
	}
	if !j.Job.LeaseExpiresAt.After(s.now()) {
		return "expired"
	}
	if j.Status != nil {
		return j.Status.State
	}
	if j.Ack != nil {
		return "accepted"
	}
	return "pending"
}
func retryable(state string) bool {
	return state == "rejected" || state == "expired" || state == "failed" || state == "rolled_back" || state == "cancelled"
}
func loadJob(tx *sql.Tx, id string) (JobRecord, error) {
	var b []byte
	var j JobRecord
	if err := tx.QueryRow("SELECT body FROM jobs WHERE id=?", id).Scan(&b); err != nil {
		return j, err
	}
	err := json.Unmarshal(b, &j)
	return j, err
}
func saveJob(tx *sql.Tx, j JobRecord) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO jobs(id,device_id,body) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", j.Job.ID, j.DeviceID, b)
	return err
}

// Mutations and their exact-byte replay receipts commit together.
func (s *Server) jobMutation(w http.ResponseWriter, r *http.Request, actor string, apply func(*sql.Tx, []byte) (JobRecord, int, any, string)) {
	b, err := read(r)
	if err != nil {
		fail(w, 400, "invalid_request")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !contracts.ValidID(key) {
		fail(w, 400, "idempotency_key_required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	defer tx.Rollback()
	var hash, jobID string
	var status int
	var response []byte
	err = tx.QueryRow("SELECT body_hash,job_id,status,response FROM job_receipts WHERE actor=? AND path=? AND request_id=?", actor, r.URL.Path, key).Scan(&hash, &jobID, &status, &response)
	if err == nil {
		if hash != digest(b) {
			fail(w, 409, "idempotency_conflict")
			return
		}
		// Revocation fences even a previously committed renewal response.
		if strings.HasSuffix(r.URL.Path, "/renew") {
			j, e := loadJob(tx, jobID)
			if e != nil {
				fail(w, 503, "storage_unavailable")
				return
			}
			if j.CancelRequestedAt != nil || !j.Job.LeaseExpiresAt.After(s.now()) || (j.Status != nil && contracts.TerminalState(j.Status.State)) {
				fail(w, 410, "lease_unavailable")
				return
			}
		}
		write(w, status, json.RawMessage(response))
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "storage_unavailable")
		return
	}
	j, status, out, code := apply(tx, b)
	if code != "" {
		fail(w, status, code)
		return
	}
	var receipts int
	if err = tx.QueryRow("SELECT COUNT(*) FROM job_receipts WHERE job_id=?", j.Job.ID).Scan(&receipts); err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	if receipts >= 4096 {
		fail(w, 503, "job_receipt_capacity")
		return
	}
	if err = saveJob(tx, j); err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	response, err = json.Marshal(out)
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	_, err = tx.Exec("INSERT INTO job_receipts(actor,path,request_id,body_hash,job_id,status,response,payload,received_at) VALUES(?,?,?,?,?,?,?,?,?)", actor, r.URL.Path, key, digest(b), j.Job.ID, status, response, b, s.now().Format(time.RFC3339Nano))
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	write(w, status, out)
}
func (s *Server) newJob(tx *sql.Tx, v CreateJob, retry string) (JobRecord, int, any, string) {
	bad := func(status int, code string) (JobRecord, int, any, string) { return JobRecord{}, status, nil, code }
	if len(s.options.SimulationPrivateKey) == 0 {
		return bad(409, "simulation_signing_disabled")
	}
	var exists int
	err := tx.QueryRow("SELECT 1 FROM devices WHERE id=?", v.DeviceID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return bad(404, "device_not_found")
	}
	if err != nil {
		return bad(503, "storage_unavailable")
	}
	var count int
	if err = tx.QueryRow("SELECT COUNT(*) FROM jobs WHERE device_id=?", v.DeviceID).Scan(&count); err != nil {
		return bad(503, "storage_unavailable")
	}
	if count >= 1000 {
		return bad(503, "device_job_capacity")
	}
	// A device gets only one outstanding simulation, including unconfirmed cancellation.
	rows, err := tx.Query("SELECT body FROM jobs WHERE device_id=?", v.DeviceID)
	if err != nil {
		return bad(503, "storage_unavailable")
	}
	busy := false
	for rows.Next() {
		var b []byte
		var old JobRecord
		if err = rows.Scan(&b); err != nil {
			break
		}
		if err = json.Unmarshal(b, &old); err != nil {
			break
		}
		state := s.jobState(old)
		if state != "succeeded" && !retryable(state) {
			busy = true
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return bad(503, "storage_unavailable")
	}
	if busy {
		return bad(409, "device_job_active")
	}
	now := s.now()
	j := JobRecord{DeviceID: v.DeviceID, Scenario: v.Scenario, InitialLeaseSeconds: v.LeaseSeconds, RetryOf: retry, State: "pending", Job: contracts.Job{ID: contracts.ID(), Kind: "deployment", TargetVersion: v.TargetVersion, CreatedAt: now, LeaseExpiresAt: now.Add(time.Duration(v.LeaseSeconds) * time.Second)}}
	if err = contracts.SignSimulation(v.DeviceID, &j.Job, v.Scenario, ed25519.PrivateKey(s.options.SimulationPrivateKey)); err != nil {
		return bad(400, "invalid_simulation")
	}
	return j, 201, j, ""
}
func (s *Server) adminJobs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/")
	if path == "jobs" {
		switch r.Method {
		case "GET":
			s.listJobs(w, r, false, "")
		case "POST":
			s.jobMutation(w, r, "admin", func(tx *sql.Tx, b []byte) (JobRecord, int, any, string) {
				v, e := parse[CreateJob](b)
				if e != nil {
					return JobRecord{}, 400, nil, "invalid_request"
				}
				return s.newJob(tx, v, "")
			})
		default:
			fail(w, 405, "method_not_allowed")
		}
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "jobs" || !contracts.ValidID(parts[1]) {
		fail(w, 404, "not_found")
		return
	}
	if len(parts) == 2 && r.Method == "GET" {
		s.jobDetail(w, r, parts[1])
		return
	}
	if len(parts) != 3 || (parts[2] != "cancel" && parts[2] != "retry") {
		fail(w, 404, "not_found")
		return
	}
	if r.Method != "POST" {
		fail(w, 405, "method_not_allowed")
		return
	}
	s.jobMutation(w, r, "admin", func(tx *sql.Tx, b []byte) (JobRecord, int, any, string) {
		var empty struct{}
		if err := contracts.Decode(strings.NewReader(string(b)), &empty); err != nil {
			return JobRecord{}, 400, nil, "invalid_request"
		}
		j, err := loadJob(tx, parts[1])
		if errors.Is(err, sql.ErrNoRows) {
			return j, 404, nil, "job_not_found"
		}
		if err != nil {
			return j, 503, nil, "storage_unavailable"
		}
		state := s.jobState(j)
		if parts[2] == "retry" {
			if !retryable(state) {
				return j, 409, nil, "job_not_retryable"
			}
			return s.newJob(tx, CreateJob{DeviceID: j.DeviceID, TargetVersion: j.Job.TargetVersion, Scenario: j.Scenario, LeaseSeconds: j.InitialLeaseSeconds}, j.Job.ID)
		}
		if j.CancelRequestedAt == nil {
			if contracts.TerminalState(state) || state == "rejected" || state == "expired" {
				return j, 409, nil, "job_terminal"
			}
			now := s.now()
			j.CancelRequestedAt = &now
		}
		j.State = s.jobState(j)
		return j, 200, j, ""
	})
}
func (s *Server) agentJobs(w http.ResponseWriter, r *http.Request, device string) {
	s.listJobs(w, r, true, device)
}
func (s *Server) listJobs(w http.ResponseWriter, r *http.Request, agent bool, device string) {
	limit, err := pageLimit(r)
	cursor := r.URL.Query().Get("cursor")
	if err != nil || (cursor != "" && !contracts.ValidID(cursor)) {
		fail(w, 400, "invalid_pagination")
		return
	}
	if !agent {
		device = r.URL.Query().Get("deviceId")
	}
	if !contracts.ValidID(device) {
		fail(w, 400, "device_id_required")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT body FROM jobs WHERE device_id=? AND id>? ORDER BY id", device, cursor)
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	defer rows.Close()
	items := []JobRecord{}
	jobs := []contracts.Job{}
	next := ""
	for rows.Next() {
		var b []byte
		var j JobRecord
		if err = rows.Scan(&b); err == nil {
			err = json.Unmarshal(b, &j)
		}
		if err != nil {
			fail(w, 503, "storage_unavailable")
			return
		}
		j.State = s.jobState(j)
		if agent && (j.Ack != nil || j.CancelRequestedAt != nil || j.State != "pending") {
			continue
		}
		if len(items) == limit {
			next = items[len(items)-1].Job.ID
			break
		}
		items = append(items, j)
		jobs = append(jobs, j.Job)
	}
	if rows.Err() != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	if agent {
		write(w, 200, contracts.JobPage{Items: jobs, NextCursor: next})
		return
	}
	write(w, 200, struct {
		Items             []JobRecord `json:"items"`
		NextCursor        string      `json:"nextCursor"`
		SimulationEnabled bool        `json:"simulationEnabled"`
	}{items, next, len(s.options.SimulationPrivateKey) != 0})
}
func (s *Server) jobDetail(w http.ResponseWriter, r *http.Request, id string) {
	limit := 100 // Preserve the original detail endpoint default.
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			fail(w, 400, "invalid_limit")
			return
		}
		limit = n
	}
	var cursor int64
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			fail(w, 400, "invalid_cursor")
			return
		}
		cursor = n
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	defer tx.Rollback()
	j, err := loadJob(tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "job_not_found")
		return
	}
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	type event struct {
		Sequence   int64           `json:"sequence"`
		Actor      string          `json:"actor"`
		Path       string          `json:"path"`
		ReceivedAt string          `json:"receivedAt"`
		Payload    json.RawMessage `json:"payload"`
	}
	events := []event{}
	query := "SELECT sequence,actor,path,received_at,payload FROM job_receipts WHERE job_id=?"
	args := []any{id}
	if cursor > 0 {
		query += " AND sequence<?"
		args = append(args, cursor)
	}
	query += " ORDER BY sequence DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := tx.QueryContext(r.Context(), query, args...)
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	for rows.Next() {
		var e event
		if err = rows.Scan(&e.Sequence, &e.Actor, &e.Path, &e.ReceivedAt, &e.Payload); err != nil {
			break
		}
		events = append(events, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	truncated := len(events) > limit
	next := ""
	if truncated {
		events = events[:limit]
		next = strconv.FormatInt(events[len(events)-1].Sequence, 10)
	}
	j.State = s.jobState(j)
	write(w, 200, struct {
		JobRecord
		Events           []event `json:"events"`
		HistoryTruncated bool    `json:"historyTruncated"`
		NextCursor       string  `json:"nextCursor"`
	}{j, events, truncated, next})
}
func (s *Server) agentJobAction(w http.ResponseWriter, r *http.Request, device, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/agent/jobs/"), "/")
	if len(parts) != 2 || !contracts.ValidID(parts[0]) || (parts[1] != "ack" && parts[1] != "status" && parts[1] != "renew") {
		fail(w, 404, "not_found")
		return
	}
	if r.Method != "POST" {
		fail(w, 405, "method_not_allowed")
		return
	}
	s.jobMutation(w, r, device, func(tx *sql.Tx, b []byte) (JobRecord, int, any, string) {
		j, err := loadJob(tx, parts[0])
		bad := func(status int, code string) (JobRecord, int, any, string) { return j, status, nil, code }
		if errors.Is(err, sql.ErrNoRows) || err == nil && j.DeviceID != device {
			return bad(404, "job_not_found")
		}
		if err != nil {
			return bad(503, "storage_unavailable")
		}
		switch parts[1] {
		case "ack":
			v, e := parse[contracts.Ack](b)
			if e != nil {
				return bad(400, "invalid_request")
			}
			if j.CancelRequestedAt != nil || !j.Job.LeaseExpiresAt.After(s.now()) {
				return bad(410, "lease_unavailable")
			}
			if j.Ack != nil {
				return bad(409, "already_acknowledged")
			}
			j.Ack = &v
		case "status":
			v, e := parse[contracts.JobStatus](b)
			if e != nil {
				return bad(400, "invalid_request")
			}
			if j.Ack == nil || !j.Ack.Accepted {
				return bad(409, "job_not_accepted")
			}
			from := ""
			if j.Status != nil {
				from = j.Status.State
			}
			if !contracts.DeploymentTransition(from, v.State) {
				return bad(409, "invalid_transition")
			}
			// Accept ordered, delayed status history even after revocation/expiry. It does
			// not grant a lease. A completion racing cancellation remains a completion.
			j.Status = &v
		case "renew":
			v, e := parse[contracts.Renew](b)
			if e != nil {
				return bad(400, "invalid_request")
			}
			if j.CancelRequestedAt != nil || !j.Job.LeaseExpiresAt.After(s.now()) || (j.Status != nil && contracts.TerminalState(j.Status.State)) {
				return bad(410, "lease_unavailable")
			}
			if j.Ack == nil || !j.Ack.Accepted {
				return bad(409, "job_not_accepted")
			}
			j.Job.LeaseExpiresAt = s.now().Add(time.Duration(v.LeaseSeconds) * time.Second)
			return j, 200, contracts.Lease{LeaseExpiresAt: j.Job.LeaseExpiresAt}, ""
		}
		j.State = s.jobState(j)
		return j, 204, nil, ""
	})
}
