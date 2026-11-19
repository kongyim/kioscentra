// kiosentra-install-msi is an explicitly invoked, elevated Windows lab executor.
// It is not an Agent service mode or a remotely exposed command endpoint.
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
	"kiosentra/apps/agent/internal/brokerapp"
	"kiosentra/apps/agent/internal/installer"
	"kiosentra/apps/agent/internal/service"
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
	f := flag.NewFlagSet("kiosentra-install-msi", flag.ContinueOnError)
	mode := f.String("mode", "oneshot", "oneshot, broker-console, broker-service or validate-broker-config")
	configFile := f.String("config", "", "protected broker configuration file")
	execute := f.Bool("execute", false, "explicitly authorize real installation on this Windows test host")
	status := f.Bool("status", false, "read a recorded result without executing or replaying")
	jobID := f.String("job-id", "", "job ID for --status")
	input := f.String("authorization", "", "signed authorization JSON file, including MSI identity")
	device := f.String("device-id", "", "expected enrolled device ID")
	keyID := f.String("key-id", "", "trusted release key ID")
	public := f.String("public-key", "", "base64 Ed25519 public key")
	origins := f.String("origins", "", "comma-separated trusted HTTPS origins")
	staging := f.String("staging-directory", "", "existing protected staging directory")
	journal := f.String("journal-directory", "", "existing protected machine-wide executor journal directory; reuse for every job")
	timeout := f.Duration("timeout", 15*time.Minute, "overall operation timeout, at most one hour")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *mode != "oneshot" {
		if *mode != "broker-console" && *mode != "broker-service" && *mode != "validate-broker-config" {
			return errors.New("unknown executor mode")
		}
		invalid := f.NArg() != 0
		f.Visit(func(v *flag.Flag) {
			if v.Name != "mode" && v.Name != "config" {
				invalid = true
			}
		})
		if invalid {
			return errors.New("broker mode accepts only mode and config")
		}
		c, err := installer.LoadBrokerConfig(*configFile)
		if err != nil {
			return err
		}
		if *mode == "validate-broker-config" {
			return nil
		}
		if *mode == "broker-service" {
			return service.RunNamed("KiosentraInstaller", func(ctx context.Context) error { return brokerapp.RunBroker(ctx, c) })
		}
		return brokerapp.RunBroker(ctx, c)
	}
	if *configFile != "" {
		return errors.New("config requires broker mode")
	}
	if *status {
		if *execute || *input != "" || f.NArg() != 0 {
			return errors.New("status must be used without execution or authorization input")
		}
		j, err := installer.OpenJournal(*journal)
		if err != nil {
			return err
		}
		defer j.Close()
		r, err := j.Status(*jobID)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	}
	if !*execute || *input == "" || *timeout <= 0 || *timeout > time.Hour || f.NArg() != 0 {
		return errors.New("explicit --execute, authorization and a valid timeout are required")
	}
	adapter, err := installer.NativeAdapter()
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(*public)
	if err != nil {
		return errors.New("invalid public key")
	}
	keys := map[string]ed25519.PublicKey{*keyID: key}
	s, err := artifact.New(artifact.Config{Directory: *staging, DeviceID: *device, Keys: keys, Origins: strings.Split(*origins, ","), MaxBytes: 256 << 20, Timeout: 5 * time.Minute})
	if err != nil {
		return err
	}
	defer s.Close()
	file, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer file.Close()
	var a contracts.ArtifactAuthorization
	if err = contracts.Decode(io.LimitReader(file, 65537), &a); err != nil {
		return err
	}
	j, err := installer.OpenJournal(*journal)
	if err != nil {
		return err
	}
	defer j.Close()
	e := installer.Engine{Journal: j, Stager: s, Adapter: adapter, DeviceID: *device, Keys: keys, Timeout: *timeout}
	r, err := e.Execute(ctx, a)
	if err != nil {
		return err
	}
	if err = json.NewEncoder(out).Encode(r); err != nil {
		return err
	}
	if r.State != "succeeded" {
		return errors.New("MSI execution did not complete successfully; inspect the recorded result")
	}
	return nil
}
