package configuration

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

type Config struct {
	ServerURL, StateDir, EnrollmentToken, DevelopmentKey string
	Development                                          bool
	SimulatedDeployments                                 bool
	SimulatedUpdates                                     bool
	SimulationPublicKey                                  string
	Interval, RequestTimeout                             time.Duration
	MaxQueue                                             int
}

func Load() (Config, error) {
	dir := os.Getenv("KIOSENTRA_STATE_DIR")
	if dir == "" {
		if runtime.GOOS == "windows" {
			base := os.Getenv("ProgramData")
			if base == "" {
				return Config{}, errors.New("ProgramData missing")
			}
			dir = filepath.Join(base, "Kiosentra")
		} else {
			base, e := os.UserConfigDir()
			if e != nil {
				return Config{}, e
			}
			dir = filepath.Join(base, "kiosentra")
		}
	}
	c := Config{ServerURL: os.Getenv("KIOSENTRA_SERVER_URL"), StateDir: dir, EnrollmentToken: os.Getenv("KIOSENTRA_ENROLLMENT_TOKEN"), DevelopmentKey: os.Getenv("KIOSENTRA_DEVELOPMENT_KEY"), Interval: 30 * time.Second, RequestTimeout: 10 * time.Second, MaxQueue: 1000}
	if c.ServerURL == "" {
		c.ServerURL = "https://localhost:8443"
	}
	if v := os.Getenv("KIOSENTRA_DEVELOPMENT"); v != "" {
		b, e := strconv.ParseBool(v)
		if e != nil {
			return c, e
		}
		c.Development = b
	}
	if v := os.Getenv("KIOSENTRA_INTERVAL"); v != "" {
		d, e := time.ParseDuration(v)
		if e != nil {
			return c, e
		}
		c.Interval = d
	}
	c.SimulationPublicKey = os.Getenv("KIOSENTRA_SIMULATION_PUBLIC_KEY")
	if v := os.Getenv("KIOSENTRA_SIMULATED_DEPLOYMENTS"); v != "" {
		b, e := strconv.ParseBool(v)
		if e != nil {
			return c, e
		}
		c.SimulatedDeployments = b
	}
	if v := os.Getenv("KIOSENTRA_SIMULATED_UPDATES"); v != "" {
		b, e := strconv.ParseBool(v)
		if e != nil {
			return c, e
		}
		c.SimulatedUpdates = b
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.SimulatedDeployments || c.SimulatedUpdates {
		key, e := base64.StdEncoding.DecodeString(c.SimulationPublicKey)
		if !c.Development || e != nil || len(key) != ed25519.PublicKeySize {
			return errors.New("simulated deployments/updates require development mode and a base64 Ed25519 public key")
		}
	}

	u, e := url.Parse(c.ServerURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("server URL must be an origin without credentials, path or query")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !c.Development || ip == nil || !ip.IsLoopback() {
			return errors.New("HTTPS required; development HTTP requires a literal loopback address")
		}
	}
	if !filepath.IsAbs(c.StateDir) {
		return errors.New("state directory must be absolute")
	}
	if c.Interval < time.Second || c.Interval > time.Hour || c.RequestTimeout < time.Second || c.MaxQueue < 1 {
		return errors.New("invalid interval, timeout or queue limit")
	}
	return nil
}
