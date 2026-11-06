package ipc

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

func TestLocalTransportReconnectAndWrongKey(t *testing.T) {
	dir, e := os.MkdirTemp("", "ki-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	o := Options{StateDir: dir, Development: true, Key: bytes.Repeat([]byte{8}, 32)}
	start := func() (context.CancelFunc, chan error) {
		t.Helper()
		l, e := Listen(o)
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		s := testServer(t)
		go func() { done <- s.Serve(ctx, l, o.Key) }()
		return cancel, done
	}
	cancel, done := start()
	c := Client{Options: o}
	var snapshot map[string]any
	if e = c.Call(context.Background(), Request{Method: "snapshot"}, &snapshot); e != nil || snapshot["version"] != "test" {
		t.Fatal(snapshot, e)
	}
	wrong := o
	wrong.Key = bytes.Repeat([]byte{9}, 32)
	if e = (Client{Options: wrong}).Call(context.Background(), Request{Method: "snapshot"}, nil); e == nil {
		t.Fatal("wrong key accepted")
	}
	cancel()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
	cancel, done = start()
	defer cancel()
	if e = c.Call(context.Background(), Request{Method: "snapshot"}, &snapshot); e != nil {
		t.Fatal("reconnect", e)
	}
	cancel()
	<-done
}
