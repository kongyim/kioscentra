package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"kiosentra/apps/agent/internal/ipc"
	"kiosentra/apps/agent/internal/updater"
	"kiosentra/packages/contracts"
)

func (a *Agent) startConsole(parent context.Context, logs *ipc.LogBuffer) (func(), error) {
	options, e := ipc.LoadOptions(a.Config.StateDir, a.Config.Development)
	if e != nil {
		return nil, e
	}
	if len(options.Key) == 0 {
		a.Log.Info("local console IPC disabled: provision KIOSENTRA_IPC_KEY")
		return func() {}, nil
	}
	server, e := ipc.NewServer(options.MaintenanceHash)
	if e != nil {
		return nil, e
	}
	server.Technicians = options.Technicians
	listener, e := ipc.Listen(options)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(parent)
	actions := make(chan string, 8)
	a.Actions = actions
	server.Snapshot = func(ctx context.Context) (any, error) {
		data, e := a.Store.ConsoleData(ctx)
		if e != nil {
			return nil, e
		}
		if _, err := os.Stat(filepath.Join(a.Config.StateDir, "update-simulation", "journal.db")); err == nil {
			journal, err := updater.Open(a.Config.StateDir)
			if err != nil {
				return nil, err
			}
			events, err := journal.Events(ctx, false)
			if err != nil {
				journal.Close()
				return nil, err
			}
			target, err := journal.Target(ctx)
			journal.Close()
			if err != nil {
				return nil, err
			}
			data["updates"] = events
			data["updateTarget"] = target
		}
		var credential contracts.Credential
		if err := a.Store.Get(ctx, "credential", &credential, true); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return map[string]any{"deviceId": credential.DeviceID, "credentialExpiresAt": credential.ExpiresAt, "namedTechnicians": len(options.Technicians) != 0, "version": Version, "commit": Commit, "hostname": a.hostname, "data": data, "logs": logs.Snapshot(), "maintenanceEnabled": server.MaintenanceEnabled()}, nil
	}
	server.Action = func(action string) error {
		select {
		case actions <- action:
			return nil
		default:
			return errors.New("service action queue busy")
		}
	}
	server.Audit = func(message string) { a.Log.Info(message) }
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if e := server.Serve(ctx, listener, options.Key); e != nil {
			a.Log.Error("local console IPC stopped", "error", e)
		}
	}()
	return func() { cancel(); <-stopped }, nil
}
