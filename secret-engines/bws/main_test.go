package main

import (
	"context"
	"errors"
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
