package storage

import (
	"errors"
	"path/filepath"

	"github.com/gofrs/flock"
)

func Lock(dir string) (func() error, error) {
	l := flock.New(filepath.Join(dir, "agent.lock"))
	ok, e := l.TryLock()
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, errors.New("another agent owns this state directory")
	}
	return l.Unlock, nil
}
