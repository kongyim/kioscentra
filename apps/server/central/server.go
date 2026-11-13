// Package central implements the persistent, single-instance central server MVP.
package central

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kiosentra/packages/contracts"
	_ "modernc.org/sqlite"
)

type Options struct {
	DatabasePath    string
	EnrollmentToken string
	AdminToken      string
	// CredentialKey must remain unchanged when reopening the same database.
	CredentialKey []byte
	Now           func() time.Time
	// Optional, development-only Ed25519 key. Absent disables job creation.
	SimulationPrivateKey []byte
}

type Server struct {
	db      *sql.DB
	options Options
}

const schema = `
CREATE TABLE IF NOT EXISTS metadata (name TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS devices (
 id TEXT PRIMARY KEY, public_key TEXT NOT NULL UNIQUE, hostname TEXT NOT NULL,
 agent_version TEXT NOT NULL, enrolled_at TEXT NOT NULL, expires_at TEXT NOT NULL,
 token_hash TEXT NOT NULL UNIQUE, last_seen_at TEXT
);
CREATE TABLE IF NOT EXISTS reports (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 device_id TEXT NOT NULL REFERENCES devices(id), method TEXT NOT NULL, path TEXT NOT NULL,
 request_id TEXT NOT NULL, body_hash TEXT NOT NULL, received_at TEXT NOT NULL, body BLOB NOT NULL,
 UNIQUE(device_id, method, path, request_id)
);
CREATE TABLE IF NOT EXISTS credential_rotations (
 device_id TEXT NOT NULL REFERENCES devices(id), request_id TEXT NOT NULL,
 old_token_hash TEXT NOT NULL UNIQUE, body_hash TEXT NOT NULL,
 expires_at TEXT NOT NULL, replay_until TEXT NOT NULL,
 PRIMARY KEY(device_id, request_id)
);
CREATE INDEX IF NOT EXISTS reports_device_sequence ON reports(device_id, sequence);
CREATE INDEX IF NOT EXISTS reports_latest ON reports(device_id, path, sequence DESC);
`

func Open(o Options) (*Server, error) {
	if len(o.EnrollmentToken) < 32 || len(o.EnrollmentToken) > 256 || len(o.AdminToken) < 32 || o.EnrollmentToken == o.AdminToken || len(o.CredentialKey) != 32 {
		return nil, errors.New("distinct enrollment/admin tokens of at least 32 characters and a 32-byte credential key required (enrollment token at most 256 characters)")
	}
	if !filepath.IsAbs(o.DatabasePath) {
		return nil, errors.New("database path must be absolute")
	}
	if len(o.SimulationPrivateKey) != 0 && (len(o.SimulationPrivateKey) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(o.SimulationPrivateKey[:32]), o.SimulationPrivateKey)) {
		return nil, errors.New("invalid simulation private key")
	}
	o.SimulationPrivateKey = append([]byte(nil), o.SimulationPrivateKey...)
	if o.Now == nil {
		o.Now = time.Now
	}
	o.CredentialKey = append([]byte(nil), o.CredentialKey...)
	if err := os.MkdirAll(filepath.Dir(o.DatabasePath), 0700); err != nil {
		return nil, err
	}
	// Refuse a symlink or non-regular database rather than following it.
	if info, err := os.Lstat(o.DatabasePath); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("database must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(o.DatabasePath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", databaseURI(o.DatabasePath))
	if err != nil {
		return nil, err
	}
	// Serialize local transactions; the MVP is one process with one SQLite connection.
	db.SetMaxOpenConns(1)
	s := &Server{db: db, options: o}
	if err = s.initialize(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func databaseURI(path string) string {
	path = filepath.ToSlash(path)
	// A Windows drive letter is part of the path, never a URI authority.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = url.Values{"_pragma": {"busy_timeout(5000)", "foreign_keys(1)", "journal_mode(WAL)", "synchronous(FULL)"}}.Encode()
	return u.String()
}

func (s *Server) initialize() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 0 && version != 1 && version != 2 && version != 3 {
		return fmt.Errorf("unsupported database schema version %d", version)
	}
	if _, err = tx.Exec(schema + jobsSchema); err != nil {
		return err
	}
	keyCheck := s.sign("kiosentra-central-key-check-v1")
	if _, err = tx.Exec("INSERT OR IGNORE INTO metadata(name,value) VALUES('credential_key_check',?)", keyCheck); err != nil {
		return err
	}
	var stored string
	if err = tx.QueryRow("SELECT value FROM metadata WHERE name='credential_key_check'").Scan(&stored); err != nil {
		return err
	}
	if !equal(stored, keyCheck) {
		return errors.New("credential key does not match this database")
	}
	if _, err = tx.Exec("PRAGMA user_version=3"); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Server) Close() error   { return s.db.Close() }
