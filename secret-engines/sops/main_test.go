package main

import (
	"errors"
	"testing"

	"github.com/samcharles93/archipelago/sdk/secretengine"
)

func TestLookup(t *testing.T) {
	plain := []byte(`{"token":"s3cret","port":5432,"nested":{"a":1},"empty":"","nil":null}`)
	for _, tc := range []struct {
		key, want string
		missing   bool
	}{
		{key: "token", want: "s3cret"},
		{key: "port", want: "5432"},
		{key: "nested", want: `{"a":1}`},
		{key: "empty", missing: true},
		{key: "nil", missing: true},
		{key: `"] --output x`, missing: true},
	} {
		got, err := lookup(plain, tc.key)
		if tc.missing != errors.Is(err, secretengine.ErrNotFound) || got != tc.want {
			t.Errorf("lookup(%q) = %q, %v", tc.key, got, err)
		}
	}
}
