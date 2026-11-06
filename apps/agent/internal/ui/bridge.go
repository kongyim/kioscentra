package ui

import (
	"context"
	"errors"
	"sync"

	"kiosentra/apps/agent/internal/ipc"
)

// Bridge holds no service database or credentials. Every read/action uses IPC.
type Bridge struct {
	client  ipc.Client
	mu      sync.Mutex
	warning string
}

func (b *Bridge) Snapshot() (map[string]any, error) {
	var result map[string]any
	e := b.client.Call(context.Background(), ipc.Request{Method: "snapshot"}, &result)
	if e != nil {
		return nil, e
	}
	if result == nil {
		return nil, errors.New("service returned no snapshot")
	}
	return result, nil
}
func (b *Bridge) Authorize(secret, scope string) (ipc.Grant, error) {
	var g ipc.Grant
	e := b.client.Call(context.Background(), ipc.Request{Method: "authorize", Secret: secret, Scope: scope}, &g)
	return g, e
}
func (b *Bridge) Action(token, scope string) error {
	return b.client.Call(context.Background(), ipc.Request{Method: "action", Token: token, Scope: scope}, nil)
}

// UIWarning remains available when service IPC is disconnected.
func (b *Bridge) UIWarning() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.warning
}
