package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The signer stamps the issue time and signs exactly the stamped bytes, so a
// catalogue is never published without a fresh stamp under its signature,
// and a stamp in the input is never trusted.
func TestRunStampsAndSigns(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "catalogue.json")
	stamped := filepath.Join(dir, "stamped.json")
	layer := `{"packages":[{"name":"github","reference":"ghcr.io/x/github","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]}`
	if err := os.WriteFile(input, []byte(layer), 0o600); err != nil {
		t.Fatal(err)
	}
	public, signer, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARCHIPELAGO_CATALOGUE_KEY", base64.StdEncoding.EncodeToString(signer.Seed()))

	signature, err := run([]string{input, stamped})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	written, err := os.ReadFile(stamped)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		t.Fatalf("signature is not base64: %v", err)
	}
	if !ed25519.Verify(public, written, decoded) {
		t.Fatal("signature does not verify over the stamped layer")
	}
	var doc struct {
		IssuedAt time.Time        `json:"issued_at"`
		Packages []map[string]any `json:"packages"`
	}
	if err := json.Unmarshal(written, &doc); err != nil {
		t.Fatalf("stamped layer: %v", err)
	}
	if doc.IssuedAt.IsZero() {
		t.Fatal("stamped layer has no issue time")
	}
	if age := time.Since(doc.IssuedAt); age < 0 || age > time.Minute {
		t.Fatalf("issue time is %s old; want a fresh stamp", age)
	}
	if len(doc.Packages) != 1 || doc.Packages[0]["name"] != "github" {
		t.Fatalf("stamped layer lost its packages: %s", written)
	}
}

// A stamp already in the input is overwritten, so a stale one cannot be
// signed through.
func TestStampOverwrites(t *testing.T) {
	layer := []byte(`{"issued_at":"2000-01-01T00:00:00Z","packages":[]}`)
	stamped, err := stamp(layer, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		IssuedAt time.Time `json:"issued_at"`
	}
	if err := json.Unmarshal(stamped, &doc); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC); !doc.IssuedAt.Equal(want) {
		t.Fatalf("issued_at = %s; want %s", doc.IssuedAt.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// A catalogue without a packages list, and a missing key, are refused rather
// than signed.
func TestRunRefuses(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "catalogue.json")
	stamped := filepath.Join(dir, "stamped.json")
	if err := os.WriteFile(input, []byte(`{"issued_at":"2026-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run([]string{input, stamped}); err == nil {
		t.Fatal("a catalogue with no packages list: err = nil, want an error")
	}
	if err := os.WriteFile(input, []byte(`{"packages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARCHIPELAGO_CATALOGUE_KEY", "not-a-key")
	if _, err := run([]string{input, stamped}); err == nil {
		t.Fatal("a bad key: err = nil, want an error")
	}
	if _, err := run([]string{input}); err == nil {
		t.Fatal("wrong argument count: err = nil, want an error")
	}
}
