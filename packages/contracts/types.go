// Package contracts contains the implementation-independent v1 wire models.
package contracts

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"time"
)

const Version = "v1"
const MaxBody = 1 << 20

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
func ValidID(s string) bool {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	_, e := hex.DecodeString(s[:8] + s[9:13] + s[14:18] + s[19:23] + s[24:])
	return e == nil
}

// Decode enforces the closed wire objects, required members and body limit.
func Decode(r io.Reader, v any) error {
	b, e := io.ReadAll(io.LimitReader(r, MaxBody+1))
	if e != nil {
		return e
	}
	if len(b) > MaxBody {
		return errors.New("JSON body too large")
	}
	if e = required(b, reflect.TypeOf(v).Elem()); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e = d.Decode(&extra); e != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func required(b []byte, t reflect.Type) error {
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			return nil
		}
		return required(b, t.Elem())
	}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return errors.New("non-null value required")
	}
	if t == reflect.TypeOf(time.Time{}) || t == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if e := json.Unmarshal(b, &fields); e != nil {
			return e
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := f.Tag.Get("json")
			if name == "" || name == "-" {
				continue
			}
			raw, ok := fields[name]
			if !ok {
				return fmt.Errorf("missing required field %s", name)
			}
			if e := required(raw, f.Type); e != nil {
				return fmt.Errorf("%s: %w", name, e)
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if e := json.Unmarshal(b, &values); e != nil {
			return e
		}
		for _, raw := range values {
			if e := required(raw, t.Elem()); e != nil {
				return e
			}
		}
	}
	return nil
}
func UTC(t time.Time) bool { _, offset := t.Zone(); return !t.IsZero() && offset == 0 }
func text(s string) bool   { return len(s) > 0 && len(s) <= 256 }

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}
type Enrollment struct {
	EnrollmentToken string `json:"enrollmentToken"`
	PublicKey       string `json:"publicKey"`
	Hostname        string `json:"hostname"`
	AgentVersion    string `json:"agentVersion"`
}

func (v Enrollment) Validate() error {
	k, e := base64.StdEncoding.DecodeString(v.PublicKey)
	if !text(v.EnrollmentToken) || !text(v.Hostname) || !text(v.AgentVersion) || e != nil || len(k) != 32 {
		return errors.New("invalid enrollment")
	}
	return nil
}

type Credential struct {
	DeviceID    string    `json:"deviceId"`
	AccessToken string    `json:"accessToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
}
type Rotation struct {
	PublicKey string `json:"publicKey"`
}

func (v Rotation) Validate() error {
	b, e := base64.StdEncoding.DecodeString(v.PublicKey)
	if e != nil || len(b) != 32 {
		return errors.New("invalid Ed25519 public key")
	}
	return nil
}

type Heartbeat struct {
	AgentVersion  string    `json:"agentVersion"`
	Status        string    `json:"status"`
	UptimeSeconds uint64    `json:"uptimeSeconds"`
	KioskRunning  *bool     `json:"kioskRunning"`
	LastActivity  time.Time `json:"lastActivity"`
	Health        string    `json:"health"`
}

func (v Heartbeat) Validate() error {
	if !text(v.AgentVersion) || (v.Status != "online" && v.Status != "degraded") || !UTC(v.LastActivity) || !text(v.Health) {
		return errors.New("invalid heartbeat")
	}
	return nil
}

type ProcessMetric struct {
	PID         int32   `json:"pid"`
	CPUPercent  float64 `json:"cpuPercent"`
	MemoryBytes uint64  `json:"memoryBytes"`
}
type Metrics struct {
	CollectedAt      time.Time       `json:"collectedAt"`
	CPUPercent       float64         `json:"cpuPercent"`
	MemoryTotalBytes uint64          `json:"memoryTotalBytes"`
	MemoryUsedBytes  uint64          `json:"memoryUsedBytes"`
	DiskTotalBytes   uint64          `json:"diskTotalBytes"`
	DiskUsedBytes    uint64          `json:"diskUsedBytes"`
	Processes        []ProcessMetric `json:"processes"`
}

func (v Metrics) Validate() error {
	if !UTC(v.CollectedAt) || v.CPUPercent < 0 || v.CPUPercent > 100 || v.MemoryUsedBytes > v.MemoryTotalBytes || v.DiskUsedBytes > v.DiskTotalBytes || len(v.Processes) > 1000 {
		return errors.New("invalid metrics")
	}
	for _, p := range v.Processes {
		if p.PID <= 0 || p.CPUPercent < 0 {
			return errors.New("invalid process metric")
		}
	}
	return nil
}

type Software struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type Network struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}
type Inventory struct {
	CollectedAt  time.Time  `json:"collectedAt"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	Architecture string     `json:"architecture"`
	CPU          string     `json:"cpu"`
	MemoryBytes  uint64     `json:"memoryBytes"`
	StorageBytes uint64     `json:"storageBytes"`
	Software     []Software `json:"software"`
	Runtimes     []Software `json:"runtimes"`
	Networks     []Network  `json:"networks"`
}

