package installer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"kiosentra/packages/contracts"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

type Journal struct {
	db   *sql.DB
	lock *flock.Flock
	mu   sync.Mutex
}

// Status reads a saved result without authorizing or replaying any installation.
func (j *Journal) Status(id string) (Result, error) {
	var r Result
	if !contracts.ValidID(id) {
		return r, errors.New("valid job ID required")
	}
	var b []byte
	if err := j.db.QueryRow("SELECT result FROM executions WHERE id=?", id).Scan(&b); err != nil {
		return r, err
	}
	err := json.Unmarshal(b, &r)
	return r, err
}

// OpenJournal holds the executor lock for its entire lifetime, including install.
// Use one protected directory per machine, not a separate directory per job.
func OpenJournal(dir string) (*Journal, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("installer journal directory must be absolute")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("installer journal requires a private directory")
	}
	for _, name := range []string{"installer.lock", "installer.db", "installer.db-wal", "installer.db-shm"} {
		info, err = os.Lstat(filepath.Join(dir, name))
		if err == nil && !info.Mode().IsRegular() {
			return nil, errors.New("installer state must be regular files")
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	lock := flock.New(filepath.Join(dir, "installer.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errors.New("another installer owns the journal")
	}
	j := &Journal{lock: lock}
	ok := false
	defer func() {
		if !ok {
			_ = j.Close()
		}
	}()
	path := filepath.Join(dir, "installer.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	u.RawQuery = url.Values{"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"}}.Encode()
	j.db, err = sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	j.db.SetMaxOpenConns(1)
	var version int
	if err = j.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version != 0 && version != 1 {
		return nil, errors.New("unsupported installer journal version")
	}
	_, err = j.db.Exec(`CREATE TABLE IF NOT EXISTS executions (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, state TEXT NOT NULL, result BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS broker_requests (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, authorization BLOB NOT NULL, lease BLOB NOT NULL, cancelled INTEGER NOT NULL DEFAULT 0, done INTEGER NOT NULL DEFAULT 0);
PRAGMA user_version=1;`)
	if err != nil {
		return nil, err
	}
	ok = true
	return j, nil
}

func (j *Journal) Close() error {
	var err error
	if j.db != nil {
		err = j.db.Close()
	}
	if j.lock != nil {
		err = errors.Join(err, j.lock.Unlock())
	}
	return err
}

func (j *Journal) result(id, fingerprint string) (Result, bool, error) {
	var b []byte
	var stored string
	var r Result
	err := j.db.QueryRow("SELECT fingerprint,result FROM executions WHERE id=?", id).Scan(&stored, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	if stored != fingerprint {
		return r, true, errors.New("installer job ID reused with different authorization")
	}
	err = json.Unmarshal(b, &r)
	return r, true, err
}

func (j *Journal) save(r Result, fingerprint string) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = j.db.Exec("INSERT INTO executions(id,fingerprint,state,result) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,result=excluded.result", r.JobID, fingerprint, r.State, b)
	return err
}

func (j *Journal) blocked() (bool, error) {
	var count int
	err := j.db.QueryRow("SELECT COUNT(*) FROM executions WHERE state IN ('installing','needs_operator','reboot_required')").Scan(&count)
	return count != 0, err
}
