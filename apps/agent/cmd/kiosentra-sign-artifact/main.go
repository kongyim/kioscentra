// kiosentra-sign-artifact is an offline operator tool, not an Agent service mode.
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
	"strings"
	"time"

	"kiosentra/apps/agent/internal/configuration"
	"kiosentra/packages/contracts"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("kiosentra-sign-artifact", flag.ContinueOnError)
	input := f.String("input", "", "unsigned authorization JSON")
	keyFile := f.String("private-key-file", "", "private file containing base64 Ed25519 private key")
	output := f.String("output", "", "new signed authorization JSON; existing files are never overwritten")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *input == "" || *keyFile == "" || *output == "" || f.NArg() != 0 {
		return errors.New("input, private-key-file and output are required")
	}
	b, err := configuration.ReadPrivateFile(*keyFile)
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return errors.New("invalid Ed25519 private key")
	}
	defer clear(key)
	// Reject an inconsistent seed/public-key pair instead of producing unusable signatures.
	derived := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	defer clear(derived)
	if !ed25519.PrivateKey(key).Equal(derived) {
		return errors.New("inconsistent Ed25519 private key")
	}
	file, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer file.Close()
	var a contracts.ArtifactAuthorization
	if err = contracts.Decode(io.LimitReader(file, 65537), &a); err != nil {
		return err
	}
	if err = contracts.SignArtifact(&a, key); err != nil {
		return err
	}
	if err = contracts.VerifyArtifact(a, a.DeviceID, map[string]ed25519.PublicKey{a.Release.KeyID: derived.Public().(ed25519.PublicKey)}, time.Now()); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	out, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = out.Close()
		if !complete {
			_ = os.Remove(*output)
		}
	}()
	if err = json.NewEncoder(out).Encode(a); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}
