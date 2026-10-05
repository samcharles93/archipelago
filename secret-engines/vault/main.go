// vault resolves a field of a KV secret through the vault CLI (also works with
// OpenBao). It needs VAULT_ADDR and VAULT_TOKEN. A key is "path/field"; a key
// with no slash is a field under VAULT_SECRET_PATH.
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
	if os.Getenv("VAULT_ADDR") == "" || os.Getenv("VAULT_TOKEN") == "" {
		return "", errors.New("VAULT_ADDR and VAULT_TOKEN must be set")
	}
	path, field, err := target(key)
	if err != nil {
		return "", err
	}
	// The key reaches the vault CLI as arguments; a leading "-" would be read
	// as a flag such as -address.
	if strings.HasPrefix(path, "-") || field == "" {
		return "", fmt.Errorf("key %q is not a valid path/field", key)
	}
	value, err := secretengine.Run(ctx, "vault", "kv", "get", "-field="+field, path)
	if err != nil {
		return "", fmt.Errorf("read %s/%s: %w", path, field, err)
	}
	if value == "" {
		return "", secretengine.ErrNotFound
	}
	return value, nil
}

func target(key string) (path, field string, err error) {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[:i], key[i+1:], nil
	}
	prefix := os.Getenv("VAULT_SECRET_PATH")
	if prefix == "" {
		return "", "", errors.New("key has no path and VAULT_SECRET_PATH is not set")
	}
	return prefix, key, nil
}

func main() { secretengine.ServeFunc(resolve) }
