package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"kiosentra/apps/agent/internal/app"
	"kiosentra/apps/agent/internal/configuration"
	"kiosentra/apps/agent/internal/service"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "kiosentra-agent:", e)
		os.Exit(1)
	}
}
func run() error {
	mode := flag.String("mode", "console", "console, service, or ui (deferred)")
	version := flag.Bool("version", false, "print build information")
	flag.Parse()
	if *version {
		fmt.Printf("Kiosentra %s (%s), API v1\n", app.Version, app.Commit)
		return nil
	}
	if *mode == "ui" {
		return errors.New("UI mode is scheduled for Phase 4")
	}
	if *mode != "console" && *mode != "service" {
		return errors.New("invalid mode")
	}
	var output io.Writer = os.Stderr
	// Service processes have no terminal. Preserve the current and previous bounded log.
	if *mode == "service" {
		c, e := configuration.Load()
		if e != nil {
			return e
		}
		if e = os.MkdirAll(c.StateDir, 0700); e != nil {
			return e
		}
		f, e := openServiceLog(filepath.Join(c.StateDir, "agent.log"))
		if e != nil {
			return e
		}
		defer f.Close()
		output = f
	}
	log := slog.New(slog.NewJSONHandler(output, nil))
	work := func(ctx context.Context) error {
		err := app.Run(ctx, log)
		if err != nil {
			log.Error("agent stopped with error", "error", err)
		}
		return err
	}
	if *mode == "service" {
		return service.Run(work)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return work(ctx)
}

// A bounded writer rotates on size so a disconnected service cannot fill its disk with logs.
type rotatingLog struct {
	path string
	file *os.File
	size int64
}

func openServiceLog(path string) (*rotatingLog, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	return &rotatingLog{path, f, st.Size()}, nil
}
func (l *rotatingLog) Write(p []byte) (int, error) {
	if l.size+int64(len(p)) > 5<<20 {
		if e := l.file.Close(); e != nil {
			return 0, e
		}
		if e := os.Remove(l.path + ".1"); e != nil && !os.IsNotExist(e) {
			return 0, e
		}
		if e := os.Rename(l.path, l.path+".1"); e != nil {
			return 0, e
		}
		f, e := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY, 0600)
		if e != nil {
			return 0, e
		}
		l.file = f
		l.size = 0
	}
	n, e := l.file.Write(p)
	l.size += int64(n)
	return n, e
}
func (l *rotatingLog) Close() error { return l.file.Close() }
