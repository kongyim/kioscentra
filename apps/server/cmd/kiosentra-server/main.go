package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kiosentra/apps/server/central"
)

func main() {
	if err := run(); err != nil {
		slog.Error("central server stopped", "error", err)
		os.Exit(1)
	}
}
func validateListener(address, cert, key string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if (cert == "") != (key == "") {
		return errors.New("both TLS certificate and key are required")
	}
	if cert == "" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("HTTP requires a literal loopback listener; provide TLS certificate and key for remote connections")
		}
	}
	return nil
}
func run() error {
	address := flag.String("listen", "127.0.0.1:8080", "listen address; remote listeners require TLS")
	database := flag.String("database", "", "absolute SQLite database path (required)")
	cert := flag.String("tls-cert", "", "TLS certificate PEM file")
	key := flag.String("tls-key", "", "TLS private key PEM file")
	adminDirectory := flag.String("admin-directory", "", "optional path to apps/admin/public; serves the device and simulation dashboard at /admin/")
	flag.Parse()
	if err := validateListener(*address, *cert, *key); err != nil {
		return err
	}
	credentialKey, err := base64.StdEncoding.DecodeString(os.Getenv("KIOSENTRA_CENTRAL_CREDENTIAL_KEY"))
	if err != nil {
		return errors.New("credential key must be base64 of 32 random bytes")
	}
	simulationKey, err := base64.StdEncoding.DecodeString(os.Getenv("KIOSENTRA_CENTRAL_SIMULATION_PRIVATE_KEY"))
	if err != nil {
		return errors.New("simulation private key must be base64 Ed25519 private key")
	}
	handler, err := central.Open(central.Options{DatabasePath: *database, EnrollmentToken: os.Getenv("KIOSENTRA_CENTRAL_ENROLLMENT_TOKEN"), AdminToken: os.Getenv("KIOSENTRA_CENTRAL_ADMIN_TOKEN"), CredentialKey: credentialKey, SimulationPrivateKey: simulationKey})
	if err != nil {
		return err
	}
	defer handler.Close()
	httpHandler, err := withAdmin(handler, *adminDirectory)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *address, Handler: httpHandler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		slog.Info("central MVP listening", "address", *address, "tls", *cert != "")
		if *cert != "" {
			done <- server.ListenAndServeTLS(*cert, *key)
		} else {
			done <- server.ListenAndServe()
		}
	}()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = server.Shutdown(shutdown)
		if err != nil {
			_ = server.Close()
		}
		return err
	}
}