func (s *Server) now() time.Time { return s.options.Now().UTC() }
func (s *Server) sign(value string) string {
	h := hmac.New(sha256.New, s.options.CredentialKey)
	h.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func digest(value []byte) string { h := sha256.Sum256(value); return hex.EncodeToString(h[:]) }
func equal(a, b string) bool     { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (s *Server) credential(id, publicKey, expires string) contracts.Credential {
	t, _ := time.Parse(time.RFC3339Nano, expires)
	return contracts.Credential{DeviceID: id, AccessToken: s.sign("device-v1\n" + id + "\n" + publicKey + "\n" + expires), ExpiresAt: t}
}
func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}
func fail(w http.ResponseWriter, status int, code string) {
	write(w, status, contracts.Error{Code: code, Message: http.StatusText(status), RequestID: contracts.ID()})
}
func read(r *http.Request) ([]byte, error) {
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		return nil, errors.New("JSON required")
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, contracts.MaxBody+1))
	if len(b) > contracts.MaxBody {
		return nil, errors.New("body too large")
	}
	return b, err
}
func parse[T interface{ Validate() error }](b []byte) (T, error) {
	var v T
	if err := contracts.Decode(bytes.NewReader(b), &v); err != nil {
		return v, err
	}
	return v, v.Validate()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
		if !equal(r.Header.Get("Authorization"), "Bearer "+s.options.AdminToken) {
			fail(w, 401, "invalid_admin_token")
			return
		}
		s.admin(w, r)
		return
	}
	if r.URL.Path == "/api/v1/enrollments" && r.Method == "POST" {
		s.enroll(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/v1/agent/") {
		fail(w, 404, "not_found")
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		fail(w, 401, "invalid_device_credential")
		return
	}
	if r.Method == "POST" && r.URL.Path == "/api/v1/agent/credentials/rotate" {
		s.rotate(w, r, digest([]byte(strings.TrimPrefix(auth, "Bearer "))))
		return
	}
	var deviceID, expires string
	err := s.db.QueryRowContext(r.Context(), "SELECT id,expires_at FROM devices WHERE token_hash=?", digest([]byte(strings.TrimPrefix(auth, "Bearer ")))).Scan(&deviceID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "invalid_device_credential")
		return
	}
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	expiry, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || !expiry.After(s.now()) {
		fail(w, 401, "invalid_device_credential")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if r.Method == "GET" {
		if path == "/agent/jobs" {
			s.agentJobs(w, r, deviceID)
			return
		}
		s.get(w, r, path)
		return
	}
	if strings.HasPrefix(path, "/agent/jobs/") {
		s.agentJobAction(w, r, deviceID, path)
		return
	}
	s.report(w, r, deviceID, path)
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	b, err := read(r)
	if err != nil {
		fail(w, 400, "invalid_request")
		return
	}
	v, err := parse[contracts.Enrollment](b)
	if err != nil {
		fail(w, 400, "invalid_request")
		return
	}
	if !equal(v.EnrollmentToken, s.options.EnrollmentToken) {
		fail(w, 401, "invalid_enrollment_token")
		return
	}
	// Normalize the public key so alternate base64 spellings cannot create duplicates.
	publicBytes, _ := base64.StdEncoding.DecodeString(v.PublicKey)
	v.PublicKey = base64.StdEncoding.EncodeToString(publicBytes)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	defer tx.Rollback()
	var id, expires string
	err = tx.QueryRowContext(r.Context(), "SELECT id,expires_at FROM devices WHERE public_key=?", v.PublicKey).Scan(&id, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		id = contracts.ID()
		now := s.now()
		expires = now.Add(24 * time.Hour).Format(time.RFC3339Nano)
		c := s.credential(id, v.PublicKey, expires)
		_, err = tx.ExecContext(r.Context(), "INSERT INTO devices(id,public_key,hostname,agent_version,enrolled_at,expires_at,token_hash) VALUES(?,?,?,?,?,?,?)", id, v.PublicKey, v.Hostname, v.AgentVersion, now.Format(time.RFC3339Nano), expires, digest([]byte(c.AccessToken)))
	}
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	c := s.credential(id, v.PublicKey, expires)
	if !c.ExpiresAt.After(s.now()) {
		fail(w, 409, "credential_expired_operator_recovery_required")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	write(w, 200, c)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/agent/policy":
		const revision = "central-mvp-1"
		w.Header().Set("ETag", `"`+revision+`"`)
		if r.Header.Get("If-None-Match") == `"`+revision+`"` {
			w.WriteHeader(304)
			return
		}
		write(w, 200, contracts.Policy{SchemaVersion: 1, Revision: revision, Arguments: []string{}, MonitorIntervalSeconds: 30, RestartPolicy: "never", StartupEntries: []string{}})
	case "/agent/releases/latest":
		write(w, 200, json.RawMessage("null"))
	default:
		fail(w, 404, "not_found")
	}
}

