package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/samcharles93/archipelago/sdk/forge"

	"code.gitea.io/sdk/gitea"
)

// GiteaClient implements Forge against a Gitea instance.
type GiteaClient struct {
	host    string
	token   string
	version string
	http    *http.Client
	log     *slog.Logger

	labelMu  sync.Mutex
	labelIDs map[string]int64

	loginMu sync.Mutex
	login   string
}

// NewGitea creates a Gitea-backed Forge implementation.
func newGitea(token, host string, log *slog.Logger) (forge.Forge, error) {
	host = strings.TrimRight(host, "/")
	cli, err := gitea.NewClient(host, gitea.SetToken(token))
	if err != nil {
		return nil, fmt.Errorf("gitea client %s: %w", host, err)
	}
	version, _, err := cli.ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("read the Gitea version at %s: %w", host, err)
	}
	c := &GiteaClient{host: host, token: token, version: version, http: &http.Client{}, log: log}
	if _, err := c.newAPI(context.Background()); err != nil {
		return nil, fmt.Errorf("gitea client %s: %w", host, err)
	}
	return c, nil
}

// api is a client bound to ctx. The SDK reads its context from the client, so
// one shared client would cancel every caller's request with the first
// caller's context; the server version is known, so this costs no request.
func (c *GiteaClient) api(ctx context.Context) *gitea.Client {
	cli, err := c.newAPI(ctx)
	if err != nil {
		// newGitea built one with the same version, so this cannot fail.
		panic(fmt.Sprintf("gitea client: %v", err))
	}
	return cli
}

func (c *GiteaClient) newAPI(ctx context.Context) (*gitea.Client, error) {
	return gitea.NewClient(c.host, gitea.SetToken(c.token), gitea.SetHTTPClient(c.http),
		gitea.SetContext(ctx), gitea.SetGiteaVersion(c.version))
}

// notFound reports whether Gitea answered 404.
func notFound(resp *gitea.Response) bool {
	return resp != nil && resp.StatusCode == http.StatusNotFound
}

// ── Forge interface ──────────────────────────────────────────────────

// AcceptInvitations accepts pending repository invitations. Gitea doesn't
// have a direct equivalent to GitHub's user invitations API -- repository
// collaborators are added directly by the owner -- so there is nothing to
// accept and the allowlist is moot.
func (c *GiteaClient) AcceptInvitations(ctx context.Context, repos []forge.RepoRef) error {
	return nil
}

