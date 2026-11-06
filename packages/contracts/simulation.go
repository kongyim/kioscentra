package contracts

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

const SimulationKeyID = "development-simulation-v1"
const simulationOrigin = "https://simulation.invalid/"

// SimulationScenario is a closed fixture catalog, never a URL to fetch or a command.
func SimulationScenario(url string) (string, error) {
	return simulationScenario(url, false)
}
func UpdateSimulationScenario(url string) (string, error) { return simulationScenario(url, true) }
func simulationScenario(url string, update bool) (string, error) {
	s := strings.TrimPrefix(url, simulationOrigin)
	if url != simulationOrigin+s {
		return "", errors.New("unsupported simulation origin")
	}
	if update {
		switch s {
		case "success", "hash_failure", "helper_launch_failure", "service_start_failure", "health_timeout", "rollback_failure":
			return s, nil
		}
		return "", errors.New("unsupported update simulation scenario")
	}
	switch s {
	case "success", "download_failure", "hash_failure", "install_failure", "validation_failure", "rollback_failure", "timeout":
		return s, nil
	}
	return "", errors.New("unsupported simulation scenario")
}
func SimulationPayload(version, scenario string) []byte {
	return []byte("kiosentra-simulation-v1\n" + version + "\n" + scenario + "\n")
}

// SimulationSigningBytes binds immutable job metadata to its enrolled device.
// Lease expiry is excluded because it changes on renewal. JSON struct order is canonical.
func SimulationSigningBytes(deviceID string, j Job) []byte {
	r := *j.Release
	r.Signature = ""
	b, _ := json.Marshal(struct {
		Domain   string  `json:"domain"`
		DeviceID string  `json:"deviceId"`
		ID       string  `json:"id"`
		Kind     string  `json:"kind"`
		Target   string  `json:"targetVersion"`
		Release  Release `json:"release"`
	}{"kiosentra-simulation-job-v1", deviceID, j.ID, j.Kind, j.TargetVersion, r})
	return b
}
func SignSimulation(deviceID string, j *Job, scenario string, key ed25519.PrivateKey) error {
	if len(key) != ed25519.PrivateKeySize {
		return errors.New("invalid simulation signing key")
	}
	if scenario == "" {
		scenario = "success"
	}
	if _, e := simulationScenario(simulationOrigin+scenario, j.Kind == "agent_update"); e != nil {
		return e
	}
	payload := SimulationPayload(j.TargetVersion, scenario)
	digest := sha256.Sum256(payload)
	if scenario == "hash_failure" {
		digest[0] ^= 1
	}
	j.Release = &Release{Version: j.TargetVersion, PackageType: "simulation", URL: simulationOrigin + scenario, SHA256: hex.EncodeToString(digest[:]), KeyID: SimulationKeyID, SizeBytes: int64(len(payload))}
	j.Release.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, SimulationSigningBytes(deviceID, *j)))
	return nil
}
func VerifySimulation(deviceID string, j Job, key ed25519.PublicKey) error {
	return verifySimulation(deviceID, j, key, "deployment")
}
func VerifyUpdateSimulation(deviceID string, j Job, key ed25519.PublicKey) error {
	return verifySimulation(deviceID, j, key, "agent_update")
}
func verifySimulation(deviceID string, j Job, key ed25519.PublicKey, kind string) error {
	if e := j.Validate(); e != nil {
		return e
	}
	if !ValidID(deviceID) || len(key) != ed25519.PublicKeySize || j.Kind != kind || j.Release == nil || j.Release.PackageType != "simulation" || j.Release.KeyID != SimulationKeyID || j.TargetVersion == "" || len(j.TargetVersion) > 256 || j.Release.Version != j.TargetVersion || j.Release.SizeBytes > 4096 {
		return errors.New("unsupported simulation job")
	}
	if _, e := simulationScenario(j.Release.URL, kind == "agent_update"); e != nil {
		return e
	}
	sig, e := base64.StdEncoding.DecodeString(j.Release.Signature)
	if e != nil || !ed25519.Verify(key, SimulationSigningBytes(deviceID, j), sig) {
		return errors.New("simulation signature or device binding invalid")
	}
	return nil
}
func TerminalState(state string) bool {
	return state == "succeeded" || state == "failed" || state == "rolled_back" || state == "cancelled"
}
func DeploymentTransition(from, to string) bool {
	if TerminalState(from) {
		return false
	}
	if to == "failed" || to == "cancelled" {
		return from != ""
	}
	switch from {
	case "":
		return to == "accepted"
	case "accepted":
		return to == "downloading"
	case "downloading":
		return to == "verifying"
	case "verifying":
		return to == "installing"
	case "installing":
		return to == "validating" || to == "rolling_back"
	case "validating":
		return to == "succeeded" || to == "rolling_back"
	case "rolling_back":
		return to == "rolled_back"
	}
	return false
}
