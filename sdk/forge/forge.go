package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Config is what the host hands a forge extension once, after launch.
type Config struct {
	Host     string
	Token    string
	Settings map[string]string
}

// Issue is an issue (never a pull request).
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
}

// Forge is the capability every forge extension has.
type Forge interface {
	AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]Issue, error)
	IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]Issue, error)
	Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error)
	CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error
	React(ctx context.Context, owner, repo string, number int, reaction string) error
	// SetStateLabel makes label the issue's only state label, removing any
	// other label in knownLabels first. An empty label clears them.
	SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string)
	CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error)
	// PRState is "open", "merged" or "closed".
	PRState(ctx context.Context, owner, repo string, number int) (string, error)
	// ClosePR closes a pull request without merging, with an optional final
	// comment.
	ClosePR(ctx context.Context, owner, repo string, number int, comment string) error
	AcceptInvitations(ctx context.Context) error
	VerifyPush(ctx context.Context, owner, repo string) error
	LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error
}

// PullRequest summarises an existing pull request.
type PullRequest struct {
	Number  int
	Title   string
	Body    string
	HeadRef string
	BaseRef string
	HeadSHA string
	BaseSHA string
	State   string
}

// PullRequestReader, PullRequestDiffReader, RepoArchiveReader, ReviewReader,
// ReviewCommentWriter and WebhookParser are optional: an extension implements
// the ones its forge supports and the host sees the rest as unsupported.
type PullRequestReader interface {
	GetPullRequest(ctx context.Context, owner, repo string, number int) (PullRequest, error)
}

type PullRequestDiffReader interface {
	GetPullRequestDiff(ctx context.Context, owner, repo string, number int) (string, error)
}

type RepoArchiveReader interface {
	GetRepoArchive(ctx context.Context, owner, repo, ref string) (io.ReadCloser, error)
}

// Review states, normalised across forges.
const (
	ReviewStateApproved         = "approved"
	ReviewStateRequestedChanges = "requested_changes"
	ReviewStateCommented        = "commented"
	ReviewStateDismissed        = "dismissed"
)

type Review struct {
	ID          int64
	Author      string
	State       string
	Body        string
	SubmittedAt time.Time
}

type ReviewComment struct {
	ID        int64
	ReviewID  int64
	Author    string
	Body      string
	Path      string
	Line      int
	InReplyTo int64
	CreatedAt time.Time
}

type ReviewReader interface {
	ListReviews(ctx context.Context, owner, repo string, number int, sinceID int64) ([]Review, error)
	ListReviewComments(ctx context.Context, owner, repo string, number int, sinceID int64) ([]ReviewComment, error)
	ReplyToReview(ctx context.Context, owner, repo string, number int, commentID int64, body string) error
}

type InlineReviewComment struct {
	Path string
	Line int
	Body string
}

type ReviewCommentWriter interface {
	CreateReviewComments(ctx context.Context, owner, repo string, number int, reviewedHeadSHA string, comments []InlineReviewComment) error
}

// WebhookEvent is a decoded delivery; at most one field is set.
type WebhookEvent struct {
	Issue         *IssueEvent
	Review        *ReviewEvent
	ReviewComment *ReviewCommentEvent
}

type IssueEvent struct {
	Action      string
	State       string
	PullRequest bool
	Owner, Repo string
	Number      int
	Title, Body string
	Labels      []string
	Assignees   []string
}

type ReviewEvent struct {
	Action      string
	Owner, Repo string
	PRNumber    int
	ReviewID    int64
	Author      string
	State       string
	Body        string
}

type ReviewCommentEvent struct {
	Action      string
	Owner, Repo string
	PRNumber    int
	ReviewID    int64
	CommentID   int64
	Author      string
	Body        string
	Path        string
	Line        int
}

// ErrBadWebhook marks a body the forge could not decode.
var ErrBadWebhook = errors.New("undecodable webhook payload")

// WebhookParser decodes a delivery the host has already authenticated.
type WebhookParser interface {
	ParseWebhook(headers map[string]string, body []byte) (WebhookEvent, error)
}

// ReviewHeadDrift is an error when head is no longer reviewedHeadSHA; an empty
// reviewedHeadSHA skips the check.
func ReviewHeadDrift(owner, repo string, number int, head, reviewedHeadSHA string) error {
	if reviewedHeadSHA == "" || strings.EqualFold(head, reviewedHeadSHA) {
		return nil
	}
	return fmt.Errorf(
		"pull request %s/%s#%d has moved off the reviewed revision (%s): head is now %s, so the line-anchored comments would land on lines they were not measured against",
		owner, repo, number, reviewedHeadSHA, head,
	)
}
