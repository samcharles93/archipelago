package main

import "testing"

func TestTarget(t *testing.T) {
	t.Setenv("VAULT_SECRET_PATH", "")
	for _, tc := range []struct {
		key, path, field string
		ok               bool
	}{
		{"secret/app/token", "secret/app", "token", true},
		{"-address=http://evil/x", "", "", false},
		{"secret/app/", "", "", false},
		{"token", "", "", false},
	} {
		path, field, err := target(tc.key)
		if (err == nil) != tc.ok || path != tc.path || field != tc.field {
			t.Errorf("target(%q) = %q, %q, %v", tc.key, path, field, err)
		}
	}
}
