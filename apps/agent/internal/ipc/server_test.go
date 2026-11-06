package ipc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	h := sha256.Sum256([]byte("technician-test-secret"))
	s, e := NewServer(hex.EncodeToString(h[:]))
	if e != nil {
		t.Fatal(e)
	}
	s.Snapshot = func(context.Context) (any, error) { return map[string]any{"version": "test"}, nil }
	s.Action = func(string) error { return nil }
	return s
}
func TestAuthorizationScopeExpiryConsumptionAndRestart(t *testing.T) {
	s := testServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	ctx := context.Background()
	if _, e := s.dispatch(ctx, Request{Method: "action", Scope: "stop_service"}); e == nil {
		t.Fatal("unauthenticated action")
	}
	if _, e := s.dispatch(ctx, Request{Method: "authorize", Secret: "wrong", Scope: "stop_service"}); e == nil {
		t.Fatal("wrong secret")
	}
	if _, e := s.dispatch(ctx, Request{Method: "authorize", Secret: "technician-test-secret", Scope: "stop_service"}); e == nil {
		t.Fatal("missing rate limit")
	}
	now = now.Add(6 * time.Second)
	v, e := s.dispatch(ctx, Request{Method: "authorize", Secret: "technician-test-secret", Scope: "refresh_inventory"})
	if e != nil {
		t.Fatal(e)
	}
	g := v.(Grant)
	if _, e = s.dispatch(ctx, Request{Method: "action", Token: g.Token, Scope: "stop_service"}); e == nil {
		t.Fatal("cross-scope action")
	}
	if _, e = s.dispatch(ctx, Request{Method: "action", Token: g.Token, Scope: g.Scope}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.dispatch(ctx, Request{Method: "action", Token: g.Token, Scope: g.Scope}); e == nil {
		t.Fatal("token reused")
	}
	now = now.Add(6 * time.Second)
	v, e = s.dispatch(ctx, Request{Method: "authorize", Secret: "technician-test-secret", Scope: "stop_service"})
	if e != nil {
		t.Fatal(e)
	}
	g = v.(Grant)
	now = now.Add(3 * time.Minute)
	if _, e = s.dispatch(ctx, Request{Method: "action", Token: g.Token, Scope: g.Scope}); e == nil {
		t.Fatal("expired token")
	}
	if _, e = testServer(t).dispatch(ctx, Request{Method: "action", Token: g.Token, Scope: g.Scope}); e == nil {
		t.Fatal("token survived service restart")
	}
	if _, e = s.dispatch(ctx, Request{Method: "authorize", Secret: "technician-test-secret", Scope: "execute"}); e == nil {
		t.Fatal("arbitrary execution")
	}
}
func TestAuthenticatedFramesTamperDirectionAndSize(t *testing.T) {
	a, _ := codec(bytes.Repeat([]byte{1}, 32))
	var buf bytes.Buffer
	_, e := send(&buf, a, Request{Method: "snapshot"}, "request")
	if e != nil {
		t.Fatal(e)
	}
	packet := append([]byte{}, buf.Bytes()...)
	var r Request
	if _, e = receive(bytes.NewReader(packet), a, &r, "response", maxRequest); e == nil {
		t.Fatal("reflection accepted")
	}
	packet[len(packet)-1] ^= 1
	if _, e = receive(bytes.NewReader(packet), a, &r, "request", maxRequest); e == nil {
		t.Fatal("tamper accepted")
	}
	oversized := make([]byte, 4)
	binary.BigEndian.PutUint32(oversized, 1<<30)
	if _, e = receive(bytes.NewReader(oversized), a, &r, "request", maxRequest); e == nil {
		t.Fatal("oversized frame")
	}
	s := testServer(t)
	now := time.Now()
	if !s.acceptNonce("one", now) || s.acceptNonce("one", now) || s.acceptNonce("old", now.Add(-time.Minute)) {
		t.Fatal("replay/expiry")
	}
}
func TestConnectionReplyBeforeStopAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := testServer(t)
	s.Action = func(string) error { cancel(); return nil }
	s.grants["grant"] = Grant{Token: "grant", Scope: "stop_service", ExpiresAt: time.Now().Add(time.Minute)}
	server, client := net.Pipe()
	defer client.Close()
	a, _ := codec(bytes.Repeat([]byte{2}, 32))
	done := make(chan struct{})
	go func() { defer close(done); serveConnection(ctx, server, a, s) }()
	nonce, e := send(client, a, Request{Method: "action", Scope: "stop_service", Token: "grant", IssuedAt: time.Now()}, "kiosentra-request-v1")
	if e != nil {
		t.Fatal(e)
	}
	var response Response
	if _, e = receive(client, a, &response, "kiosentra-response-v1:"+base64.RawStdEncoding.EncodeToString(nonce), MaxResponse); e != nil || response.Error != "" {
		t.Fatal(e, response)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
	server, client = net.Pipe()
	defer client.Close()
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { defer close(done); serveConnection(ctx, server, a, s) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle connection ignored cancellation")
	}
	_, _ = io.Copy(io.Discard, client)
}
