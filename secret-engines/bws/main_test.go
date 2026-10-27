package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretenginev1 "github.com/samcharles93/archipelago/sdk/secretengine/v1"
)

// The engine used to cache its first list forever, so one failed read at
// startup made every later key unavailable until a restart.
func TestResolveRecovers(t *testing.T) {
	reads := []error{errors.New("bws unreachable"), nil}
	e := &engine{list: func(context.Context) (map[string]string, error) {
		err := reads[0]
		reads = reads[1:]
		if err != nil {
			return nil, err
		}
		return map[string]string{"token": "v1"}, nil
	}}
	resolve := func() (string, codes.Code) {
		r, err := e.Resolve(t.Context(), &secretenginev1.ResolveRequest{Key: "token"})
		return r.GetValue(), status.Code(err)
	}
	if _, code := resolve(); code != codes.Unavailable {
		t.Fatalf("first read failed: code = %v, want Unavailable", code)
	}
	if v, code := resolve(); code != codes.OK || v != "v1" {
		t.Fatalf("after recovery: %q, %v; want v1, OK", v, code)
	}
	// A stale cache serves what it holds when a refresh fails.
	e.fetched = time.Now().Add(-2 * cacheTTL)
	e.list = func(context.Context) (map[string]string, error) { return nil, errors.New("down") }
	if v, code := resolve(); code != codes.OK || v != "v1" {
		t.Fatalf("stale during outage: %q, %v; want v1, OK", v, code)
	}
}

func TestListWithFailure(t *testing.T) {
	const secret = "s3cr3t-value"
	tests := []struct {
		name   string
		stdout string
		stderr string
		err    error
		want   string
	}{
		{"stderr reason", "", "Error: invalid access token\n", errors.New("exit status 1"), "invalid access token"},
		{"no stderr", "", "", errors.New("exit status 1"), "exit status 1"},
		{"stdout never leaks", secret, "Error: forbidden", errors.New("exit status 1"), "forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := listWith(t.Context(), func(context.Context) ([]byte, []byte, error) {
				return []byte(tt.stdout), []byte(tt.stderr), tt.err
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("err leaks stdout: %v", err)
			}
		})
	}
}
