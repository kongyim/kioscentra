#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
modules='./apps/agent/... ./packages/contracts/... ./tools/mock-server/...'
# Intentional word splitting: explicit Go package patterns.
go test $modules
go test -race $modules
go vet $modules
go build -o bin/kiosentra-agent ./apps/agent/cmd/kiosentra-agent
go build -o bin/mock-server ./tools/mock-server/cmd/mock-server
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/kiosentra-agent.exe ./apps/agent/cmd/kiosentra-agent
