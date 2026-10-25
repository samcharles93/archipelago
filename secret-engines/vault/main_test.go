package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archipelago/sdk/secretengine"
)

func TestTarget(t *testing.T) {
	t.Setenv("VAULT_SECRET_PATH", "")
	for _, tc := range []struct {
		key, path, field string
		ok               bool
	}{
		{"secret/app/token", "secret/app", "token", true},
		{"-address=http://evil/x", "", "", false},
		{"secret/../sys/token", "", "", false},
		{"secret/app/", "", "", false},
		{"token", "", "", false},
	} {
		path, field, err := target(tc.key)
		if (err == nil) != tc.ok || path != tc.path || field != tc.field {
			t.Errorf("target(%q) = %q, %q, %v", tc.key, path, field, err)
		}
	}
}

// A missing secret or field is not found, on either KV version; the CLI this
// replaced reported both as the engine being unavailable.
func TestResolve(t *testing.T) {
	mounts := map[string]string{
		"/v1/sys/internal/ui/mounts/kv2/app":  `{"data":{"path":"kv2/","options":{"version":"2"}}}`,
		"/v1/sys/internal/ui/mounts/kv1/app":  `{"data":{"path":"kv1/","options":null}}`,
		"/v1/sys/internal/ui/mounts/kv2/gone": `{"data":{"path":"kv2/","options":{"version":"2"}}}`,
	}
	secrets := map[string]string{"/v1/kv2/data/app": `{"data":{"data":{"token":"v2"}}}`, "/v1/kv1/app": `{"data":{"token":"v1"}}`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := mounts[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := secrets[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[]}`))
	}))
	defer srv.Close()
	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_TOKEN", "t")
	for _, tc := range []struct {
		key, want string
		missing   bool
	}{
		{key: "kv2/app/token", want: "v2"},
		{key: "kv1/app/token", want: "v1"},
		{key: "kv2/app/absent", missing: true},
		{key: "kv2/gone/token", missing: true},
	} {
		got, err := resolve(t.Context(), tc.key)
		if tc.missing != errors.Is(err, secretengine.ErrNotFound) || got != tc.want {
			t.Errorf("resolve(%q) = %q, %v", tc.key, got, err)
		}
	}
}
