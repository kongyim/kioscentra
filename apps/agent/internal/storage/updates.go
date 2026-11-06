package storage

import (
	"context"
	"encoding/json"
	"kiosentra/packages/contracts"
)

// UpdateJobs returns durable accepted handoffs, including those awaiting ACK.
func (s *Store) UpdateJobs(ctx context.Context) ([]contracts.Job, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT body FROM jobs JOIN update_handoffs ON jobs.id=update_handoffs.id WHERE complete=0 ORDER BY created_at`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []contracts.Job{}
	for rows.Next() {
		var b []byte
		var j contracts.Job
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &j); e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) CompleteUpdate(ctx context.Context, id string) error {
	_, e := s.db.ExecContext(ctx, `UPDATE update_handoffs SET complete=1 WHERE id=?`, id)
	return e
}
