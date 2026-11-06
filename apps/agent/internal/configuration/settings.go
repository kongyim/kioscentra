package configuration

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Settings files are explicit, bounded and authoritative over inherited environment.
// Installers provision separate service, reader and recovery files with distinct ACLs.
func Settings() (func(string) string, error) {
	values := map[string]string{}
	if path := os.Getenv("KIOSENTRA_CONFIG_FILE"); path != "" {
		if !filepath.IsAbs(path) {
			return nil, errors.New("configuration file must be an absolute path")
		}
		b, err := ReadPrivateFile(path)
		if err != nil {
			return nil, err
		}
		var raw map[string]json.RawMessage
		if err = json.Unmarshal(b, &raw); err != nil || raw == nil {
			return nil, errors.New("configuration must be a JSON object of string settings")
		}
		for key, encoded := range raw {
			var value *string
			if err = json.Unmarshal(encoded, &value); err != nil || value == nil {
				return nil, errors.New("configuration values must be strings")
			}
			values[key] = *value
			switch key {
			case "KIOSENTRA_SERVER_URL", "KIOSENTRA_STATE_DIR", "KIOSENTRA_ENROLLMENT_TOKEN",
				"KIOSENTRA_DEVELOPMENT_KEY", "KIOSENTRA_DEVELOPMENT", "KIOSENTRA_INTERVAL",
				"KIOSENTRA_SIMULATION_PUBLIC_KEY", "KIOSENTRA_SIMULATED_DEPLOYMENTS", "KIOSENTRA_SIMULATED_UPDATES",
				"KIOSENTRA_IPC_KEY", "KIOSENTRA_UI_SID", "KIOSENTRA_MAINTENANCE_SHA256",
				"KIOSENTRA_TECHNICIANS_FILE", "KIOSENTRA_RECOVERY_EXECUTABLE":
			default:
				return nil, errors.New("unknown configuration setting: " + key)
			}
		}
		// File mode is isolated: omitted settings must not inherit service secrets or simulation flags.
		return func(key string) string { return values[key] }, nil
	}
	return os.Getenv, nil
}

func ReadPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("configuration must be a regular file")
	}
	if err = checkPrivateMode(info); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return nil, err
	}
	if len(b) > 65536 {
		return nil, errors.New("configuration exceeds 64 KiB")
	}
	return b, nil
}
