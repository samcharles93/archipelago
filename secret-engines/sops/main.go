// sops resolves a top-level key from a sops-encrypted file (YAML, JSON, dotenv
// or INI). It needs the sops CLI on PATH and SOPS_FILE (the encrypted file).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/samcharles93/archipelago/sdk/secretengine"
)

func resolve(ctx context.Context, key string) (string, error) {
	file := os.Getenv("SOPS_FILE")
	if file == "" {
		return "", errors.New("SOPS_FILE is not set")
	}
	// The whole file decrypts to JSON and the key is looked up here, so a
	// missing key reads as missing rather than as a failed decrypt, and the
	// key never reaches the CLI.
	plain, err := secretengine.Run(ctx, "sops", "--decrypt", "--output-type", "json", file)
	if err != nil {
		return "", fmt.Errorf("decrypt %s: %w", file, err)
	}
	return lookup([]byte(plain), key)
}

// lookup returns a top-level key's value: a string as written, anything else
// as JSON.
func lookup(plain []byte, key string) (string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(plain, &doc); err != nil {
		return "", fmt.Errorf("decode decrypted file: %w", err)
	}
	raw, ok := doc[key]
	if !ok || string(raw) == "null" {
		return "", secretengine.ErrNotFound
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return "", secretengine.ErrNotFound
		}
		return s, nil
	}
	return string(raw), nil
}

func main() { secretengine.ServeFunc(resolve) }
