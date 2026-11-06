package contracts

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestSimulationSignatureBindsDeviceAndMetadata(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	device := ID()
	j := Job{ID: ID(), Kind: "deployment", TargetVersion: "2", CreatedAt: now, LeaseExpiresAt: now.Add(time.Minute)}
	if e := SignSimulation(device, &j, "success", priv); e != nil {
		t.Fatal(e)
	}
	if e := VerifySimulation(device, j, pub); e != nil {
		t.Fatal(e)
	}
	if VerifySimulation(ID(), j, pub) == nil {
		t.Fatal("accepted wrong device")
	}
	j.Release.SizeBytes++
	if VerifySimulation(device, j, pub) == nil {
		t.Fatal("accepted altered metadata")
	}
}
func TestOptionalDecodeDoesNotRelaxRequiredFields(t *testing.T) {
	type request struct {
		Required string `json:"required"`
		Optional string `json:"optional,omitempty"`
	}
	var out request
	if e := Decode(bytes.NewBufferString(`{"required":"yes"}`), &out); e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{`{}`, `{"required":null}`, `{"required":"yes","extra":1}`, `{"required":"yes","optional":null}`} {
		if Decode(bytes.NewBufferString(body), &out) == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestUpdateSimulationCannotCrossJobKindsOrDevices(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	device := ID()
	job := Job{ID: ID(), Kind: "agent_update", TargetVersion: "next", CreatedAt: now, LeaseExpiresAt: now.Add(time.Minute)}
	if e := SignSimulation(device, &job, "health_timeout", priv); e != nil {
		t.Fatal(e)
	}
	if e := VerifyUpdateSimulation(device, job, pub); e != nil {
		t.Fatal(e)
	}
	if VerifySimulation(device, job, pub) == nil || VerifyUpdateSimulation(ID(), job, pub) == nil {
		t.Fatal("accepted wrong scope")
	}
	job.Kind = "deployment"
	if VerifyUpdateSimulation(device, job, pub) == nil {
		t.Fatal("accepted mutated kind")
	}
}
