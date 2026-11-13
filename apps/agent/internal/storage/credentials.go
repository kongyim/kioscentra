package storage

import (
	"context"
	"encoding/json"
	"kiosentra/packages/contracts"
)

// CommitCredentialRotation atomically replaces the protected credential and clears
// its pending request. A crash exposes either the old credential plus intent or the new one.
func (s *Store) CommitCredentialRotation(ctx context.Context, c contracts.Credential) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	b, err = s.p.Seal(b)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO kv(key,value) VALUES('credential',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, b); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM kv WHERE key='credential-rotation'`); err != nil {
		return err
	}
	return tx.Commit()
}
