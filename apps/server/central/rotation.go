package central

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"kiosentra/packages/contracts"
)

// Rotation is authenticated inside the same transaction that changes the credential.
// Old credentials authorize only the exact committed rotation replay, for five minutes.
func (s *Server) rotate(w http.ResponseWriter, r *http.Request, tokenHash string) {
	b, err := read(r)
	if err != nil {
		fail(w, 400, "invalid_request")
		return
	}
	v, err := parse[contracts.Rotation](b)
	if err != nil {
		fail(w, 400, "invalid_rotation")
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
	var id, publicKey, expiry, currentHash string
	err = tx.QueryRowContext(r.Context(), `SELECT id,public_key,expires_at,token_hash FROM devices WHERE token_hash=?`, tokenHash).Scan(&id, &publicKey, &expiry, &currentHash)
	oldToken := false
	if errors.Is(err, sql.ErrNoRows) {
		var until string
		err = tx.QueryRowContext(r.Context(), `SELECT d.id,d.public_key,d.expires_at,d.token_hash,c.replay_until FROM credential_rotations c JOIN devices d ON d.id=c.device_id WHERE c.old_token_hash=? AND c.request_id=?`, tokenHash, requestID).Scan(&id, &publicKey, &expiry, &currentHash, &until)
		deadline, e := time.Parse(time.RFC3339Nano, until)
		if err == nil && (e != nil || !deadline.After(s.now())) {
			fail(w, 401, "invalid_device_credential")
			return
		}
		oldToken = true
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "invalid_device_credential")
		return
	}
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	expires, e := time.Parse(time.RFC3339Nano, expiry)
	if e != nil || !expires.After(s.now()) {
		fail(w, 401, "invalid_device_credential")
		return
	}
	var bodyHash, resultExpiry string
	err = tx.QueryRowContext(r.Context(), `SELECT body_hash,expires_at FROM credential_rotations WHERE device_id=? AND request_id=?`, id, requestID).Scan(&bodyHash, &resultExpiry)
	if err == nil {
		if bodyHash != digest(b) {
			fail(w, 409, "idempotency_conflict")
			return
		}
		c := s.credential(id, publicKey, resultExpiry)
		if digest([]byte(c.AccessToken)) != currentHash {
			fail(w, 409, "rotation_superseded")
			return
		}
		write(w, 200, c)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "storage_unavailable")
		return
	}
	if oldToken {
		fail(w, 401, "invalid_device_credential")
		return
	}
	if v.PublicKey != publicKey {
		fail(w, 400, "invalid_rotation")
		return
	}
	now := s.now()
	nextExpiry := now.Add(24 * time.Hour)
	// Keep deterministic tokens distinct even with a stationary/backward clock.
	if !nextExpiry.After(expires) {
		nextExpiry = expires.Add(time.Nanosecond)
	}
	c := s.credential(id, publicKey, nextExpiry.Format(time.RFC3339Nano))
	_, err = tx.ExecContext(r.Context(), `INSERT INTO credential_rotations(device_id,request_id,old_token_hash,body_hash,expires_at,replay_until) VALUES(?,?,?,?,?,?)`, id, requestID, tokenHash, digest(b), nextExpiry.Format(time.RFC3339Nano), now.Add(5*time.Minute).Format(time.RFC3339Nano))
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE devices SET token_hash=?,expires_at=? WHERE id=?`, digest([]byte(c.AccessToken)), nextExpiry.Format(time.RFC3339Nano), id)
	}
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	write(w, 200, c)
}
