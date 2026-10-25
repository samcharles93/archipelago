package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"

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

	// An unencrypted SSH key, which the age CLI this engine replaced accepted.
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, _ := ssh.NewPublicKey(pub)
	recipient, err := agessh.NewEd25519Recipient(sshPub)
	if err != nil {
		t.Fatal(err)
	}
	var sshSealed bytes.Buffer
	w, _ = age.Encrypt(&sshSealed, recipient)
	_, _ = w.Write([]byte("token=via-ssh\n"))
	_ = w.Close()
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGE_FILE", write("ssh.age", sshSealed.Bytes()))
	t.Setenv("AGE_IDENTITY", write("id_ed25519", pem.EncodeToMemory(block)))
	if got, err := resolve(t.Context(), "token"); err != nil || got != "via-ssh" {
		t.Fatalf("SSH identity: %q, %v", got, err)
	}

	other, _ := age.GenerateX25519Identity()
	t.Setenv("AGE_IDENTITY", write("other.txt", []byte(other.String()+"\n")))
	if _, err := resolve(t.Context(), "token"); err == nil || errors.Is(err, secretengine.ErrNotFound) {
		t.Fatalf("wrong identity: err = %v, want a decrypt failure, not a missing key", err)
	}
}
