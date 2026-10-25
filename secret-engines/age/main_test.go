package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/samcharles93/archipelago/sdk/secretengine"
)

func TestResolve(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var sealed bytes.Buffer
	w, err := age.Encrypt(&sealed, identity.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("# tokens\ntoken = a=b\nempty=\n"))
	_ = w.Close()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Setenv("AGE_FILE", write("secrets.age", sealed.Bytes()))
	t.Setenv("AGE_IDENTITY", write("key.txt", []byte(identity.String()+"\n")))

	for _, tc := range []struct {
		key, want string
		missing   bool
	}{
		{key: "token", want: "a=b"},
		{key: "empty", missing: true},
		{key: "absent", missing: true},
	} {
		got, err := resolve(t.Context(), tc.key)
		if tc.missing != errors.Is(err, secretengine.ErrNotFound) || got != tc.want {
			t.Errorf("resolve(%q) = %q, %v", tc.key, got, err)
		}
	}

	other, _ := age.GenerateX25519Identity()
	t.Setenv("AGE_IDENTITY", write("other.txt", []byte(other.String()+"\n")))
	if _, err := resolve(t.Context(), "token"); err == nil || errors.Is(err, secretengine.ErrNotFound) {
		t.Fatalf("wrong identity: err = %v, want a decrypt failure, not a missing key", err)
	}
}
