//go:build desktop

package ui

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"kiosentra/apps/agent/frontend"
	"kiosentra/apps/agent/internal/configuration"
	"kiosentra/apps/agent/internal/ipc"
)

func Run() error {
	c, e := configuration.Load()
	if e != nil {
		return e
	}
	options, e := ipc.LoadOptions(c.StateDir, c.Development)
	if e != nil {
		return e
	}
	if len(options.Key) == 0 {
		return errors.New("provision KIOSENTRA_IPC_KEY for the service and UI")
	}
	bridge := &Bridge{client: ipc.Client{Options: options}}
	warn := func(e error) { bridge.mu.Lock(); bridge.warning = e.Error(); bridge.mu.Unlock() }
	a := application.New(application.Options{Name: "Kiosentra", Description: "Local device console", Services: []application.Service{application.NewService(bridge)}, Assets: application.AssetOptions{Handler: application.BundledAssetFileServer(frontend.Assets())}, ErrorHandler: warn})
	window := a.Window.NewWithOptions(application.WebviewWindowOptions{Name: "Kiosentra console", Title: "Kiosentra · Device console", Width: 1180, Height: 800, MinWidth: 780, MinHeight: 560, URL: "/", Hidden: os.Getenv("KIOSENTRA_UI_SHOW") != "true", DevToolsEnabled: false})
	show := func() { window.Show(); window.Focus() }
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { e.Cancel(); window.Hide() })
	tray := a.SystemTray.New()
	tray.SetIcon(trayIcon())
	tray.SetTooltip("Kiosentra · Device console")
	tray.OnClick(show)
	menu := a.NewMenu()
	menu.Add("Open console").OnClick(func(*application.Context) { show() })
	menu.Add("Hide console").OnClick(func(*application.Context) { window.Hide() })
	menu.AddSeparator()
	menu.Add("Exit console (service keeps running)").OnClick(func(*application.Context) { a.Quit() })
	tray.SetMenu(menu)
	shortcut := os.Getenv("KIOSENTRA_UI_HOTKEY")
	if shortcut == "" {
		shortcut = "Ctrl+Alt+Shift+F12"
	}
	a.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if err := a.GlobalShortcut.Register(shortcut, show); err != nil {
			warn(err)
			show()
		}
	})
	a.OnShutdown(func() { a.GlobalShortcut.UnregisterAll(); tray.Destroy() })
	return a.Run()
}
func trayIcon() []byte {
	icon := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			c := color.RGBA{25, 194, 164, 255}
			if x < 5 || x > 26 || y < 5 || y > 26 {
				c = color.RGBA{17, 24, 39, 255}
			}
			if x >= 11 && x <= 14 && y >= 10 && y <= 22 || x >= 15 && x <= 21 && (y == 16-(x-15) || y == 16+(x-15)) {
				c = color.RGBA{17, 24, 39, 255}
			}
			icon.Set(x, y, c)
		}
	}
	var out bytes.Buffer
	png.Encode(&out, icon)
	return out.Bytes()
}
