package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
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

// A fake Gitea API captures the review object CreateReviewComments submits:
// exactly one review carrying the event, the body and the inline comments.
func TestCreateReviewCommentsSubmitsOneReview(t *testing.T) {
	type reviewComment struct {
		Path       string `json:"path"`
		Body       string `json:"body"`
		NewLineNum int64  `json:"new_position"`
	}
	type reviewRequest struct {
		State    string          `json:"event"`
		Body     string          `json:"body"`
		CommitID string          `json:"commit_id"`
		Comments []reviewComment `json:"comments"`
	}
	const headSHA = "deadbeefcafe"
	var got reviewRequest
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/version":
			_, _ = w.Write([]byte(`{"version":"1.23.0"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/o/r/pulls/6":
			_, _ = w.Write([]byte(`{"user":{"login":"alice"},"head":{"sha":"` + headSHA + `","ref":"feat"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
			_, _ = w.Write([]byte(`{"login":"archie"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/o/r/pulls/6/reviews":
			posts++
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode submitted review: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":9}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	f, err := newGitea("token", srv.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("newGitea: %v", err)
	}
	writer, ok := f.(forge.ReviewCommentWriter)
	if !ok {
		t.Fatal("the Gitea extension is not a ReviewCommentWriter")
	}
	for _, tc := range []struct {
		name         string
		review       forge.ReviewSubmission
		wantState    string
		wantBody     string
		wantComments []reviewComment
	}{
		{
			name: "comment event carries the body and the inline comments",
			review: forge.ReviewSubmission{
				Event:    forge.ReviewEventComment,
				Body:     "looks good overall",
				Comments: []forge.InlineReviewComment{{Path: "main.go", Line: 12, Body: "rename this"}},
			},
			wantState:    forge.ReviewEventComment,
			wantBody:     "looks good overall",
			wantComments: []reviewComment{{Path: "main.go", Body: "rename this", NewLineNum: 12}},
		},
		{
			name: "request changes lists the unanchored finding in the review body",
			review: forge.ReviewSubmission{
				Event: forge.ReviewEventRequestChanges,
				Body:  "Findings outside the diff:\n\n- `main.go`: blocking finding",
			},
			wantState: forge.ReviewEventRequestChanges,
			wantBody:  "Findings outside the diff:\n\n- `main.go`: blocking finding",
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
			if got.State != tc.wantState {
				t.Errorf("state = %q, want %q", got.State, tc.wantState)
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
