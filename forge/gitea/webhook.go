package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"code.gitea.io/sdk/gitea"

	"github.com/samcharles93/archipelago/sdk/forge"
)

// ParseWebhook decodes a delivery the host has authenticated: the receiver
// verifies the HMAC signature (X-Hub-Signature-256, which Gitea also sends, or
// X-Gitea-Signature) before this runs, so the body is never parsed before
// verification. Events the host does not act on (push, ping, ...) decode to
// no event.
func (c *GiteaClient) ParseWebhook(headers map[string]string, body []byte) (forge.WebhookEvent, error) {
	switch headers[http.CanonicalHeaderKey("X-Gitea-Event")] {
	case "issues":
		var p issuePayload
		if err := json.Unmarshal(body, &p); err != nil {
			return forge.WebhookEvent{}, fmt.Errorf("%w: %w", forge.ErrBadWebhook, err)
		}
		return forge.WebhookEvent{Issue: giteaIssueEvent(&p)}, nil
	case "pull_request_review", "pull_request_review_approved", "pull_request_review_rejected", "pull_request_review_comment":
		var p prReviewPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return forge.WebhookEvent{}, fmt.Errorf("%w: %w", forge.ErrBadWebhook, err)
		}
		if p.Review == nil {
			return forge.WebhookEvent{}, nil
		}
		return forge.WebhookEvent{Review: giteaReviewEvent(headers[http.CanonicalHeaderKey("X-Gitea-Event")], &p)}, nil
	}
	return forge.WebhookEvent{}, nil
}

// giteaIssueAction maps Gitea's issue actions onto the action vocabulary the
// host's dispatch predicate understands (GitHub's). Gitea calls label changes
// label_updated/label_cleared; older releases called them labeled/unlabeled.
func giteaIssueAction(action string) string {
	switch action {
	case "label_updated", "labeled":
		return "labeled"
	case "label_cleared", "unlabeled":
		return "unlabeled"
	default:
		return action
	}
}

func giteaIssueEvent(p *issuePayload) *forge.IssueEvent {
	if p.Issue == nil {
		return &forge.IssueEvent{Action: giteaIssueAction(p.Action)}
	}
	number := p.Issue.Number
	if number == 0 {
		number = p.Number
	}
	var labels []string
	for _, l := range p.Issue.Labels {
		if l.Name != "" {
			labels = append(labels, l.Name)
		}
	}
	seen := map[string]bool{}
	var assignees []string
	users := append([]webhookUser{}, p.Issue.Assignees...)
	if p.Issue.Assignee != nil {
		users = append(users, *p.Issue.Assignee)
	}
	for _, u := range users {
		if u.Login != "" && !seen[u.Login] {
			seen[u.Login] = true
			assignees = append(assignees, u.Login)
		}
	}
	e := &forge.IssueEvent{
		Action:      giteaIssueAction(p.Action),
		State:       p.Issue.State,
		PullRequest: p.Issue.PullRequest != nil,
		Number:      number,
		Title:       p.Issue.Title,
		Body:        p.Issue.Body,
		Labels:      labels,
		Assignees:   assignees,
	}
	if p.Repository != nil {
		e.Owner = p.Repository.Owner.Login
		e.Repo = p.Repository.Name
	}
	return e
}

// giteaReviewEvent maps a pull request review delivery onto the neutral review
// event. Recent Gitea versions send one event per verdict
// (pull_request_review_approved/rejected/comment) with a thin review object
// ({type, content}); the legacy pull_request_review event carries the full
// review ({id, user, state, body}), so both fields are read.
func giteaReviewEvent(event string, p *prReviewPayload) *forge.ReviewEvent {
	e := &forge.ReviewEvent{
		Action: "submitted", // a review delivery is always a submission
		State:  giteaReviewState(event, p.Review),
		Body:   p.Review.Content,
	}
	if e.Body == "" {
		e.Body = p.Review.Body
	}
	if p.Review.User != nil && p.Review.User.Login != "" {
		e.Author = p.Review.User.Login
	} else if p.Sender != nil {
		e.Author = p.Sender.Login
	}
	e.ReviewID = p.Review.ID
	number := p.Number
	if number == 0 && p.PullRequest != nil {
		number = p.PullRequest.Number
	}
	e.PRNumber = number
	if p.Repository != nil {
		e.Owner = p.Repository.Owner.Login
		e.Repo = p.Repository.Name
	}
	return e
}

func giteaReviewState(event string, r *reviewPayload) string {
	switch event {
	case "pull_request_review_approved":
		return forge.ReviewStateApproved
	case "pull_request_review_rejected":
		return forge.ReviewStateRequestedChanges
	case "pull_request_review_comment":
		return forge.ReviewStateCommented
	default: // legacy pull_request_review: the payload carries the state
		return normalizeGiteaReviewState(gitea.ReviewStateType(r.State))
	}
}

// ── webhook payload shapes (Gitea's api.* webhook payloads, trimmed to the
// fields archie reads) ──────────────────────────────────────────────────

type webhookUser struct {
	Login string `json:"login"`
}

type webhookLabel struct {
	Name string `json:"name"`
}

type webhookIssue struct {
	Number      int            `json:"number"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	State       string         `json:"state"`
	Labels      []webhookLabel `json:"labels"`
	Assignee    *webhookUser   `json:"assignee"`
	Assignees   []webhookUser  `json:"assignees"`
	PullRequest *struct{}      `json:"pull_request"`
}

type webhookRepository struct {
	Name  string      `json:"name"`
	Owner webhookUser `json:"owner"`
}

type issuePayload struct {
	Action     string             `json:"action"`
	Number     int                `json:"number"`
	Issue      *webhookIssue      `json:"issue"`
	Repository *webhookRepository `json:"repository"`
	Sender     *webhookUser       `json:"sender"`
}

type reviewPayload struct {
	ID      int64        `json:"id"`
	User    *webhookUser `json:"user"`
	State   string       `json:"state"`
	Body    string       `json:"body"`
	Type    string       `json:"type"`
	Content string       `json:"content"`
}

type prReviewPayload struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest *struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	Repository *webhookRepository `json:"repository"`
	Sender     *webhookUser       `json:"sender"`
	Review     *reviewPayload     `json:"review"`
}
