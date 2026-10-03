// age resolves a key from an age-encrypted file of "key=value" lines. It needs
// the age CLI on PATH, AGE_FILE (the encrypted file) and AGE_IDENTITY (the age
// private-key file). Blank lines and "#" comments are skipped, a value may
// contain "=", and an empty value counts as missing.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/samcharles93/archipelago/sdk/secretengine"
)

func resolve(ctx context.Context, key string) (string, error) {
	file, identity := os.Getenv("AGE_FILE"), os.Getenv("AGE_IDENTITY")
	if file == "" || identity == "" {
		return "", errors.New("AGE_FILE and AGE_IDENTITY must be set")
	}
	out, err := secretengine.Run(ctx, "age", "-d", "-i", identity, file)
	if err != nil {
		return "", fmt.Errorf("decrypt %s: %w", file, err)
	}
	for _, line := range strings.Split(out, "\n") {
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
