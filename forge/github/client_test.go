package main

import (
	"slices"
	"testing"

	"github.com/google/go-github/v78/github"

	"github.com/samcharles93/archipelago/sdk/forge"
)

func TestSplitInvitations(t *testing.T) {
	invitation := func(id int64, fullName string) *github.RepositoryInvitation {
		return &github.RepositoryInvitation{ID: new(id), Repo: &github.Repository{FullName: new(fullName)}}
	}
	ids := func(invites []*github.RepositoryInvitation) []int64 {
		out := make([]int64, 0, len(invites))
		for _, inv := range invites {
			out = append(out, inv.GetID())
		}
		return out
	}
	configured := []forge.RepoRef{{Owner: "samcharles93", Repo: "archie-core"}, {Owner: "acme", Repo: "widgets"}}
	for _, tc := range []struct {
		name         string
		invites      []*github.RepositoryInvitation
		repos        []forge.RepoRef
		wantAccepted []int64
		wantPending  []int64
	}{
		{
			name:         "invitation to a configured repository is accepted",
			invites:      []*github.RepositoryInvitation{invitation(1, "samcharles93/archie-core")},
			repos:        configured,
			wantAccepted: []int64{1},
		},
		{
			name:         "matching is case-insensitive",
			invites:      []*github.RepositoryInvitation{invitation(1, "SamCharles93/Archie-Core")},
			repos:        configured,
			wantAccepted: []int64{1},
		},
		{
			name:        "same owner, different repository stays pending",
			invites:     []*github.RepositoryInvitation{invitation(1, "samcharles93/other")},
			repos:       configured,
			wantPending: []int64{1},
		},
		{
			name:        "different owner, same repository name stays pending",
			invites:     []*github.RepositoryInvitation{invitation(1, "mallory/archie-core")},
			repos:       configured,
			wantPending: []int64{1},
		},
		{
			name:        "no configured repositories accepts nothing",
			invites:     []*github.RepositoryInvitation{invitation(1, "samcharles93/archie-core")},
			repos:       nil,
			wantPending: []int64{1},
		},
		{
			name:         "a mixed batch is partitioned",
			invites:      []*github.RepositoryInvitation{invitation(1, "acme/widgets"), invitation(2, "mallory/archie-core"), invitation(3, "samcharles93/archie-core")},
			repos:        configured,
			wantAccepted: []int64{1, 3},
			wantPending:  []int64{2},
		},
		{
			name:    "no invitations accepts nothing",
			invites: nil,
			repos:   configured,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accept, pending := splitInvitations(tc.invites, tc.repos)
			if got := ids(accept); !slices.Equal(got, tc.wantAccepted) {
				t.Errorf("accepted = %v, want %v", got, tc.wantAccepted)
			}
			if got := ids(pending); !slices.Equal(got, tc.wantPending) {
				t.Errorf("pending = %v, want %v", got, tc.wantPending)
			}
		})
	}
}