// AssignedIssues returns open issues assigned to the given user, excluding PRs.
func (c *GiteaClient) AssignedIssues(ctx context.Context, owner, repo, assignee string) ([]forge.Issue, error) {
	opts := gitea.ListIssueOption{
		ListOptions: gitea.ListOptions{PageSize: 50},
		State:       gitea.StateOpen,
		Type:        gitea.IssueTypeIssue,
		AssignedBy:  assignee,
	}
	var out []forge.Issue
	for {
		issues, resp, err := c.api(ctx).ListRepoIssues(owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list issues %s/%s: %w", owner, repo, err)
		}
		for _, is := range issues {
			if is.PullRequest == nil {
				out = append(out, forge.Issue{
					Number: int(is.Index),
					Title:  is.Title,
					Body:   is.Body,
					Labels: labelNamesGitea(is.Labels),
				})
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// IssuesWithLabel returns open issues matching the given label, excluding PRs.
func (c *GiteaClient) IssuesWithLabel(ctx context.Context, owner, repo, label string) ([]forge.Issue, error) {
	opts := gitea.ListIssueOption{
		ListOptions: gitea.ListOptions{PageSize: 50},
		State:       gitea.StateOpen,
		Type:        gitea.IssueTypeIssue,
		Labels:      []string{label},
	}
	var out []forge.Issue
	for {
		issues, resp, err := c.api(ctx).ListRepoIssues(owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list issues %s/%s: %w", owner, repo, err)
		}
		for _, is := range issues {
			if is.PullRequest == nil {
				out = append(out, forge.Issue{
					Number: int(is.Index),
					Title:  is.Title,
					Body:   is.Body,
					Labels: labelNamesGitea(is.Labels),
				})
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// Comment posts an issue comment and returns its id.
func (c *GiteaClient) Comment(ctx context.Context, owner, repo string, number int, body string) (int64, error) {
	cm, _, err := c.api(ctx).CreateIssueComment(owner, repo, int64(number), gitea.CreateIssueCommentOption{Body: body})
	if err != nil {
		return 0, fmt.Errorf("comment on %s/%s#%d: %w", owner, repo, number, err)
	}
	return cm.ID, nil
}

// CreatePR opens a pull request and returns its number.
func (c *GiteaClient) CreatePR(ctx context.Context, owner, repo, title, head, base, body string) (int, error) {
	pr, _, err := c.api(ctx).CreatePullRequest(owner, repo, gitea.CreatePullRequestOption{
		Title: title,
		Head:  head,
		Base:  base,
		Body:  body,
	})
	if err != nil {
		return 0, fmt.Errorf("create PR %s/%s: %w", owner, repo, err)
	}
	return int(pr.Index), nil
}

// PRState returns "open", "merged", or "closed" for a PR. A pull request
// Gitea no longer has (deleted with its issue) reads as closed.
func (c *GiteaClient) PRState(ctx context.Context, owner, repo string, number int) (string, error) {
	pr, resp, err := c.api(ctx).GetPullRequest(owner, repo, int64(number))
	if notFound(resp) {
		return "closed", nil
	}
	if err != nil {
		return "", fmt.Errorf("get pull request %s/%s#%d: %w", owner, repo, number, err)
	}
	if pr.HasMerged {
		return "merged", nil
	}
	return string(pr.State), nil
}

// GetPullRequest returns the forge-neutral metadata for an existing PR.
func (c *GiteaClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (forge.PullRequest, error) {
	pr, _, err := c.api(ctx).GetPullRequest(owner, repo, int64(number))
	if err != nil {
		return forge.PullRequest{}, fmt.Errorf("get pull request %s/%s#%d: %w", owner, repo, number, err)
	}
	// A merged/closed PR whose head branch was deleted returns nil head/base
	// from Gitea; dereferencing it would panic.
	if pr.Head == nil || pr.Base == nil {
		return forge.PullRequest{}, fmt.Errorf("pull request %s/%s#%d has a missing head or base ref", owner, repo, number)
	}
	state := string(pr.State)
	if pr.HasMerged {
		state = "merged"
	}
	return forge.PullRequest{
		Number:  int(pr.Index),
		Title:   pr.Title,
		Body:    pr.Body,
		HeadRef: pr.Head.Ref,
		BaseRef: pr.Base.Ref,
		HeadSHA: pr.Head.Sha,
		BaseSHA: pr.Base.Sha,
		State:   state,
	}, nil
}

// GetPullRequestDiff fetches the pull request's unified diff over the Gitea
// API, never a local git clone.
func (c *GiteaClient) GetPullRequestDiff(ctx context.Context, owner, repo string, number int) (string, error) {
	diff, _, err := c.api(ctx).GetPullRequestDiff(owner, repo, int64(number), gitea.PullRequestDiffOptions{})
	if err != nil {
		return "", fmt.Errorf("get pull request diff %s/%s#%d: %w", owner, repo, number, err)
	}
	return string(diff), nil
}

// GetRepoArchive fetches a gzipped tar archive of the repository at ref
// directly from Gitea's own API -- unlike GitHub, no separate unauthenticated
// fetch is needed.
func (c *GiteaClient) GetRepoArchive(ctx context.Context, owner, repo, ref string) (io.ReadCloser, error) {
	body, _, err := c.api(ctx).GetArchiveReader(owner, repo, ref, gitea.TarGZArchive)
	if err != nil {
		return nil, fmt.Errorf("get repo archive %s/%s@%s: %w", owner, repo, ref, err)
	}
	return body, nil
}

// ListReviews returns the reviews on a PR with ID > sinceID.
func (c *GiteaClient) ListReviews(ctx context.Context, owner, repo string, number int, sinceID int64) ([]forge.Review, error) {
	var out []forge.Review
	opts := gitea.ListPullReviewsOptions{}
	for {
		reviews, resp, err := c.api(ctx).ListPullReviews(owner, repo, int64(number), opts)
		if err != nil {
			return nil, fmt.Errorf("list reviews %s/%s#%d: %w", owner, repo, number, err)
		}
		for _, r := range reviews {
			if r.ID <= sinceID {
				continue
			}
			out = append(out, forge.Review{
				ID:          r.ID,
				Author:      giteaReviewAuthor(r.Reviewer),
				State:       normalizeGiteaReviewState(r.State),
				Body:        r.Body,
				SubmittedAt: r.Submitted,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// ListReviewComments returns the inline review comments on a PR with
// ID > sinceID. Gitea nests comments under reviews, so this lists each
// review's comments.
func (c *GiteaClient) ListReviewComments(ctx context.Context, owner, repo string, number int, sinceID int64) ([]forge.ReviewComment, error) {
	reviews, err := c.ListReviews(ctx, owner, repo, number, 0)
	if err != nil {
		return nil, err
	}
	var out []forge.ReviewComment
	for _, rv := range reviews {
		comments, _, err := c.api(ctx).ListPullReviewComments(owner, repo, int64(number), rv.ID)
		if err != nil {
			return nil, fmt.Errorf("list review comments %s/%s#%d review %d: %w", owner, repo, number, rv.ID, err)
		}
		for _, cm := range comments {
			if cm.ID <= sinceID {
				continue
			}
			// LineNum is the forge's uint64. A value past the int range is not a
			// line this client can name, and wrapping it would put a negative
			// line number in front of a reviewer.
			if cm.LineNum > math.MaxInt {
				return nil, fmt.Errorf("review comment %d on %s/%s#%d has line number %d, which does not fit an int", cm.ID, owner, repo, number, cm.LineNum)
			}
			out = append(out, forge.ReviewComment{
				ID:        cm.ID,
				ReviewID:  rv.ID,
				Author:    giteaReviewAuthor(cm.Reviewer),
				Body:      cm.Body,
				Path:      cm.Path,
				Line:      int(cm.LineNum),
				CreatedAt: cm.Created,
			})
		}
	}
	return out, nil
}

// ReplyToReview posts a reply to an existing review comment.
func (c *GiteaClient) ReplyToReview(ctx context.Context, owner, repo string, number int, commentID int64, body string) error {
	if _, _, err := c.api(ctx).CreatePullReviewCommentReply(owner, repo, int64(number), commentID, gitea.CreatePullReviewCommentReplyOptions{Body: body}); err != nil {
		return fmt.Errorf("reply to review comment %d on %s/%s#%d: %w", commentID, owner, repo, number, err)
	}
	return nil
}

// CreateReviewComments submits the review on the PR's head, refused if the
// head moved past reviewedHeadSHA.
func (c *GiteaClient) CreateReviewComments(ctx context.Context, owner, repo string, number int, reviewedHeadSHA string, review forge.ReviewSubmission) error {
	pr, _, err := c.api(ctx).GetPullRequest(owner, repo, int64(number))
	if err != nil {
		return fmt.Errorf("get pull request %s/%s#%d: %w", owner, repo, number, err)
	}
	if pr.Head == nil || pr.Head.Sha == "" {
		return fmt.Errorf("resolve head revision of %s/%s#%d: the forge reported no head sha", owner, repo, number)
	}
	head := pr.Head.Sha
	if err := forge.ReviewHeadDrift(owner, repo, number, head, reviewedHeadSHA); err != nil {
		return err
	}
	event := review.Event
	if event == forge.ReviewEventRequestChanges && pr.Poster != nil {
		self, err := c.selfLogin(ctx)
		if err != nil {
			return err
		}
		event = reviewEvent(event, pr.Poster.UserName, self)
	}
	reviewComments := make([]gitea.CreatePullReviewComment, 0, len(review.Comments))
	for _, cm := range review.Comments {
		reviewComments = append(reviewComments, gitea.CreatePullReviewComment{
			Path:       cm.Path,
			Body:       cm.Body,
			NewLineNum: int64(cm.Line),
		})
	}
	if _, _, err := c.api(ctx).CreatePullReview(owner, repo, int64(number), gitea.CreatePullReviewOptions{
		State:    gitea.ReviewStateType(event),
		Body:     review.Body,
		CommitID: head,
		Comments: reviewComments,
	}); err != nil {
		return fmt.Errorf("create review comments on %s/%s#%d: %w", owner, repo, number, err)
	}
	return nil
}

// reviewEvent is the event a review can post as: Gitea refuses
// REQUEST_CHANGES from the pull request's own author, so it comments instead.
func reviewEvent(event, author, self string) string {
	if event == forge.ReviewEventRequestChanges && author != "" && strings.EqualFold(author, self) {
		return forge.ReviewEventComment
	}
	return event
}

// selfLogin is the login the token acts as, read once.
func (c *GiteaClient) selfLogin(ctx context.Context) (string, error) {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.login != "" {
		return c.login, nil
	}
	user, _, err := c.api(ctx).GetMyUserInfo()
	if err != nil {
		return "", fmt.Errorf("read the token's user: %w", err)
	}
	c.login = user.UserName
	return c.login, nil
}

// giteaReviewAuthor returns a review/comment author's login, or "" when the
// author is absent (a team review, or a deleted user).
func giteaReviewAuthor(u *gitea.User) string {
	if u == nil {
		return ""
	}
	return u.UserName
}

// normalizeGiteaReviewState maps a Gitea review state onto the neutral
// ReviewState* constants. Pending and request-review reviews are not a
// decision and pass through lowercased so the caller can ignore them.
func normalizeGiteaReviewState(s gitea.ReviewStateType) string {
	switch s {
	case gitea.ReviewStateApproved:
		return forge.ReviewStateApproved
	case gitea.ReviewStateRequestChanges:
		return forge.ReviewStateRequestedChanges
	case gitea.ReviewStateComment:
		return forge.ReviewStateCommented
	default:
		return strings.ToLower(string(s))
	}
}

// CloseIssue closes an issue with an optional final comment.
func (c *GiteaClient) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	if comment != "" {
		if _, err := c.Comment(ctx, owner, repo, number, comment); err != nil {
			return err
		}
	}
	state := gitea.StateClosed
	if _, _, err := c.api(ctx).EditIssue(owner, repo, int64(number), gitea.EditIssueOption{State: &state}); err != nil {
		return fmt.Errorf("close issue %s/%s#%d: %w", owner, repo, number, err)
	}
	return nil
}

// ClosePR closes a pull request without merging, with an optional final
// comment.
func (c *GiteaClient) ClosePR(ctx context.Context, owner, repo string, number int, comment string) error {
	if comment != "" {
		if _, err := c.Comment(ctx, owner, repo, number, comment); err != nil {
			return err
		}
	}
	state := gitea.StateClosed
	if _, _, err := c.api(ctx).EditPullRequest(owner, repo, int64(number), gitea.EditPullRequestOption{State: &state}); err != nil {
		return fmt.Errorf("close pull request %s/%s#%d: %w", owner, repo, number, err)
	}
	return nil
}

// MergePR merges a pull request with the repository's default merge style.
func (c *GiteaClient) MergePR(ctx context.Context, owner, repo string, number int) error {
	api := c.api(ctx)
	r, _, err := api.GetRepo(owner, repo)
	if err != nil {
		return fmt.Errorf("get repository %s/%s: %w", owner, repo, err)
	}
	style := r.DefaultMergeStyle
	if style == "" {
		style = gitea.MergeStyleMerge
	}
	merged, _, err := api.MergePullRequest(owner, repo, int64(number), gitea.MergePullRequestOption{Style: style})
	if err != nil {
		return fmt.Errorf("merge pull request %s/%s#%d: %w", owner, repo, number, err)
	}
	if !merged {
		return fmt.Errorf("merge pull request %s/%s#%d: Gitea did not merge it", owner, repo, number)
	}
	return nil
}

// React adds an emoji reaction to an issue.
func (c *GiteaClient) React(ctx context.Context, owner, repo string, number int, reaction string) error {
	if _, _, err := c.api(ctx).PostIssueReaction(owner, repo, int64(number), reaction); err != nil {
		return fmt.Errorf("react to %s/%s#%d: %w", owner, repo, number, err)
	}
	return nil
}

// LinkBranch sets the issue's ref to the branch working on it.
func (c *GiteaClient) LinkBranch(ctx context.Context, owner, repo string, issueNumber int, branch string) error {
	if _, _, err := c.api(ctx).EditIssue(owner, repo, int64(issueNumber), gitea.EditIssueOption{Ref: &branch}); err != nil {
		return fmt.Errorf("link branch %s to %s/%s#%d: %w", branch, owner, repo, issueNumber, err)
	}
	return nil
}

// SetStateLabel makes label the issue's only state label.
func (c *GiteaClient) SetStateLabel(ctx context.Context, owner, repo string, number int, label string, knownLabels []string) {
	known := make(map[string]bool, len(knownLabels))
	for _, l := range knownLabels {
		known[l] = true
	}
	issue, _, err := c.api(ctx).GetIssue(owner, repo, int64(number))
	if err != nil {
		c.log.Warn("set state label: get issue failed", "issue", number, "err", err)
		return
	}
	// Remove known state labels (except the target).
	for _, l := range issue.Labels {
		if l.Name == label {
			label = "" // already present
		} else if known[l.Name] {
			if _, err := c.api(ctx).DeleteIssueLabel(owner, repo, int64(number), l.ID); err != nil {
				c.log.Warn("remove state label failed", "label", l.Name, "err", err)
			}
		}
	}
	if label == "" {
		return
	}
	labelID, err := c.labelID(ctx, owner, repo, label)
	if err != nil {
		c.log.Warn("resolve state label failed", "label", label, "err", err)
		return
	}
	if _, _, err := c.api(ctx).AddIssueLabels(owner, repo, int64(number), gitea.IssueLabelsOption{
		Labels: []int64{labelID},
	}); err != nil {
		c.log.Warn("add state label failed", "label", label, "err", err)
	}
}

// labelID returns a label's ID, creating the label when the repository has
// none by that name.
func (c *GiteaClient) labelID(ctx context.Context, owner, repo, name string) (int64, error) {
	key := owner + "/" + repo + "/" + name
	c.labelMu.Lock()
	defer c.labelMu.Unlock()
	if id, ok := c.labelIDs[key]; ok {
		return id, nil
	}
	api := c.api(ctx)
	opts := gitea.ListLabelsOptions{ListOptions: gitea.ListOptions{PageSize: 50}}
	var id int64
	for id == 0 {
		labels, resp, err := api.ListRepoLabels(owner, repo, opts)
		if err != nil {
			return 0, fmt.Errorf("list labels %s/%s: %w", owner, repo, err)
		}
		for _, l := range labels {
			if l.Name == name {
				id = l.ID
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	if id == 0 {
		color := giteaStateLabelColors[name]
		if color == "" {
			color = "#bfd4f2"
		}
		created, _, err := api.CreateLabel(owner, repo, gitea.CreateLabelOption{
			Name:        name,
			Color:       color,
			Description: "archie task state (managed automatically)",
		})
		if err != nil {
			return 0, fmt.Errorf("create label %s on %s/%s: %w", name, owner, repo, err)
		}
		id = created.ID
	}
	if c.labelIDs == nil {
		c.labelIDs = map[string]int64{}
	}
	c.labelIDs[key] = id
	return id, nil
}

// VerifyPush confirms the token can push to the repo.
func (c *GiteaClient) VerifyPush(ctx context.Context, owner, repo string) error {
	r, resp, err := c.api(ctx).GetRepo(owner, repo)
	if notFound(resp) {
		return fmt.Errorf("repository %s/%s not found, or the token cannot see it", owner, repo)
	}
	if err != nil {
		return fmt.Errorf("get repository %s/%s: %w", owner, repo, err)
	}
	if !r.Permissions.Push {
		return fmt.Errorf("no push permission on %s/%s", owner, repo)
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────

func labelNamesGitea(labels []*gitea.Label) []string {
	var names []string
	for _, l := range labels {
		names = append(names, l.Name)
	}
	return names
}

// Gitea state label colours matching the GitHub palette.
var giteaStateLabelColors = map[string]string{
	"archie:queued":  "#bfd4f2",
	"archie:working": "#1d76db",
	"archie:waiting": "#fbca04",
	"archie:pr":      "#0e8a16",
	"archie:parked":  "#d93f0b",
}
