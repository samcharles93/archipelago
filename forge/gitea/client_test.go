package main

import (
	"testing"

	"github.com/samcharles93/archipelago/sdk/forge"
)

func TestReviewEvent(t *testing.T) {
	for _, tc := range []struct{ name, event, author, self, want string }{
		{"own pull request comments", forge.ReviewEventRequestChanges, "archie", "Archie", forge.ReviewEventComment},
		{"someone else's pull request requests changes", forge.ReviewEventRequestChanges, "alice", "archie", forge.ReviewEventRequestChanges},
		{"unknown author requests changes", forge.ReviewEventRequestChanges, "", "archie", forge.ReviewEventRequestChanges},
	} {
		if got := reviewEvent(tc.event, tc.author, tc.self); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
