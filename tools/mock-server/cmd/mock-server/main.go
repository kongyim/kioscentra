package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kiosentra/tools/mock-server/mock"
)

func main() {
	if e := run(); e != nil {
		slog.Error("mock server stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	listen := flag.String("listen", "127.0.0.1:8080", "literal loopback address only")
	flag.Parse()
	host, _, e := net.SplitHostPort(*listen)
	if e != nil {
		return e
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("development mock must bind a literal loopback address")
	}
	handler, e := mock.New(mock.Options{EnrollmentToken: os.Getenv("KIOSENTRA_MOCK_ENROLLMENT_TOKEN"), AdminToken: os.Getenv("KIOSENTRA_MOCK_ADMIN_TOKEN")})
	if e != nil {
		return e
	}
	s := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { slog.Info("development-only mock listening", "address", *listen); done <- s.ListenAndServe() }()
	select {
	case e := <-done:
		return e
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.Shutdown(shutdown)
	}
}
