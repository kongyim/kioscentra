// Generates development-only simulation signing keys. Never use these for real releases.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(map[string]string{"publicKey": base64.StdEncoding.EncodeToString(pub), "privateKey": base64.StdEncoding.EncodeToString(priv)}); err != nil {
		panic(err)
	}
}
