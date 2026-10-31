package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

// A fake GitHub API captures the review object CreateReviewComments submits:
// exactly one review carrying the event, the body and the inline comments.
func TestCreateReviewCommentsSubmitsOneReview(t *testing.T) {
	type draftComment struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Side string `json:"side"`
		Body string `json:"body"`
	}
	type reviewRequest struct {
		CommitID string         `json:"commit_id"`
		Body     string         `json:"body"`
		Event    string         `json:"event"`
		Comments []draftComment `json:"comments"`
	}
	const headSHA = "deadbeefcafe"
	var got reviewRequest
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/o/r/pulls/6":
			_, _ = w.Write([]byte(`{"head":{"sha":"` + headSHA + `"},"user":{"login":"alice"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/user":
			_, _ = w.Write([]byte(`{"login":"archie-bot"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/repos/o/r/pulls/6/reviews":
			posts++
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode submitted review: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"id":1}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	f, err := newGitHub("token", srv.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("newGitHub: %v", err)
	}
	writer, ok := f.(forge.ReviewCommentWriter)
	if !ok {
		t.Fatal("the GitHub extension is not a ReviewCommentWriter")
	}
	for _, tc := range []struct {
		name         string
		review       forge.ReviewSubmission
		wantEvent    string
		wantBody     string
		wantComments []draftComment
	}{
		{
			name: "comment event carries the body and the inline comments",
			review: forge.ReviewSubmission{
				Event:    forge.ReviewEventComment,
				Body:     "looks good overall",
				Comments: []forge.InlineReviewComment{{Path: "main.go", Line: 12, Body: "rename this"}},
			},
			wantEvent:    forge.ReviewEventComment,
			wantBody:     "looks good overall",
			wantComments: []draftComment{{Path: "main.go", Line: 12, Side: "RIGHT", Body: "rename this"}},
		},
		{
			name: "request changes lists the unanchored finding in the review body",
			review: forge.ReviewSubmission{
				Event: forge.ReviewEventRequestChanges,
				Body:  "Findings outside the diff:\n\n- `main.go`: blocking finding",
			},
			wantEvent: forge.ReviewEventRequestChanges,
			wantBody:  "Findings outside the diff:\n\n- `main.go`: blocking finding",
		},
		{
			name: "request changes without a body still posts one",
			review: forge.ReviewSubmission{
				Event:    forge.ReviewEventRequestChanges,
				Comments: []forge.InlineReviewComment{{Path: "main.go", Line: 3, Body: "blocking"}},
			},
			wantEvent:    forge.ReviewEventRequestChanges,
			wantBody:     "Changes requested.",
			wantComments: []draftComment{{Path: "main.go", Line: 3, Side: "RIGHT", Body: "blocking"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, posts = reviewRequest{}, 0
			if err := writer.CreateReviewComments(t.Context(), "o", "r", 6, headSHA, tc.review); err != nil {
				t.Fatalf("CreateReviewComments: %v", err)
			}
			if posts != 1 {
				t.Fatalf("submitted %d review objects, want exactly 1", posts)
			}
			if got.Event != tc.wantEvent {
				t.Errorf("event = %q, want %q", got.Event, tc.wantEvent)
			}
			if got.Body != tc.wantBody {
				t.Errorf("body = %q, want %q", got.Body, tc.wantBody)
			}
			if got.CommitID != headSHA {
				t.Errorf("commit_id = %q, want the reviewed head %q", got.CommitID, headSHA)
			}
			if !slices.Equal(got.Comments, tc.wantComments) {
				t.Errorf("comments = %+v, want %+v", got.Comments, tc.wantComments)
			}
		})
	}
}
