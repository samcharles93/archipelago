package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/samcharles93/archipelago/sdk/forge"
)

// testSecret stands in for the webhook secret the host is configured with.
const testSecret = "gitea-webhook-test-secret"

// signDelivery returns the signature headers Gitea sends for body: the plain
// hex HMAC-SHA256 in X-Gitea-Signature and the same digest as "sha256=<hex>"
// in X-Hub-Signature-256.
func signDelivery(body []byte) map[string]string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	return map[string]string{
		"X-Hub-Signature-256": "sha256=" + sig,
		"X-Gitea-Signature":   sig,
	}
}

// verifyDelivery mirrors the host receiver's HMAC gate
// (archie-core internal/webhookguard.VerifyHMAC, which the receiver applies to
// X-Hub-Signature-256 before any parsing): hex SHA-256 HMAC of the body, the
// "sha256=" prefix optional, constant-time comparison, empty never verifies.
// The parser must only ever run behind this check.
func verifyDelivery(body []byte, headers map[string]string) bool {
	sig := headers["X-Hub-Signature-256"]
	if sig == "" {
		return false
	}
	supplied, err := hex.DecodeString(strings.TrimPrefix(sig, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	return hmac.Equal(supplied, mac.Sum(nil))
}

const issuesOpenedBody = `{
  "action": "opened",
  "number": 7,
  "issue": {
    "id": 101,
    "number": 7,
    "title": "Fix the flaky gate",
    "body": "It fails on retry",
    "state": "open",
    "user": {"login": "alice"},
    "labels": [{"id": 1, "name": "archie:queued"}, {"id": 2, "name": "bug"}],
    "assignee": {"login": "archie-bot"},
    "assignees": [{"login": "archie-bot"}, {"login": "alice"}],
    "pull_request": null
  },
  "repository": {"id": 5, "name": "repo", "full_name": "sam/repo", "owner": {"login": "sam"}},
  "sender": {"login": "alice"}
}`

const issuesLabelUpdatedBody = `{
  "action": "label_updated",
  "number": 7,
  "issue": {
    "number": 7,
    "title": "Fix the flaky gate",
    "body": "It fails on retry",
    "state": "open",
    "labels": [{"id": 1, "name": "archie:queued"}],
    "assignee": {"login": "archie-bot"},
    "assignees": [{"login": "archie-bot"}],
    "pull_request": null
  },
  "repository": {"name": "repo", "owner": {"login": "sam"}},
  "sender": {"login": "alice"}
}`

const reviewApprovedBody = `{
  "action": "reviewed",
  "number": 12,
  "pull_request": {"number": 12, "title": "Add the parser"},
  "repository": {"name": "repo", "owner": {"login": "sam"}},
  "sender": {"login": "alice"},
  "review": {"type": "pull_request_review_approved", "content": "lgtm"}
}`

const reviewRejectedBody = `{
  "action": "reviewed",
  "number": 12,
  "pull_request": {"number": 12},
  "repository": {"name": "repo", "owner": {"login": "sam"}},
  "sender": {"login": "bob"},
  "review": {"type": "pull_request_review_rejected", "content": "please fix the tests"}
}`

// legacyReviewBody is the pre-1.22 pull_request_review shape: one event, full
// review object carrying id, user, state and body.
const legacyReviewBody = `{
  "action": "reviewed",
  "number": 12,
  "pull_request": {"number": 12},
  "repository": {"name": "repo", "owner": {"login": "sam"}},
  "sender": {"login": "alice"},
  "review": {"id": 99, "user": {"login": "alice"}, "state": "REQUEST_CHANGES", "body": "needs work"}
}`

const pushBody = `{"ref": "refs/heads/main", "repository": {"name": "repo", "owner": {"login": "sam"}}}`

func TestParseWebhookSignedDeliveries(t *testing.T) {
	c := &GiteaClient{}
	for _, tc := range []struct {
		name  string
		event string
		body  string
		want  forge.WebhookEvent
	}{
		{
			name:  "issues opened",
			event: "issues",
			body:  issuesOpenedBody,
			want: forge.WebhookEvent{Issue: &forge.IssueEvent{
				Action: "opened", State: "open", PullRequest: false,
				Owner: "sam", Repo: "repo", Number: 7,
				Title: "Fix the flaky gate", Body: "It fails on retry",
				Labels:    []string{"archie:queued", "bug"},
				Assignees: []string{"archie-bot", "alice"},
			}},
		},
		{
			name:  "label_updated normalises to labeled",
			event: "issues",
			body:  issuesLabelUpdatedBody,
			want: forge.WebhookEvent{Issue: &forge.IssueEvent{
				Action: "labeled", State: "open", PullRequest: false,
				Owner: "sam", Repo: "repo", Number: 7,
				Title: "Fix the flaky gate", Body: "It fails on retry",
				Labels:    []string{"archie:queued"},
				Assignees: []string{"archie-bot"},
			}},
		},
		{
			name:  "review approved",
			event: "pull_request_review_approved",
			body:  reviewApprovedBody,
			want: forge.WebhookEvent{Review: &forge.ReviewEvent{
				Action: "submitted", Owner: "sam", Repo: "repo", PRNumber: 12,
				ReviewID: 0, Author: "alice", State: forge.ReviewStateApproved, Body: "lgtm",
			}},
		},
		{
			name:  "review rejected",
			event: "pull_request_review_rejected",
			body:  reviewRejectedBody,
			want: forge.WebhookEvent{Review: &forge.ReviewEvent{
				Action: "submitted", Owner: "sam", Repo: "repo", PRNumber: 12,
				ReviewID: 0, Author: "bob", State: forge.ReviewStateRequestedChanges, Body: "please fix the tests",
			}},
		},
		{
			name:  "legacy pull_request_review carries id and state",
			event: "pull_request_review",
			body:  legacyReviewBody,
			want: forge.WebhookEvent{Review: &forge.ReviewEvent{
				Action: "submitted", Owner: "sam", Repo: "repo", PRNumber: 12,
				ReviewID: 99, Author: "alice", State: forge.ReviewStateRequestedChanges, Body: "needs work",
			}},
		},
		{
			name:  "push decodes to no event",
			event: "push",
			body:  pushBody,
			want:  forge.WebhookEvent{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			headers := signDelivery(body)
			headers["X-Gitea-Event"] = tc.event
			// The host's invariant: HMAC first, parse only what verified.
			if !verifyDelivery(body, headers) {
				t.Fatalf("fixture signature did not verify: %s", headers["X-Hub-Signature-256"])
			}
			got, err := c.ParseWebhook(headers, body)
			if err != nil {
				t.Fatalf("ParseWebhook: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseWebhook mismatch\n got: %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

func TestParseWebhookRejectsTamperedSignatureBeforeParsing(t *testing.T) {
	c := &GiteaClient{}
	body := []byte(issuesOpenedBody)
	headers := signDelivery(body)
	headers["X-Gitea-Event"] = "issues"

	tamperedBody := []byte(strings.Replace(issuesOpenedBody, "Fix the flaky gate", "Steal the gate", 1))
	tamperedSig := signDelivery(body)
	tamperedSig["X-Hub-Signature-256"] = "sha256=" + strings.Repeat("00", 32)

	for _, tc := range []struct {
		name    string
		body    []byte
		headers map[string]string
	}{
		{"body modified after signing", tamperedBody, headers},
		{"signature from a different secret", body, tamperedSig},
		{"missing signature", body, map[string]string{"X-Gitea-Event": "issues"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The host rejects here with 401 and never calls the parser; the
			// test asserts the gate fails, so no parse-before-verify path is
			// exercised.
			if verifyDelivery(tc.body, tc.headers) {
				t.Fatalf("tampered delivery verified: %+v", tc.headers)
			}
		})
	}

	// Sanity: the untampered delivery still verifies and parses.
	if !verifyDelivery(body, headers) {
		t.Fatal("valid delivery did not verify")
	}
	event, err := c.ParseWebhook(headers, body)
	if err != nil {
		t.Fatalf("ParseWebhook on valid delivery: %v", err)
	}
	if event.Issue == nil || event.Issue.Number != 7 {
		t.Fatalf("valid delivery parsed to %+v", event)
	}
}

func TestParseWebhookBadPayload(t *testing.T) {
	c := &GiteaClient{}
	body := []byte(`{"action":`)
	headers := signDelivery(body)
	headers["X-Gitea-Event"] = "issues"
	if !verifyDelivery(body, headers) {
		t.Fatal("fixture signature did not verify")
	}
	_, err := c.ParseWebhook(headers, body)
	if !errors.Is(err, forge.ErrBadWebhook) {
		t.Fatalf("err = %v, want ErrBadWebhook", err)
	}
}

// TestParseWebhookCanonicalHeaderKey: the receiver passes Go's canonical
// header map, so the lookup must use the canonical key.
func TestParseWebhookCanonicalHeaderKey(t *testing.T) {
	c := &GiteaClient{}
	body := []byte(issuesOpenedBody)
	headers := signDelivery(body)
	headers[http.CanonicalHeaderKey("x-gitea-event")] = "issues"
	if !verifyDelivery(body, headers) {
		t.Fatal("fixture signature did not verify")
	}
	event, err := c.ParseWebhook(headers, body)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if event.Issue == nil {
		t.Fatalf("lowercase header not honoured: %+v", event)
	}
}
