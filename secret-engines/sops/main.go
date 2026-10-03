// sops resolves a top-level key from a sops-encrypted file. It needs the sops
// CLI on PATH and SOPS_FILE (the encrypted file).
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
	file := os.Getenv("SOPS_FILE")
	if file == "" {
		return "", errors.New("SOPS_FILE is not set")
	}
	if strings.ContainsAny(key, `"\[`) {
		return "", fmt.Errorf("key %q is not a valid --extract path", key)
	}
	value, err := secretengine.Run(ctx, "sops", "-d", "--extract", `["`+key+`"]`, file)
	if err != nil {
		return "", fmt.Errorf("decrypt %s: %w", file, err)
	}
	if value == "" {
		return "", secretengine.ErrNotFound
	}
	return value, nil
}

func main() { secretengine.ServeFunc(resolve) }
