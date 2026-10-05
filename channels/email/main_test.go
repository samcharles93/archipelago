package main

import (
	"testing"

	"github.com/emersion/go-message/mail"
)

func TestAuthenticated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []string
		want    bool
	}{
		{"dmarc pass", []string{"mx.example.net; dmarc=pass header.from=a.com"}, true},
		{"aligned dkim pass", []string{"mx.example.net; dkim=pass header.d=a.com"}, true},
		{"dkim pass for another domain", []string{"mx.example.net; dkim=pass header.d=evil.com"}, false},
		{"forged by another authserv", []string{"mx.evil.com; dmarc=pass header.from=a.com"}, false},
		{"dmarc fail", []string{"mx.example.net; dmarc=fail header.from=a.com"}, false},
		{"no results", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var h mail.Header
			for _, v := range tc.results {
				h.Add("Authentication-Results", v)
			}
			if got := authenticated(h, "mx.example.net", "me@a.com"); got != tc.want {
				t.Fatalf("authenticated = %v, want %v", got, tc.want)
			}
		})
	}
}
