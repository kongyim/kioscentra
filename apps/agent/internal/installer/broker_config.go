package installer

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"

	"kiosentra/apps/agent/internal/configuration"
	"kiosentra/apps/agent/internal/ipc"
	"kiosentra/packages/contracts"
)

const BrokerDomain = "kiosentra-installer-broker-v1"

type BrokerConfig struct {
	DeviceID         string            `json:"deviceId"`
	StateDirectory   string            `json:"stateDirectory"`
	StagingDirectory string            `json:"stagingDirectory"`
	AgentSID         string            `json:"agentSid"`
	IPCKey           string            `json:"ipcKey"`
	ReleaseKeys      map[string]string `json:"releaseKeys"`
	LeaseKeys        map[string]string `json:"leaseKeys"`
	Origins          []string          `json:"origins"`
	TimeoutSeconds   int               `json:"timeoutSeconds"`
}

type BrokerClientConfig struct {
	StateDirectory string `json:"stateDirectory"`
	IPCKey         string `json:"ipcKey"`
	Development    bool   `json:"development"`
}

func LoadBrokerConfig(path string) (BrokerConfig, error) {
	var c BrokerConfig
	if !filepath.IsAbs(path) {
		return c, errors.New("broker config path must be absolute")
	}
	if err := checkBrokerPath(path); err != nil {
		return c, err
	}
	exe, err := os.Executable()
	if err != nil {
		return c, err
	}
	if err = checkBrokerPath(exe); err != nil {
		return c, err
	}
	b, err := configuration.ReadPrivateFile(path)
	if err != nil {
		return c, err
	}
	if err = contracts.Decode(bytes.NewReader(b), &c); err != nil {
		return c, err
	}
	if !contracts.ValidID(c.DeviceID) || !filepath.IsAbs(c.StateDirectory) || !filepath.IsAbs(c.StagingDirectory) || c.StateDirectory == c.StagingDirectory || c.AgentSID == "" || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 3600 {
		return c, errors.New("invalid broker configuration")
	}
	key, err := base64.StdEncoding.DecodeString(c.IPCKey)
	if err != nil || len(key) != 32 {
		return c, errors.New("broker IPC key must be 32 bytes")
	}
	if _, err = DecodeBrokerKeys(c.ReleaseKeys); err != nil {
		return c, err
	}
	if _, err = DecodeBrokerKeys(c.LeaseKeys); err != nil {
		return c, err
	}
	for _, p := range []string{c.StateDirectory, c.StagingDirectory} {
		if err = checkBrokerPath(p); err != nil {
			return c, err
		}
	}
	return c, nil
}

func DecodeBrokerKeys(values map[string]string) (map[string]ed25519.PublicKey, error) {
	if len(values) == 0 || len(values) > 16 {
		return nil, errors.New("broker requires 1 to 16 trusted keys")
	}
	keys := map[string]ed25519.PublicKey{}
	for id, v := range values {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(k) != 32 || id == "" || len(id) > 128 || id == contracts.SimulationKeyID {
			return nil, errors.New("invalid broker public key")
		}
		keys[id] = k
	}
	return keys, nil
}

func LoadBrokerClient(path string) (ipc.RPCClient, error) {
	var c BrokerClientConfig
	if !filepath.IsAbs(path) {
		return ipc.RPCClient{}, errors.New("broker client config path must be absolute")
	}
	b, err := configuration.ReadPrivateFile(path)
	if err != nil {
		return ipc.RPCClient{}, err
	}
	if err = contracts.Decode(bytes.NewReader(b), &c); err != nil {
		return ipc.RPCClient{}, err
	}
	key, err := base64.StdEncoding.DecodeString(c.IPCKey)
	if err != nil || len(key) != 32 || !filepath.IsAbs(c.StateDirectory) {
		return ipc.RPCClient{}, errors.New("invalid broker client configuration")
	}
	return ipc.RPCClient{Options: ipc.Options{StateDir: c.StateDirectory, Key: key, Development: c.Development}, Domain: BrokerDomain}, nil
}
