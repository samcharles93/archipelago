package main

import (
	"fmt"
	"net/http"

	"github.com/google/go-github/v78/github"

	"github.com/samcharles93/archipelago/sdk/forge"
)

// ParseWebhook decodes a delivery the host has authenticated. Events the host
// does not act on (ping, push) decode to no event.
func (c *GitHubClient) ParseWebhook(headers map[string]string, body []byte) (forge.WebhookEvent, error) {
	event, err := github.ParseWebHook(headers[http.CanonicalHeaderKey("X-GitHub-Event")], body)
	if err != nil {
		return forge.WebhookEvent{}, fmt.Errorf("%w: %w", forge.ErrBadWebhook, err)
	}
	switch e := event.(type) {
	case *github.IssuesEvent:
		return forge.WebhookEvent{Issue: issueEvent(e)}, nil
	case *github.PullRequestReviewEvent:
		review := e.GetReview()
		if review == nil {
			return forge.WebhookEvent{}, nil
		}
		return forge.WebhookEvent{Review: &forge.ReviewEvent{
			Action:   e.GetAction(),
			Owner:    e.GetRepo().GetOwner().GetLogin(),
			Repo:     e.GetRepo().GetName(),
			PRNumber: e.GetPullRequest().GetNumber(),
			ReviewID: review.GetID(),
			Author:   review.GetUser().GetLogin(),
			State:    review.GetState(),
			Body:     review.GetBody(),
		}}, nil
	case *github.PullRequestReviewCommentEvent:
		comment := e.GetComment()
		if comment == nil {
			return forge.WebhookEvent{}, nil
		}
		return forge.WebhookEvent{ReviewComment: &forge.ReviewCommentEvent{
			Action:    e.GetAction(),
			Owner:     e.GetRepo().GetOwner().GetLogin(),
			Repo:      e.GetRepo().GetName(),
			PRNumber:  e.GetPullRequest().GetNumber(),
			ReviewID:  comment.GetPullRequestReviewID(),
			CommentID: comment.GetID(),
			Author:    comment.GetUser().GetLogin(),
			Body:      comment.GetBody(),
			Path:      comment.GetPath(),
			Line:      comment.GetLine(),
		}}, nil
	}
	return forge.WebhookEvent{}, nil
}

func issueEvent(e *github.IssuesEvent) *forge.IssueEvent {
	issue := e.GetIssue()
	if issue == nil {
		return &forge.IssueEvent{Action: e.GetAction()}
	}
	var labels []string
	for _, l := range issue.Labels {
		if name := l.GetName(); name != "" {
			labels = append(labels, name)
		}
	}
	seen := map[string]bool{}
	var assignees []string
	for _, u := range append(append([]*github.User{}, issue.Assignees...), issue.Assignee, e.GetAssignee()) {
		if login := u.GetLogin(); login != "" && !seen[login] {
			seen[login] = true
			assignees = append(assignees, login)
		}
	}
	return &forge.IssueEvent{
		Action:      e.GetAction(),
		State:       issue.GetState(),
		PullRequest: issue.IsPullRequest(),
		Owner:       e.GetRepo().GetOwner().GetLogin(),
		Repo:        e.GetRepo().GetName(),
		Number:      issue.GetNumber(),
		Title:       issue.GetTitle(),
		Body:        issue.GetBody(),
		Labels:      labels,
		Assignees:   assignees,
	}
}
