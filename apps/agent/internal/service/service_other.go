//go:build !windows

package service

import (
	"context"
	"errors"
)

func Run(_ func(context.Context) error) error {
	return errors.New("Windows Service mode is unsupported on this OS; use --mode=console for development")
}
