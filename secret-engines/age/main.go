// age resolves a key from an age-encrypted file of "key=value" lines. It needs
// AGE_FILE (the encrypted file) and AGE_IDENTITY (the age private-key file).
// Blank lines and "#" comments are skipped, a value may contain "=", and an
// empty value counts as missing.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"

	"github.com/samcharles93/archipelago/sdk/secretengine"
)

func resolve(_ context.Context, key string) (string, error) {
	file, identity := os.Getenv("AGE_FILE"), os.Getenv("AGE_IDENTITY")
	if file == "" || identity == "" {
		return "", errors.New("AGE_FILE and AGE_IDENTITY must be set")
	}
	plain, err := decrypt(file, identity)
	if err != nil {
		return "", err
	}
	return lookup(plain, key)
}

func decrypt(file, identityFile string) ([]byte, error) {
	keys, err := os.ReadFile(identityFile)
	if err != nil {
		return nil, fmt.Errorf("read identity: %w", err)
	}
	identities, err := age.ParseIdentities(bytes.NewReader(keys))
	if err != nil {
		return nil, fmt.Errorf("parse identity %s: %w", identityFile, err)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	defer func() { _ = f.Close() }()
	r, err := age.Decrypt(f, identities...)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", file, err)
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", file, err)
	}
	return plain, nil
}

// lookup finds key in "key=value" lines.
func lookup(plain []byte, key string) (string, error) {
	for line := range strings.SplitSeq(string(plain), "\n") {
		line = strings.TrimSpace(line)
		name, value, ok := strings.Cut(line, "=")
		if line == "" || strings.HasPrefix(line, "#") || !ok || strings.TrimSpace(name) != key {
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			return value, nil
		}
		break
	}
	return "", secretengine.ErrNotFound
}

func main() { secretengine.ServeFunc(resolve) }
