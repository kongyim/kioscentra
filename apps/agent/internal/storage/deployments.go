package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"kiosentra/packages/contracts"
)

type Deployment struct {
	RenewalID      string              `json:"renewalId"`
	Job            contracts.Job       `json:"job"`
	Status         contracts.JobStatus `json:"status"`
	StartedAt      time.Time           `json:"startedAt"`
	Deadline       time.Time           `json:"deadline"`
	LeaseExpiresAt time.Time           `json:"leaseExpiresAt"`
}
type DeploymentEvent struct {
	ID       string              `json:"id"`
	JobID    string              `json:"jobId"`
	Status   contracts.JobStatus `json:"status"`
	Delivery string              `json:"delivery"`
}

func writeDeployment(ctx context.Context, tx *sql.Tx, d Deployment) error {
	d.RenewalID = contracts.ID()
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO deployments(id,state,record) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,record=excluded.record`, d.Job.ID, d.Status.State, b); e != nil {
		return e
	}
	status, e := json.Marshal(d.Status)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO deployment_events(id,job_id,body) VALUES(?,?,?)`, contracts.ID(), d.Job.ID, status)
	return e
}
func (s *Store) Deployment(ctx context.Context, id string) (Deployment, error) {
	var d Deployment
	var b []byte
	e := s.db.QueryRowContext(ctx, `SELECT record FROM deployments WHERE id=?`, id).Scan(&b)
	if e == nil {
		e = json.Unmarshal(b, &d)
	}
	return d, e
}

// ActiveDeployments includes unacknowledged work so admission cannot create a second run.
func (s *Store) ActiveDeployments(ctx context.Context) ([]Deployment, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT record FROM deployments WHERE state NOT IN ('succeeded','failed','rolled_back','cancelled') ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		var b []byte
		var d Deployment
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &d); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) DeploymentAcknowledged(ctx context.Context, id string) (bool, error) {
	var reported bool
	e := s.db.QueryRowContext(ctx, `SELECT reported FROM jobs WHERE id=?`, id).Scan(&reported)
	return reported, e
}

// TransitionDeployment commits state and its stable report ID in one transaction.
func (s *Store) TransitionDeployment(ctx context.Context, id, from string, status contracts.JobStatus) error {
	if e := status.Validate(); e != nil {
		return e
	}
	if !contracts.DeploymentTransition(from, status.State) {
		return errors.New("invalid deployment transition")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var b []byte
	if e = tx.QueryRowContext(ctx, `SELECT record FROM deployments WHERE id=? AND state=?`, id, from).Scan(&b); e != nil {
		return e
	}
	var d Deployment
	if e = json.Unmarshal(b, &d); e != nil {
		return e
	}
	d.Status = status
	if e = writeDeployment(ctx, tx, d); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SaveDeploymentLease(ctx context.Context, id string, expiry time.Time) error {
	d, e := s.Deployment(ctx, id)
	if e != nil {
		return e
	}
	d.LeaseExpiresAt = expiry
	b, e := json.Marshal(d)
	if e != nil {
		return e
	}
	_, e = s.db.ExecContext(ctx, `UPDATE deployments SET record=? WHERE id=?`, b, id)
	return e
}
func (s *Store) DeploymentEvents(ctx context.Context) ([]DeploymentEvent, error) {
	return s.deploymentEvents(ctx, `WHERE delivery='pending' ORDER BY seq LIMIT 100`)
}
func (s *Store) DeploymentHistory(ctx context.Context) ([]DeploymentEvent, error) {
	return s.deploymentEvents(ctx, `ORDER BY seq DESC LIMIT 100`)
}
func (s *Store) deploymentEvents(ctx context.Context, clause string) ([]DeploymentEvent, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,job_id,body,delivery FROM deployment_events `+clause)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DeploymentEvent{}
	for rows.Next() {
		var v DeploymentEvent
		var b []byte
		if e = rows.Scan(&v.ID, &v.JobID, &b, &v.Delivery); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v.Status); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) MarkDeploymentEvent(ctx context.Context, id, delivery string) error {
	if delivery != "delivered" && delivery != "unavailable" {
		return errors.New("invalid delivery result")
	}
	_, e := s.db.ExecContext(ctx, `UPDATE deployment_events SET delivery=? WHERE id=?`, delivery, id)
	return e
}

func (s *Store) DeploymentReportsPending(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM deployment_events WHERE job_id=? AND delivery='pending'`, id).Scan(&count)
	return count != 0, err
}
