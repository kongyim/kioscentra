package installer

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"kiosentra/packages/contracts"
)

type BrokerRequest struct {
	Method        string                           `json:"method"`
	JobID         string                           `json:"jobId"`
	Authorization *contracts.ArtifactAuthorization `json:"authorization,omitempty"`
	Lease         *contracts.InstallationLease     `json:"lease,omitempty"`
}
type BrokerStatus struct {
	Result                Result `json:"result"`
	CancellationRequested bool   `json:"cancellationRequested"`
}
type Broker struct {
	engine    Engine
	leaseKeys map[string]ed25519.PublicKey
	mu        sync.Mutex
}

func NewBroker(e Engine, leaseKeys map[string]ed25519.PublicKey) (*Broker, error) {
	if e.Journal == nil || e.Stager == nil || e.Adapter == nil || !contracts.ValidID(e.DeviceID) || len(e.Keys) == 0 || len(leaseKeys) == 0 || e.Timeout <= 0 || e.Timeout > time.Hour {
		return nil, errors.New("invalid broker configuration")
	}
	clone := func(keys map[string]ed25519.PublicKey) (map[string]ed25519.PublicKey, error) {
		out := map[string]ed25519.PublicKey{}
		for id, key := range keys {
			if id == "" || id == contracts.SimulationKeyID || len(key) != ed25519.PublicKeySize {
				return nil, errors.New("invalid broker trust key")
			}
			out[id] = append(ed25519.PublicKey(nil), key...)
		}
		return out, nil
	}
	var err error
	e.Keys, err = clone(e.Keys)
	if err != nil {
		return nil, err
	}
	keys, err := clone(leaseKeys)
	if err != nil {
		return nil, err
	}
	b := &Broker{engine: e, leaseKeys: keys}
	// Recover uncertain executions before accepting any work, even if their
	// authorization has since expired or its signing key has been removed.
	rows, err := e.Journal.db.Query("SELECT result,fingerprint FROM executions WHERE state='installing'")
	if err != nil {
		return nil, err
	}
	type pending struct {
		r    Result
		hash string
	}
	var recovered []pending
	for rows.Next() {
		var raw []byte
		var p pending
		if err = rows.Scan(&raw, &p.hash); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &p.r); err != nil {
			break
		}
		recovered = append(recovered, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, p := range recovered {
		p.r.State = "needs_operator"
		p.r.Message = "Broker restarted after installer intent; automatic replay prohibited"
		p.r.UpdatedAt = e.now()
		if err = e.Journal.save(p.r, p.hash); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (b *Broker) Handle(ctx context.Context, r BrokerRequest) (BrokerStatus, error) {
	if !contracts.ValidID(r.JobID) {
		return BrokerStatus{}, errors.New("valid installation job ID required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch r.Method {
	case "submit":
		if r.Authorization == nil || r.Lease == nil || r.JobID != r.Authorization.JobID {
			return BrokerStatus{}, errors.New("authorization and lease required")
		}
		a := *r.Authorization
		if err := contracts.VerifyArtifact(a, b.engine.DeviceID, b.engine.Keys, b.engine.now()); err != nil {
			return BrokerStatus{}, err
		}
		if a.MSI == nil {
			return BrokerStatus{}, errors.New("signed MSI identity required")
		}
		if err := contracts.VerifyInstallationLease(*r.Lease, a, b.leaseKeys, b.engine.now()); err != nil {
			return BrokerStatus{}, err
		}
		var hash string
		err := b.engine.Journal.db.QueryRowContext(ctx, "SELECT fingerprint FROM broker_requests WHERE id=?", r.JobID).Scan(&hash)
		if err == nil {
			if hash != contracts.ArtifactDigest(a) {
				return BrokerStatus{}, errors.New("job authorization conflict")
			}
			return b.status(r.JobID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return BrokerStatus{}, err
		}
		var active, count int
		if err = b.engine.Journal.db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(CASE WHEN done=0 THEN 1 ELSE 0 END),0) FROM broker_requests").Scan(&count, &active); err != nil {
			return BrokerStatus{}, err
		}
		blocked, err := b.engine.Journal.blocked()
		if err != nil {
			return BrokerStatus{}, err
		}
		if active != 0 || blocked || count >= 10000 {
			return BrokerStatus{}, errors.New("broker busy, recovery required or capacity reached")
		}
		// Do not reuse a job already present from the standalone executor.
		if _, exists, err := b.engine.Journal.result(r.JobID, contracts.ArtifactDigest(a)); err != nil {
			return BrokerStatus{}, err
		} else if exists {
			return BrokerStatus{}, errors.New("job already belongs to standalone executor")
		}
		ab, _ := json.Marshal(a)
		lb, _ := json.Marshal(r.Lease)
		_, err = b.engine.Journal.db.ExecContext(ctx, "INSERT INTO broker_requests(id,fingerprint,authorization,lease) VALUES(?,?,?,?)", r.JobID, contracts.ArtifactDigest(a), ab, lb)
		if err != nil {
			return BrokerStatus{}, err
		}
		return b.status(r.JobID)
	case "renew":
		if r.Authorization != nil || r.Lease == nil {
			return BrokerStatus{}, errors.New("renew requires only lease")
		}
		a, old, cancelled, done, err := b.record(r.JobID)
		if err != nil {
			return BrokerStatus{}, err
		}
		if cancelled || done {
			return BrokerStatus{}, errors.New("job no longer renewable")
		}
		if !old.ExpiresAt.After(b.engine.now()) {
			return BrokerStatus{}, errors.New("expired execution lease cannot be revived")
		}
		if err = contracts.VerifyInstallationLease(*r.Lease, a, b.leaseKeys, b.engine.now()); err != nil {
			return BrokerStatus{}, err
		}
		if r.Lease.ExpiresAt.After(old.ExpiresAt) {
			raw, _ := json.Marshal(r.Lease)
			if _, err = b.engine.Journal.db.ExecContext(ctx, "UPDATE broker_requests SET lease=? WHERE id=?", raw, r.JobID); err != nil {
				return BrokerStatus{}, err
			}
		}
		return b.status(r.JobID)
	case "cancel":
		if r.Authorization != nil || r.Lease != nil {
			return BrokerStatus{}, errors.New("cancel accepts only job ID")
		}
		if _, _, _, _, err := b.record(r.JobID); err != nil {
			return BrokerStatus{}, err
		}
		_, err := b.engine.Journal.db.ExecContext(ctx, "UPDATE broker_requests SET cancelled=1 WHERE id=? AND done=0", r.JobID)
		if err != nil {
			return BrokerStatus{}, err
		}
		return b.status(r.JobID)
	case "status":
		if r.Authorization != nil || r.Lease != nil {
			return BrokerStatus{}, errors.New("status accepts only job ID")
		}
		return b.status(r.JobID)
	default:
		return BrokerStatus{}, errors.New("unsupported broker method")
	}
}

func (b *Broker) record(id string) (contracts.ArtifactAuthorization, contracts.InstallationLease, bool, bool, error) {
	var a contracts.ArtifactAuthorization
	var l contracts.InstallationLease
	var ab, lb []byte
	var cancelled, done bool
	err := b.engine.Journal.db.QueryRow("SELECT authorization,lease,cancelled,done FROM broker_requests WHERE id=?", id).Scan(&ab, &lb, &cancelled, &done)
	if err == nil {
		err = json.Unmarshal(ab, &a)
	}
	if err == nil {
		err = json.Unmarshal(lb, &l)
	}
	return a, l, cancelled, done, err
}
func (b *Broker) status(id string) (BrokerStatus, error) {
	a, _, cancelled, _, err := b.record(id)
	if err != nil {
		return BrokerStatus{}, err
	}
	r, err := b.engine.Journal.Status(id)
	if errors.Is(err, sql.ErrNoRows) {
		r = Result{JobID: id, State: "queued", Message: "Queued for signed MSI execution", ExitCode: -1, UpdatedAt: a.IssuedAt}
		err = nil
	}
	return BrokerStatus{Result: r, CancellationRequested: cancelled}, err
}
func (b *Broker) permitted(id string) error {
	a, l, cancelled, done, err := b.record(id)
	if err != nil {
		return err
	}
	if cancelled || done {
		return errors.New("broker execution cancelled")
	}
	if err = contracts.VerifyArtifact(a, b.engine.DeviceID, b.engine.Keys, b.engine.now()); err != nil {
		return err
	}
	return contracts.VerifyInstallationLease(l, a, b.leaseKeys, b.engine.now())
}

// Run executes one persisted request at a time. Client disconnection does not
// cancel accepted work; a signed lease, durable cancel, or service stop does.
func (b *Broker) Run(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		var id string
		err := b.engine.Journal.db.QueryRowContext(ctx, "SELECT id FROM broker_requests WHERE done=0 ORDER BY rowid LIMIT 1").Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err = b.execute(ctx, id); err != nil {
			return err
		}
	}
}
func (b *Broker) execute(ctx context.Context, id string) error {
	a, _, _, _, err := b.record(id)
	if err != nil {
		return err
	}
	finish := func(r Result) error {
		if err := b.engine.Journal.save(r, contracts.ArtifactDigest(a)); err != nil {
			return err
		}
		_, err := b.engine.Journal.db.Exec("UPDATE broker_requests SET done=1 WHERE id=?", id)
		return err
	}
	old, err := b.engine.Journal.Status(id)
	if err == nil && contracts.InstallationTerminal(old.State) {
		return finish(old)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = b.permitted(id); err != nil {
		return finish(Result{JobID: id, State: "cancelled", Message: "Cancelled or execution lease expired before launch", ExitCode: -1, UpdatedAt: b.engine.now()})
	}
	child, cancel := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-child.Done():
				return
			case <-t.C:
				if b.permitted(id) != nil {
					cancel()
					return
				}
			}
		}
	}()
	e := b.engine
	e.BeforeInstall = func(context.Context) error { return b.permitted(id) }
	r, runErr := e.Execute(child, a)
	cancel()
	<-watchDone
	if runErr != nil {
		old, statusErr := b.engine.Journal.Status(id)
		if statusErr != nil && !errors.Is(statusErr, sql.ErrNoRows) {
			return errors.Join(runErr, statusErr)
		}
		if old.State == "installing" {
			r = old
			r.State = "needs_operator"
			r.Message = "Executor failed after installer intent; operator recovery required"
		} else if contracts.InstallationTerminal(old.State) {
			r = old
		} else {
			r = Result{JobID: id, State: "failed", Message: "Broker could not execute signed installation", ExitCode: -1}
		}
		r.UpdatedAt = b.engine.now()
	}
	return finish(r)
}
