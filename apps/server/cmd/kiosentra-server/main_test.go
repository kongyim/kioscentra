package main

import "testing"

func TestListenerTLSBoundary(t *testing.T) {
	for _, v := range []struct {
		address, cert, key string
		valid              bool
	}{
		{"127.0.0.1:8080", "", "", true}, {"[::1]:8080", "", "", true},
		{"0.0.0.0:8080", "", "", false}, {"localhost:8080", "", "", false},
		{":8443", "cert.pem", "key.pem", true}, {":8443", "cert.pem", "", false},
	} {
		if err := validateListener(v.address, v.cert, v.key); (err == nil) != v.valid {
			t.Errorf("%+v: %v", v, err)
		}
	}
}