func (s *Server) report(w http.ResponseWriter, r *http.Request, deviceID, path string) {
	var validate func([]byte) error
	switch r.Method + " " + path {
	case "POST /agent/heartbeat":
		validate = func(b []byte) error { _, e := parse[contracts.Heartbeat](b); return e }
	case "POST /agent/metrics":
		validate = func(b []byte) error { _, e := parse[contracts.Metrics](b); return e }
	case "PUT /agent/inventory":
		validate = func(b []byte) error { _, e := parse[contracts.Inventory](b); return e }
	case "POST /agent/policy/report":
		validate = func(b []byte) error { _, e := parse[contracts.PolicyReport](b); return e }
	default:
		fail(w, 404, "not_found")
		return
	}
	b, err := read(r)
	if err != nil || validate(b) != nil {
		fail(w, 400, "invalid_request")
		return
	}
	requestID := r.Header.Get("Idempotency-Key")
	if !contracts.ValidID(requestID) {
		fail(w, 400, "idempotency_key_required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	defer tx.Rollback()
	var oldHash string
	hash := digest(b)
	err = tx.QueryRowContext(r.Context(), "SELECT body_hash FROM reports WHERE device_id=? AND method=? AND path=? AND request_id=?", deviceID, r.Method, path, requestID).Scan(&oldHash)
	if err == nil {
		if oldHash != hash {
			fail(w, 409, "idempotency_conflict")
		} else {
			write(w, 204, nil)
		}
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "storage_unavailable")
		return
	}
	now := s.now().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(r.Context(), "INSERT INTO reports(device_id,method,path,request_id,body_hash,received_at,body) VALUES(?,?,?,?,?,?,?)", deviceID, r.Method, path, requestID, hash, now, b)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "UPDATE devices SET last_seen_at=? WHERE id=?", now, deviceID)
	}
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	write(w, 204, nil)
}

func pageLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 20, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 100 {
		return 0, errors.New("limit must be 1..100")
	}
	return n, nil
}
