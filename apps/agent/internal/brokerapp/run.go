package brokerapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"kiosentra/apps/agent/internal/artifact"
	"kiosentra/apps/agent/internal/installer"
	"kiosentra/apps/agent/internal/ipc"
	"kiosentra/packages/contracts"
	"time"
)

func RunBroker(ctx context.Context, c installer.BrokerConfig) error {
	adapter, err := installer.NativeAdapter()
	if err != nil {
		return err
	}
	keys, err := installer.DecodeBrokerKeys(c.ReleaseKeys)
	if err != nil {
		return err
	}
	leases, err := installer.DecodeBrokerKeys(c.LeaseKeys)
	if err != nil {
		return err
	}
	s, err := artifact.New(artifact.Config{Directory: c.StagingDirectory, DeviceID: c.DeviceID, Keys: keys, Origins: c.Origins, MaxBytes: 256 << 20, Timeout: 5 * time.Minute})
	if err != nil {
		return err
	}
	defer s.Close()
	j, err := installer.OpenJournal(c.StateDirectory)
	if err != nil {
		return err
	}
	defer j.Close()
	b, err := installer.NewBroker(installer.Engine{Journal: j, Stager: s, Adapter: adapter, DeviceID: c.DeviceID, Keys: keys, Timeout: time.Duration(c.TimeoutSeconds) * time.Second}, leases)
	if err != nil {
		return err
	}
	key, _ := base64.StdEncoding.DecodeString(c.IPCKey)
	l, err := ipc.Listen(ipc.Options{StateDir: c.StateDirectory, ReaderSID: c.AgentSID})
	if err != nil {
		return err
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 2)
	go func() { done <- b.Run(child) }()
	go func() {
		done <- ipc.ServeRPC(child, l, key, installer.BrokerDomain, func(ctx context.Context, raw json.RawMessage) (any, error) {
			var r installer.BrokerRequest
			if err := contracts.Decode(bytes.NewReader(raw), &r); err != nil {
				return nil, err
			}
			return b.Handle(ctx, r)
		})
	}()
	err = <-done
	cancel()
	return errors.Join(err, <-done)
}
