// vault resolves a field of a KV secret over the Vault API (also works with
// OpenBao), on KV version 1 or 2 mounts alike. It needs VAULT_ADDR and
// VAULT_TOKEN. A key is "path/field"; a key with no slash is a field under
// VAULT_SECRET_PATH.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	vault "github.com/hashicorp/vault/api"

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
	client, err := vault.NewClient(vault.DefaultConfig())
	if err != nil {
		return "", fmt.Errorf("vault client: %w", err)
	}
	data, err := read(ctx, client.Logical(), path)
	if err != nil {
		return "", err
	}
	value, ok := data[field].(string)
	if !ok || value == "" {
		return "", secretengine.ErrNotFound
	}
	return value, nil
}

// read returns a KV secret's fields, finding the mount and its KV version the
// way the vault CLI does. A secret that does not exist is ErrNotFound.
func read(ctx context.Context, logical *vault.Logical, path string) (map[string]any, error) {
	mount, version, err := kvMount(ctx, logical, path)
	if err != nil {
		return nil, err
	}
	readPath := path
	if version == "2" {
		readPath = mount + "data/" + strings.TrimPrefix(path, mount)
	}
	secret, err := logical.ReadWithContext(ctx, readPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if secret == nil || secret.Data == nil {
		return nil, secretengine.ErrNotFound
	}
	if version != "2" {
		return secret.Data, nil
	}
	data, _ := secret.Data["data"].(map[string]any)
	if data == nil {
		return nil, secretengine.ErrNotFound // deleted or destroyed version
	}
	return data, nil
}

// kvMount returns the mount path (with its trailing slash) holding path and
// its KV version, "1" or "2".
func kvMount(ctx context.Context, logical *vault.Logical, path string) (string, string, error) {
	secret, err := logical.ReadWithContext(ctx, "sys/internal/ui/mounts/"+path)
	if err != nil {
		return "", "", fmt.Errorf("find the mount for %s: %w", path, err)
	}
	if secret == nil || secret.Data == nil {
		return "", "", fmt.Errorf("find the mount for %s: no mount holds it", path)
	}
	mount, _ := secret.Data["path"].(string)
	version := "1"
	if options, ok := secret.Data["options"].(map[string]any); ok {
		if v, ok := options["version"].(string); ok && v != "" {
			version = v
		}
	}
	return mount, version, nil
}

// target splits key into a secret path and field. A path with a leading "-"
// or a ".." segment is refused: neither names a KV secret.
func target(key string) (path, field string, err error) {
	path, field = os.Getenv("VAULT_SECRET_PATH"), key
	if i := strings.LastIndex(key, "/"); i >= 0 {
		path, field = key[:i], key[i+1:]
	}
	if path == "" || field == "" || strings.HasPrefix(path, "-") || strings.Contains("/"+path+"/", "/../") {
		return "", "", fmt.Errorf("key %q is not a valid path/field (a key with no path needs VAULT_SECRET_PATH)", key)
	}
	return path, field, nil
}

func main() { secretengine.ServeFunc(resolve) }
