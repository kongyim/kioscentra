package main

import (
	"bytes"
	"context"
	"runtime"
	"testing"
)

func TestRequiresExplicitExecutionAndWindows(t *testing.T) {
	var out bytes.Buffer
	if run(context.Background(), []string{"--authorization", "unused.json"}, &out) == nil {
		t.Fatal("implicit execution accepted")
	}
	if runtime.GOOS != "windows" && run(context.Background(), []string{"--execute", "--authorization", "unused.json"}, &out) == nil {
		t.Fatal("non-Windows execution accepted")
	}
	if out.Len() != 0 {
		t.Fatal("reported a successful execution")
	}
}
