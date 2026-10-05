// Command catalogue signs a catalogue layer for archie-core: it reads the
// catalogue JSON on stdin and prints the base64 ed25519 signature over those
// exact bytes. ARCHIPELAGO_CATALOGUE_KEY holds the base64 private key seed.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "catalogue:", err)
		os.Exit(1)
	}
}

func run() error {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("ARCHIPELAGO_CATALOGUE_KEY")))
	if err != nil || len(seed) != ed25519.SeedSize {
		return fmt.Errorf("ARCHIPELAGO_CATALOGUE_KEY must be a base64 ed25519 seed")
	}
	layer, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(seed), layer)))
	return nil
}
