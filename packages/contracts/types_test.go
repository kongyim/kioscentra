package contracts

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestStrictJSON(t *testing.T) {
	for _, raw := range []string{`{}`, `{"accepted":false}`, `{"accepted":false,"reason":"x","unexpected":1}`, `{"accepted":null,"reason":"x"}`, `{"accepted":false,"reason":"x"} {}`} {
		var a Ack
		if Decode(strings.NewReader(raw), &a) == nil {
			t.Fatalf("accepted invalid payload %s", raw)
		}
	}
	var a Ack
	if e := Decode(strings.NewReader(`{"accepted":false,"reason":"disabled"}`), &a); e != nil {
		t.Fatal(e)
	}
	if e := Decode(strings.NewReader(strings.Repeat(" ", MaxBody+1)), &a); e == nil {
		t.Fatal("oversized body accepted")
	}
}
func TestContractRequiredFieldsMatchModels(t *testing.T) {
	raw, e := os.ReadFile("openapi-v1.yaml")
	if e != nil {
		t.Fatal(e)
	}
	var spec struct {
		OpenAPI    string         `json:"openapi"`
		Paths      map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Required             []string       `json:"required"`
				Properties           map[string]any `json:"properties"`
				AdditionalProperties bool           `json:"additionalProperties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if e = json.Unmarshal(raw, &spec); e != nil {
		t.Fatal(e)
	}
	if spec.OpenAPI != "3.1.0" || len(spec.Paths) != 13 {
		t.Fatal("unexpected API version or endpoints")
	}
	for _, model := range []any{Error{}, Enrollment{}, Credential{}, Rotation{}, Heartbeat{}, ProcessMetric{}, Metrics{}, Software{}, Network{}, Inventory{}, Policy{}, PolicyReport{}, Release{}, Job{}, JobPage{}, Ack{}, JobStatus{}, Renew{}, Lease{}, UpdateStatus{}} {
		typ := reflect.TypeOf(model)
		schema, ok := spec.Components.Schemas[typ.Name()]
		if !ok {
			t.Fatalf("schema missing for %s", typ.Name())
		}
		if schema.AdditionalProperties || len(schema.Required) != typ.NumField() || len(schema.Properties) != typ.NumField() {
			t.Fatalf("schema shape mismatch %s", typ.Name())
		}
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Tag.Get("json")
			if _, ok = schema.Properties[name]; !ok {
				t.Fatalf("field %s.%s missing", typ.Name(), name)
			}
		}
	}
}
