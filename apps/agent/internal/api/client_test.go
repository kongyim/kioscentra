package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTLSVerificationAndRedirects(t *testing.T) {
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer tls.Close()
	c := New(tls.URL, time.Second)
	if e := c.Do(context.Background(), "GET", "/x", "", nil, nil); e == nil {
		t.Fatal("untrusted certificate accepted")
	}
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	c = New(redirect.URL, time.Second)
	if e := c.Do(context.Background(), "GET", "/x", "", nil, nil); e == nil || called {
		t.Fatal("redirect followed or accepted")
	}
}
func TestCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := New(s.URL, time.Second).Do(ctx, "GET", "/x", "", nil, nil); e == nil {
		t.Fatal("request ignored cancellation")
	}
}