func (v Inventory) Validate() error {
	if !UTC(v.CollectedAt) || !text(v.Hostname) || !text(v.OS) || !text(v.Architecture) || len(v.Software) > 2000 || len(v.Runtimes) > 100 || len(v.Networks) > 100 {
		return errors.New("invalid inventory")
	}
	return nil
}

type Policy struct {
	SchemaVersion          int      `json:"schemaVersion"`
	Revision               string   `json:"revision"`
	KioskExecutable        string   `json:"kioskExecutable"`
	Arguments              []string `json:"arguments"`
	AutoLaunch             bool     `json:"autoLaunch"`
	MonitorIntervalSeconds int      `json:"monitorIntervalSeconds"`
	RestartPolicy          string   `json:"restartPolicy"`
	ExpectedShell          string   `json:"expectedShell"`
	StartupEntries         []string `json:"startupEntries"`
}
type PolicyReport struct {
	Revision   string    `json:"revision"`
	ObservedAt time.Time `json:"observedAt"`
	Compliance string    `json:"compliance"`
	Drift      []string  `json:"drift"`
	Errors     []string  `json:"errors"`
}

func (v PolicyReport) Validate() error {
	if !text(v.Revision) || !UTC(v.ObservedAt) || (v.Compliance != "compliant" && v.Compliance != "drifted" && v.Compliance != "unknown") {
		return errors.New("invalid compliance report")
	}
	return nil
}

type Release struct {
	Version     string `json:"version"`
	PackageType string `json:"packageType"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Signature   string `json:"signature"`
	KeyID       string `json:"keyId"`
	SizeBytes   int64  `json:"sizeBytes"`
}
type Job struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	CreatedAt      time.Time `json:"createdAt"`
	LeaseExpiresAt time.Time `json:"leaseExpiresAt"`
	TargetVersion  string    `json:"targetVersion"`
	Release        *Release  `json:"release"`
}

func (v Job) Validate() error {
	if v.Release != nil {
		if e := v.Release.Validate(); e != nil {
			return e
		}
	}
	if !ValidID(v.ID) || (v.Kind != "deployment" && v.Kind != "agent_update" && v.Kind != "diagnostics") || !UTC(v.CreatedAt) || !UTC(v.LeaseExpiresAt) || !v.LeaseExpiresAt.After(v.CreatedAt) {
		return errors.New("invalid job")
	}
	return nil
}

type JobPage struct {
	Items      []Job  `json:"items"`
	NextCursor string `json:"nextCursor"`
}
type Ack struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

func (v Ack) Validate() error {
	if !text(v.Reason) {
		return errors.New("reason required")
	}
	return nil
}

type JobStatus struct {
	State     string    `json:"state"`
	Progress  int       `json:"progress"`
	Message   string    `json:"message"`
	UpdatedAt time.Time `json:"updatedAt"`
}

var States = map[string]bool{"pending": true, "accepted": true, "downloading": true, "verifying": true, "installing": true, "validating": true, "succeeded": true, "failed": true, "rolling_back": true, "rolled_back": true, "cancelled": true}

func (v JobStatus) Validate() error {
	if !States[v.State] || v.Progress < 0 || v.Progress > 100 || !UTC(v.UpdatedAt) || !text(v.Message) {
		return errors.New("invalid job status")
	}
	return nil
}

type Renew struct {
	LeaseSeconds int `json:"leaseSeconds"`
}

func (v Renew) Validate() error {
	if v.LeaseSeconds < 30 || v.LeaseSeconds > 300 {
		return errors.New("leaseSeconds must be 30..300")
	}
	return nil
}

type Lease struct {
	LeaseExpiresAt time.Time `json:"leaseExpiresAt"`
}
type UpdateStatus struct {
	TargetVersion string    `json:"targetVersion"`
	State         string    `json:"state"`
	Message       string    `json:"message"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (v UpdateStatus) Validate() error {
	switch v.State {
	case "staged", "switching", "verifying", "succeeded", "failed", "rolling_back", "rolled_back":
	default:
		return errors.New("invalid update state")
	}
	if !text(v.TargetVersion) || !text(v.Message) || !UTC(v.UpdatedAt) {
		return fmt.Errorf("invalid update report")
	}
	return nil
}

func (v Policy) Validate() error {
	if v.SchemaVersion != 1 || !text(v.Revision) || v.MonitorIntervalSeconds < 1 || (v.RestartPolicy != "never" && v.RestartPolicy != "on_failure") {
		return errors.New("invalid policy")
	}
	return nil
}
func (v Release) Validate() error {
	u, e := url.Parse(v.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("invalid release URL")
	}
	h, e := hex.DecodeString(v.SHA256)
	if e != nil || len(h) != 32 {
		return errors.New("invalid release digest")
	}
	sig, e := base64.StdEncoding.DecodeString(v.Signature)
	if e != nil || len(sig) != 64 || !text(v.KeyID) || v.SizeBytes < 0 {
		return errors.New("invalid signature metadata")
	}
	switch v.PackageType {
	case "msi", "exe", "zip", "kiosk", "agent", "configuration", "simulation":
	default:
		return errors.New("unsupported package type")
	}
	return nil
}
