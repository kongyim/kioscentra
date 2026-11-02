package configuration

import (
	"testing"
	"time"
)

func TestTransportBoundary(t *testing.T) {
	for _, tc := range []struct {
		url     string
		dev, ok bool
	}{{"https://central.example", false, true}, {"http://127.0.0.1:8080", true, true}, {"http://[::1]:8080", true, true}, {"http://127.0.0.1:8080", false, false}, {"http://localhost:8080", true, false}, {"http://central.example", true, false}, {"https://user:secret@central.example", false, false}, {"https://central.example/api", false, false}, {"https://central.example?token=x", false, false}} {
		t.Run(tc.url, func(t *testing.T) {
			c := Config{ServerURL: tc.url, StateDir: t.TempDir(), Development: tc.dev, Interval: time.Second, RequestTimeout: time.Second, MaxQueue: 10}
			if (c.Validate() == nil) != tc.ok {
				t.Fatalf("unexpected validation for %#v", tc)
			}
		})
	}
}
