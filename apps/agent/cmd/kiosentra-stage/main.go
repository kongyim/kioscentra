// kiosentra-stage verifies and downloads a signed MSI authorization. It never installs.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"kiosentra/apps/agent/internal/artifact"
	"kiosentra/packages/contracts"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("kiosentra-stage", flag.ContinueOnError)
	manifest := f.String("authorization", "", "signed authorization JSON path")
	device := f.String("device-id", "", "expected enrolled device ID")
	keyID := f.String("key-id", "", "trusted release signing key ID")
	public := f.String("public-key", "", "base64 Ed25519 public key (not a private key)")
	origins := f.String("origins", "", "comma-separated trusted HTTPS origins, without trailing slashes")
	directory := f.String("directory", "", "existing absolute private staging directory")
	max := f.Int64("max-bytes", 256<<20, "maximum artifact size, at most 2 GiB")
	timeout := f.Duration("timeout", 5*time.Minute, "total download timeout, at most 15m")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *manifest == "" {
		return errors.New("authorization file required; positional arguments are not supported")
	}
	key, err := base64.StdEncoding.DecodeString(*public)
	if err != nil {
		return errors.New("invalid public key encoding")
	}
	s, err := artifact.New(artifact.Config{Directory: *directory, DeviceID: *device, Keys: map[string]ed25519.PublicKey{*keyID: key}, Origins: strings.Split(*origins, ","), MaxBytes: *max, Timeout: *timeout})
	if err != nil {
		return err
	}
	defer s.Close()
	input, err := os.Open(*manifest)
	if err != nil {
		return err
	}
	defer input.Close()
	var a contracts.ArtifactAuthorization
	if err = contracts.Decode(io.LimitReader(input, 65537), &a); err != nil {
		return err
	}
	path, err := s.Stage(ctx, a)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		Path      string `json:"path"`
		Installed bool   `json:"installed"`
	}{Path: path})
}
