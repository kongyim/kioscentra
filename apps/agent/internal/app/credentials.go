package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"kiosentra/packages/contracts"
)

const credentialRenewalLead = time.Hour

type pendingRotation struct {
	RequestID string `json:"requestId"`
	DeviceID  string `json:"deviceId"`
	PublicKey string `json:"publicKey"`
}

func (a *Agent) renewCredential(ctx context.Context) error {
	var pending pendingRotation
	err := a.Store.Get(ctx, "credential-rotation", &pending, true)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load credential rotation: %w", err)
	}
	a.rotationPending = err == nil
	if errors.Is(err, sql.ErrNoRows) {
		if !a.credential.ExpiresAt.After(time.Now()) {
			return errors.New("device credential expired; operator recovery required")
		}
		if time.Until(a.credential.ExpiresAt) > max(credentialRenewalLead, 2*a.Config.Interval) {
			return nil
		}
		pending = pendingRotation{RequestID: contracts.ID(), DeviceID: a.credential.DeviceID, PublicKey: a.identity.PublicKey}
		// Durable intent must precede any request: a lost response retries this same ID.
		if err = a.Store.Put(ctx, "credential-rotation", pending, true); err != nil {
			return err
		}
	}
	a.rotationPending = true
	if !contracts.ValidID(pending.RequestID) || pending.DeviceID != a.credential.DeviceID || pending.PublicKey != a.identity.PublicKey {
		return errors.New("invalid pending credential rotation")
	}
	// A committed rotation may be replayable even after the local old token expires.
	next, err := a.Client.Rotate(ctx, pending.RequestID, contracts.Rotation{PublicKey: pending.PublicKey})
	if err != nil {
		return fmt.Errorf("credential rotation: %w", err)
	}
	if next.DeviceID != a.credential.DeviceID || next.AccessToken == "" || next.AccessToken == a.credential.AccessToken || !contracts.UTC(next.ExpiresAt) || !next.ExpiresAt.After(time.Now()) || !next.ExpiresAt.After(a.credential.ExpiresAt) {
		return errors.New("invalid rotated credential response")
	}
	if err = a.Store.CommitCredentialRotation(ctx, next); err != nil {
		return fmt.Errorf("save rotated credential: %w", err)
	}
	a.rotationPending = false
	a.credential = next
	a.Client.SetCredential(next)
	a.Log.Info("device credential renewed", "device_id", next.DeviceID, "expires_at", next.ExpiresAt)
	return nil
}
