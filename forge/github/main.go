// Command github is the GitHub forge extension: issues, pull requests and
// reviews over the GitHub API, and webhook decoding.
package main

import (
	"log/slog"
	"strings"

	"github.com/samcharles93/archipelago/sdk/forge"
)

func main() {
	forge.Serve(func(cfg forge.Config) (forge.Forge, error) {
		return newGitHub(cfg.Token, cfg.Host, slog.Default())
	})
}

// normalizeReviewState maps a GitHub review state onto the neutral states.
// Unknown states pass through lowercased.
func normalizeReviewState(s string) string {
	switch s {
	case "APPROVED":
		return forge.ReviewStateApproved
	case "CHANGES_REQUESTED":
		return forge.ReviewStateRequestedChanges
	case "COMMENTED":
		return forge.ReviewStateCommented
	case "DISMISSED":
		return forge.ReviewStateDismissed
	default:
		return strings.ToLower(s)
	}
}
