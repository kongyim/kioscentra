//go:build windows

package service

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sys/windows/svc"
)

type handler struct{ run func(context.Context) error }

func (h handler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	current := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	status <- current
	for {
		select {
		case e := <-done:
			if e != nil {
				return true, 1
			}
			return false, 0
		case r, ok := <-requests:
			if !ok {
				cancel()
				return true, 2
			}
			switch r.Cmd {
			case svc.Interrogate:
				status <- current
			case svc.Stop, svc.Shutdown:
				cancel()
				current = svc.Status{State: svc.StopPending, WaitHint: 30000}
				status <- current
				timer := time.NewTimer(25 * time.Second)
				defer timer.Stop()
				select {
				case e := <-done:
					if e != nil {
						return true, 1
					}
					return false, 0
				case <-timer.C:
					return true, 3
				}
			}
		}
	}
}
func Run(run func(context.Context) error) error {
	ok, e := svc.IsWindowsService()
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("--mode=service must be launched by the Windows Service Control Manager")
	}
	return svc.Run("Kiosentra", handler{run})
}
