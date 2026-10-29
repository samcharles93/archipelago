// Command catalogue stamps and signs a catalogue layer for archie-core: it
// reads the catalogue JSON, stamps it with the current UTC issue time, writes
// the exact bytes it signs, and prints the base64 ed25519 signature over
// them. ARCHIPELAGO_CATALOGUE_KEY holds the base64 private key seed.
//
//	catalogue <catalogue.json> <stamped.json>
//
// The issue time rides under the signature, and archie-core refuses a
// catalogue older than the newest it has accepted, so every published
// catalogue carries a fresh stamp: an older validly signed one is a replay.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	signature, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "catalogue:", err)
		os.Exit(1)
	}
	fmt.Println(signature)
}

func run(args []string) (string, error) {
	if len(args) != 2 {
		return "", errors.New("usage: catalogue <catalogue.json> <stamped.json>")
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("ARCHIPELAGO_CATALOGUE_KEY")))
	if err != nil || len(seed) != ed25519.SeedSize {
		return "", fmt.Errorf("ARCHIPELAGO_CATALOGUE_KEY must be a base64 ed25519 seed")
	}
	layer, err := os.ReadFile(args[0])
	if err != nil {
		return "", err
	}
	stamped, err := stamp(layer, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(args[1], stamped, 0o600); err != nil {
		return "", err
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(seed), stamped))
	return signature, nil
}

// stamp returns the catalogue layer with its issue time set to issuedAt,
// truncated to the second. The stamp is overwritten, never read from the
// input: a catalogue is signed only as freshly issued as the moment it is
// published.
func stamp(layer []byte, issuedAt time.Time) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(layer, &doc); err != nil {
		return nil, fmt.Errorf("decode catalogue: %w", err)
	}
	if _, ok := doc["packages"]; !ok {
		return nil, errors.New("catalogue has no packages list")
	}
	encoded, err := json.Marshal(issuedAt.UTC().Truncate(time.Second))
	if err != nil {
		return nil, err
	}
	doc["issued_at"] = encoded
	return json.Marshal(doc)
}
