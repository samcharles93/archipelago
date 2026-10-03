package secretengine

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretenginev1 "github.com/samcharles93/archipelago/sdk/secretengine/v1"
)

// ErrNotFound is what a resolve function returns for a key the backend does
// not hold.
var ErrNotFound = errors.New("key not found")

type funcEngine struct {
	secretenginev1.UnimplementedSecretEngineServiceServer
	resolve func(ctx context.Context, key string) (string, error)
}

func (funcEngine) Configure(context.Context, *secretenginev1.ConfigureRequest) (*secretenginev1.ConfigureResponse, error) {
	return &secretenginev1.ConfigureResponse{}, nil
}

func (e funcEngine) Resolve(ctx context.Context, r *secretenginev1.ResolveRequest) (*secretenginev1.ResolveResponse, error) {
	value, err := e.resolve(ctx, r.GetKey())
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, status.Error(codes.NotFound, err.Error())
	case err != nil:
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &secretenginev1.ResolveResponse{Value: value}, nil
}

// ServeFunc runs an engine whose whole behaviour is one resolve function and
// that needs no settings.
func ServeFunc(resolve func(ctx context.Context, key string) (string, error)) {
	Serve(funcEngine{resolve: resolve})
}

const cliTimeout = 30 * time.Second

// Run executes a CLI with the extension's own environment and returns its
// trimmed stdout. Stderr is discarded so a CLI cannot leak a secret into logs.
func Run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
