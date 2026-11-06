package ipc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"

	"kiosentra/apps/agent/internal/configuration"
)

// Hashes are for independently generated high-entropy tokens, not human passwords.
type Technician struct {
	SHA256 string   `json:"sha256"`
	Scopes []string `json:"scopes"`
}

var technicianID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func LoadTechnicians(path string) (map[string]Technician, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("technician file must be an absolute path")
	}
	b, err := configuration.ReadPrivateFile(path)
	if err != nil {
		return nil, err
	}
	if !json.Valid(b) {
		return nil, errors.New("invalid technician configuration")
	}
	var entries map[string]Technician
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&entries); err != nil {
		return nil, errors.New("invalid technician configuration")
	}
	if len(entries) == 0 || len(entries) > 100 {
		return nil, errors.New("configure between 1 and 100 technicians")
	}
	for id, entry := range entries {
		hash, err := hex.DecodeString(entry.SHA256)
		if !technicianID.MatchString(id) || err != nil || len(hash) != 32 || len(entry.Scopes) == 0 {
			return nil, errors.New("invalid technician identity, token hash or scopes")
		}
		for _, scope := range entry.Scopes {
			if !allowed(scope) {
				return nil, errors.New("invalid technician scope")
			}
		}
	}
	return entries, nil
}

func (s *Server) MaintenanceEnabled() bool { return len(s.hash) != 0 || len(s.Technicians) != 0 }
