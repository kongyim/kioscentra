//go:build !desktop

package ui

import "errors"

func Run() error {
	return errors.New("this is a headless build; build with -tags desktop after building apps/agent/frontend")
}
