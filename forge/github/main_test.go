package main

import (
	"net/http"
	"testing"

	"github.com/google/go-github/v78/github"

	"github.com/samcharles93/archipelago/sdk/forge"
)

func TestNormalizeReviewState(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"CHANGES_REQUESTED", forge.ReviewStateRequestedChanges}, // REST
		{"changes_requested", forge.ReviewStateRequestedChanges}, // webhook
		{"approved", forge.ReviewStateApproved},
		{"PENDING", "pending"},
	} {
		if got := normalizeReviewState(tc.in); got != tc.want {
			t.Errorf("normalizeReviewState(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestReviewEvent(t *testing.T) {
	for _, tc := range []struct{ name, event, author, self, want string }{
		{"own pull request comments", forge.ReviewEventRequestChanges, "archie-bot", "Archie-Bot", forge.ReviewEventComment},
		{"someone else's pull request requests changes", forge.ReviewEventRequestChanges, "alice", "archie-bot", forge.ReviewEventRequestChanges},
		{"a comment stays a comment", forge.ReviewEventComment, "archie-bot", "archie-bot", forge.ReviewEventComment},
	} {
		if got := reviewEvent(tc.event, tc.author, tc.self); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAPIError(t *testing.T) {
	refused := &github.ErrorResponse{
		Response: &http.Response{StatusCode: 422, Request: &http.Request{Method: "POST"}},
		Message:  "Unprocessable Entity",
		Errors:   []github.Error{{Message: "Review Can not request changes on your own pull request"}},
	}
	want := "create review on o/r#6: GitHub 422 Unprocessable Entity: Review Can not request changes on your own pull request"
	if got := apiError("create review on o/r#6", refused).Error(); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
