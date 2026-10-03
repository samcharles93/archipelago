// bws serves Bitwarden Secrets Manager as an archie-core secret engine. It
// wraps the bws CLI, which must be installed on the host, and reads
// BWS_ACCESS_TOKEN from the environment archie-core passes it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archipelago/sdk/secretengine"
	secretenginev1 "github.com/samcharles93/archipelago/sdk/secretengine/v1"
)

const listTimeout = 30 * time.Second

type engine struct {
	secretenginev1.UnimplementedSecretEngineServiceServer

	once    sync.Once
	secrets map[string]string
	err     error
}

func (*engine) Configure(context.Context, *secretenginev1.ConfigureRequest) (*secretenginev1.ConfigureResponse, error) {
	return &secretenginev1.ConfigureResponse{}, nil
}

func (e *engine) Resolve(ctx context.Context, r *secretenginev1.ResolveRequest) (*secretenginev1.ResolveResponse, error) {
	e.once.Do(func() { e.secrets, e.err = list(ctx) })
	if e.err != nil {
		return nil, status.Error(codes.Unavailable, e.err.Error())
	}
	value, ok := e.secrets[r.GetKey()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "bws secret %q not found", r.GetKey())
	}
	return &secretenginev1.ResolveResponse{Value: value}, nil
}

func list(ctx context.Context) (map[string]string, error) {
	command, err := find()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, "secret", "list", "--output", "json")
	cmd.Env = os.Environ()
	cmd.Stderr = io.Discard
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list bws secrets: %w", err)
	}
	var records []struct{ Key, Value string }
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("decode bws secrets: %w", err)
	}
	secrets := make(map[string]string, len(records))
	for _, record := range records {
		if record.Key != "" && record.Value != "" {
			secrets[record.Key] = record.Value
		}
	}
	return secrets, nil
}

func find() (string, error) {
	if command, err := exec.LookPath("bws"); err == nil {
		return command, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		command := filepath.Join(home, ".local", "bin", "bws")
		if info, statErr := os.Stat(command); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return command, nil
		}
	}
	return "", errors.New("bws executable not found")
}

func main() { secretengine.Serve(&engine{}) }
