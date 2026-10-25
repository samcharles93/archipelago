// age resolves a key from an age-encrypted file of "key=value" lines. It needs
// AGE_FILE (the encrypted file) and AGE_IDENTITY (the identity file: native
// age keys, an unencrypted SSH private key, or plugin identities whose
// age-plugin-* binary is on PATH).
// Blank lines and "#" comments are skipped, a value may contain "=", and an
// empty value counts as missing.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"filippo.io/age/armor"
	"filippo.io/age/plugin"

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
	identities, err := parseIdentities(keys)
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

// parseIdentities reads an identity file as the age CLI does: an SSH private
// key, or lines of native age keys and plugin identities (such as a YubiKey's
// AGE-PLUGIN-YUBIKEY-...). A passphrase-protected file cannot be used: there
// is no one to type the passphrase.
func parseIdentities(keys []byte) ([]age.Identity, error) {
	trimmed := bytes.TrimSpace(keys)
	switch {
	case bytes.HasPrefix(trimmed, []byte("-----BEGIN")):
		id, err := agessh.ParseIdentity(trimmed)
		if err != nil {
			return nil, fmt.Errorf("SSH key: %w", err)
		}
		return []age.Identity{id}, nil
	case bytes.HasPrefix(trimmed, []byte("age-encryption.org/")), bytes.HasPrefix(trimmed, []byte(armor.Header)):
		return nil, errors.New("the identity file is passphrase-protected, which a secret engine cannot unlock")
	}
	var identities []age.Identity
	var native []string
	for line := range strings.SplitSeq(string(trimmed), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "AGE-PLUGIN-"):
			id, err := plugin.NewIdentity(line, unattended())
			if err != nil {
				return nil, fmt.Errorf("plugin identity: %w", err)
			}
			identities = append(identities, id)
		default:
			native = append(native, line)
		}
	}
	if len(native) > 0 {
		ids, err := age.ParseIdentities(strings.NewReader(strings.Join(native, "\n")))
		if err != nil {
			return nil, err
		}
		identities = append(identities, ids...)
	}
	if len(identities) == 0 {
		return nil, errors.New("no identities found")
	}
	return identities, nil
}

// unattended answers a plugin that wants a person: nobody is there. A plugin
// that needs only a touch (a hardware key's presence check) still works.
func unattended() *plugin.ClientUI {
	return &plugin.ClientUI{
		DisplayMessage: func(name, message string) error {
			slog.Info("age plugin message", "plugin", name, "message", message)
			return nil
		},
		RequestValue: func(name, prompt string, _ bool) (string, error) {
			return "", fmt.Errorf("age plugin %s asked for input (%q), which a secret engine cannot give", name, prompt)
		},
		Confirm: func(name, prompt, _, _ string) (bool, error) {
			return false, fmt.Errorf("age plugin %s asked for confirmation (%q), which a secret engine cannot give", name, prompt)
		},
		WaitTimer: func(name string) { slog.Info("age plugin is waiting, likely for a hardware key touch", "plugin", name) },
	}
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
