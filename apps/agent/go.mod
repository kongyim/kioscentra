module kiosentra/apps/agent

go 1.26.0

require (
	github.com/gofrs/flock v0.13.1
	github.com/shirou/gopsutil/v4 v4.26.10
	golang.org/x/sys v0.49.0
	kiosentra/packages/contracts v0.0.0
	modernc.org/sqlite v1.61.1
)

require (
	github.com/coder/websocket v1.8.14 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/ebitengine/purego v0.11.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/power-devops/perfstat v0.0.0-20260805114148-88456608a4f6 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	golang.org/x/term v0.46.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace kiosentra/packages/contracts => ../../packages/contracts

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/wailsapp/wails/v3 v3.1.0
	kiosentra/apps/server v0.0.0
	kiosentra/tools/mock-server v0.0.0
)

replace kiosentra/tools/mock-server => ../../tools/mock-server

replace kiosentra/apps/server => ../server
