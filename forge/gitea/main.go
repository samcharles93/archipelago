// Command gitea is the Gitea forge extension: issues, pull requests and
// reviews over the Gitea API.
package main

import (
	"log/slog"

	"github.com/samcharles93/archipelago/sdk/forge"
)

func main() {
	forge.Serve(func(cfg forge.Config) (forge.Forge, error) {
		return newGitea(cfg.Token, cfg.Host, slog.Default())
	})
}
