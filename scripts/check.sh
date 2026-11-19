#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
modules='./apps/agent/... ./apps/server/... ./packages/contracts/... ./tools/mock-server/...'
# Intentional word splitting: explicit Go package patterns.
go test $modules
go test -race $modules
go vet $modules
go build -o bin/kiosentra-agent ./apps/agent/cmd/kiosentra-agent
go build -o bin/mock-server ./tools/mock-server/cmd/mock-server
go build -o bin/kiosentra-server ./apps/server/cmd/kiosentra-server
go build -o bin/kiosentra-stage ./apps/agent/cmd/kiosentra-stage
go build -o bin/kiosentra-sign-artifact ./apps/agent/cmd/kiosentra-sign-artifact
go build -o bin/kiosentra-install-msi ./apps/agent/cmd/kiosentra-install-msi
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/kiosentra-agent.exe ./apps/agent/cmd/kiosentra-agent
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/kiosentra-stage.exe ./apps/agent/cmd/kiosentra-stage
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/kiosentra-sign-artifact.exe ./apps/agent/cmd/kiosentra-sign-artifact
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/kiosentra-install-msi.exe ./apps/agent/cmd/kiosentra-install-msi
