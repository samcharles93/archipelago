package forge

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	forgev1 "github.com/samcharles93/archipelago/sdk/forge/v1"
)

// archiveChunk bounds one message of a streamed archive.
const archiveChunk = 256 << 10

// server adapts a Forge to the gRPC surface. Optional capabilities the Forge
// lacks answer UNIMPLEMENTED.
type server struct {
	forgev1.UnimplementedForgeServiceServer
	build func(Config) (Forge, error)

	mu sync.Mutex
	f  atomic.Pointer[Forge]
}

func (s *server) forge() (Forge, error) {
	f := s.f.Load()
	if f == nil {
		return nil, status.Error(codes.FailedPrecondition, "forge is not configured")
	}
	return *f, nil
}

func (s *server) Configure(_ context.Context, r *forgev1.ConfigureRequest) (*forgev1.ConfigureResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.build(Config{Host: r.GetHost(), Token: r.GetToken(), Settings: r.GetSettings()})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	s.f.Store(&f)
	return &forgev1.ConfigureResponse{}, nil
}

func capability[T any](s *server) (T, error) {
	var zero T
	f, err := s.forge()
	if err != nil {
		return zero, err
	}
	c, ok := f.(T)
	if !ok {
		return zero, status.Error(codes.Unimplemented, "not supported by this forge")
	}
	return c, nil
}

func fail(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Unknown, err.Error())
}

func issues(in []Issue) []*forgev1.Issue {
	out := make([]*forgev1.Issue, 0, len(in))
	for _, i := range in {
		out = append(out, &forgev1.Issue{Number: int32(i.Number), Title: i.Title, Body: i.Body, Labels: i.Labels}) //nolint:gosec // issue numbers fit int32
	}
	return out
}

func (s *server) AssignedIssues(ctx context.Context, r *forgev1.AssignedIssuesRequest) (*forgev1.AssignedIssuesResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	out, err := f.AssignedIssues(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), r.GetAssignee())
	return &forgev1.AssignedIssuesResponse{Issues: issues(out)}, fail(err)
}

func (s *server) IssuesWithLabel(ctx context.Context, r *forgev1.IssuesWithLabelRequest) (*forgev1.IssuesWithLabelResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	out, err := f.IssuesWithLabel(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), r.GetLabel())
	return &forgev1.IssuesWithLabelResponse{Issues: issues(out)}, fail(err)
}

func (s *server) Comment(ctx context.Context, r *forgev1.CommentRequest) (*forgev1.CommentResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	id, err := f.Comment(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), int(r.GetNumber()), r.GetBody())
	return &forgev1.CommentResponse{Id: id}, fail(err)
}

func (s *server) CloseIssue(ctx context.Context, r *forgev1.CloseIssueRequest) (*forgev1.CloseIssueResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	return &forgev1.CloseIssueResponse{}, fail(f.CloseIssue(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), int(r.GetNumber()), r.GetComment()))
}

func (s *server) React(ctx context.Context, r *forgev1.ReactRequest) (*forgev1.ReactResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	return &forgev1.ReactResponse{}, fail(f.React(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), int(r.GetNumber()), r.GetReaction()))
}

func (s *server) SetStateLabel(ctx context.Context, r *forgev1.SetStateLabelRequest) (*forgev1.SetStateLabelResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	f.SetStateLabel(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), int(r.GetNumber()), r.GetLabel(), r.GetKnownLabels())
	return &forgev1.SetStateLabelResponse{}, nil
}

func (s *server) CreatePR(ctx context.Context, r *forgev1.CreatePRRequest) (*forgev1.CreatePRResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	n, err := f.CreatePR(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), r.GetTitle(), r.GetHead(), r.GetBase(), r.GetBody())
	return &forgev1.CreatePRResponse{Number: int32(n)}, fail(err) //nolint:gosec // issue numbers fit int32
}

func (s *server) PRState(ctx context.Context, r *forgev1.PRStateRequest) (*forgev1.PRStateResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	state, err := f.PRState(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), int(r.GetNumber()))
	return &forgev1.PRStateResponse{State: state}, fail(err)
}

func (s *server) ClosePR(ctx context.Context, r *forgev1.ClosePRRequest) (*forgev1.ClosePRResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	return &forgev1.ClosePRResponse{}, fail(f.ClosePR(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), int(r.GetNumber()), r.GetComment()))
}

func (s *server) GetPullRequest(ctx context.Context, r *forgev1.GetPullRequestRequest) (*forgev1.GetPullRequestResponse, error) {
	c, err := capability[PullRequestReader](s)
	if err != nil {
		return nil, err
	}
	pr, err := c.GetPullRequest(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), int(r.GetNumber()))
	if err != nil {
		return nil, fail(err)
	}
	return &forgev1.GetPullRequestResponse{PullRequest: &forgev1.PullRequest{
		Number: int32(pr.Number), Title: pr.Title, Body: pr.Body, HeadRef: pr.HeadRef, BaseRef: pr.BaseRef, //nolint:gosec // issue numbers fit int32
		HeadSha: pr.HeadSHA, BaseSha: pr.BaseSHA, State: pr.State,
	}}, nil
}

func (s *server) GetPullRequestDiff(ctx context.Context, r *forgev1.GetPullRequestDiffRequest) (*forgev1.GetPullRequestDiffResponse, error) {
	c, err := capability[PullRequestDiffReader](s)
	if err != nil {
		return nil, err
	}
	diff, err := c.GetPullRequestDiff(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), int(r.GetNumber()))
	return &forgev1.GetPullRequestDiffResponse{Diff: diff}, fail(err)
}

func (s *server) ListReviews(ctx context.Context, r *forgev1.ListReviewsRequest) (*forgev1.ListReviewsResponse, error) {
	c, err := capability[ReviewReader](s)
	if err != nil {
		return nil, err
	}
	pr := r.GetPullRequest()
	reviews, err := c.ListReviews(ctx, pr.GetRepo().GetOwner(), pr.GetRepo().GetRepo(), int(pr.GetNumber()), r.GetSinceId())
	if err != nil {
		return nil, fail(err)
	}
	out := make([]*forgev1.Review, 0, len(reviews))
	for _, rv := range reviews {
		out = append(out, &forgev1.Review{Id: rv.ID, Author: rv.Author, State: rv.State, Body: rv.Body, SubmittedAt: timestamppb.New(rv.SubmittedAt)})
	}
	return &forgev1.ListReviewsResponse{Reviews: out}, nil
}

func (s *server) ListReviewComments(ctx context.Context, r *forgev1.ListReviewCommentsRequest) (*forgev1.ListReviewCommentsResponse, error) {
	c, err := capability[ReviewReader](s)
	if err != nil {
		return nil, err
	}
	pr := r.GetPullRequest()
	comments, err := c.ListReviewComments(ctx, pr.GetRepo().GetOwner(), pr.GetRepo().GetRepo(), int(pr.GetNumber()), r.GetSinceId())
	if err != nil {
		return nil, fail(err)
	}
	out := make([]*forgev1.ReviewComment, 0, len(comments))
	for _, cm := range comments {
		out = append(out, &forgev1.ReviewComment{
			Id: cm.ID, ReviewId: cm.ReviewID, Author: cm.Author, Body: cm.Body, Path: cm.Path,
			Line: int32(cm.Line), InReplyTo: cm.InReplyTo, CreatedAt: timestamppb.New(cm.CreatedAt), //nolint:gosec // line numbers fit int32
		})
	}
	return &forgev1.ListReviewCommentsResponse{Comments: out}, nil
}

func (s *server) ReplyToReview(ctx context.Context, r *forgev1.ReplyToReviewRequest) (*forgev1.ReplyToReviewResponse, error) {
	c, err := capability[ReviewReader](s)
	if err != nil {
		return nil, err
	}
	pr := r.GetPullRequest()
	return &forgev1.ReplyToReviewResponse{}, fail(c.ReplyToReview(ctx, pr.GetRepo().GetOwner(), pr.GetRepo().GetRepo(), int(pr.GetNumber()), r.GetCommentId(), r.GetBody()))
}

func (s *server) CreateReviewComments(ctx context.Context, r *forgev1.CreateReviewCommentsRequest) (*forgev1.CreateReviewCommentsResponse, error) {
	c, err := capability[ReviewCommentWriter](s)
	if err != nil {
		return nil, err
	}
	pr := r.GetPullRequest()
	comments := make([]InlineReviewComment, 0, len(r.GetComments()))
	for _, cm := range r.GetComments() {
		comments = append(comments, InlineReviewComment{Path: cm.GetPath(), Line: int(cm.GetLine()), Body: cm.GetBody()})
	}
	return &forgev1.CreateReviewCommentsResponse{}, fail(c.CreateReviewComments(ctx, pr.GetRepo().GetOwner(), pr.GetRepo().GetRepo(), int(pr.GetNumber()), r.GetReviewedHeadSha(), comments))
}

func (s *server) AcceptInvitations(ctx context.Context, _ *forgev1.AcceptInvitationsRequest) (*forgev1.AcceptInvitationsResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	return &forgev1.AcceptInvitationsResponse{}, fail(f.AcceptInvitations(ctx))
}

func (s *server) VerifyPush(ctx context.Context, r *forgev1.VerifyPushRequest) (*forgev1.VerifyPushResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	return &forgev1.VerifyPushResponse{}, fail(f.VerifyPush(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo()))
}

func (s *server) LinkBranch(ctx context.Context, r *forgev1.LinkBranchRequest) (*forgev1.LinkBranchResponse, error) {
	f, err := s.forge()
	if err != nil {
		return nil, err
	}
	return &forgev1.LinkBranchResponse{}, fail(f.LinkBranch(ctx, r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), int(r.GetIssueNumber()), r.GetBranch()))
}

func (s *server) GetRepoArchive(r *forgev1.GetRepoArchiveRequest, stream forgev1.ForgeService_GetRepoArchiveServer) error {
	c, err := capability[RepoArchiveReader](s)
	if err != nil {
		return err
	}
	body, err := c.GetRepoArchive(stream.Context(), r.GetRepo().GetOwner(), r.GetRepo().GetRepo(), r.GetRef())
	if err != nil {
		return fail(err)
	}
	defer func() { _ = body.Close() }()
	buf := make([]byte, archiveChunk)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if serr := stream.Send(&forgev1.GetRepoArchiveResponse{Data: buf[:n]}); serr != nil {
				return serr
			}
		}
		if errors.Is(rerr, io.EOF) {
			return nil
		}
		if rerr != nil {
			return fail(rerr)
		}
	}
}

func (s *server) ParseWebhook(_ context.Context, r *forgev1.ParseWebhookRequest) (*forgev1.ParseWebhookResponse, error) {
	c, err := capability[WebhookParser](s)
	if err != nil {
		return nil, err
	}
	event, err := c.ParseWebhook(r.GetHeaders(), r.GetBody())
	if errors.Is(err, ErrBadWebhook) {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err != nil {
		return nil, fail(err)
	}
	repo := func(owner, name string) *forgev1.RepoRef { return &forgev1.RepoRef{Owner: owner, Repo: name} }
	switch {
	case event.Issue != nil:
		i := event.Issue
		return &forgev1.ParseWebhookResponse{Event: &forgev1.ParseWebhookResponse_Issue{Issue: &forgev1.IssueEvent{
			Action: i.Action, State: i.State, IsPullRequest: i.PullRequest, Repo: repo(i.Owner, i.Repo), Number: int32(i.Number), //nolint:gosec // issue numbers fit int32
			Title: i.Title, Body: i.Body, Labels: i.Labels, Assignees: i.Assignees,
		}}}, nil
	case event.Review != nil:
		v := event.Review
		return &forgev1.ParseWebhookResponse{Event: &forgev1.ParseWebhookResponse_Review{Review: &forgev1.ReviewEvent{
			Action: v.Action, Repo: repo(v.Owner, v.Repo), PrNumber: int32(v.PRNumber), ReviewId: v.ReviewID, //nolint:gosec // issue numbers fit int32
			Author: v.Author, State: v.State, Body: v.Body,
		}}}, nil
	case event.ReviewComment != nil:
		c := event.ReviewComment
		return &forgev1.ParseWebhookResponse{Event: &forgev1.ParseWebhookResponse_ReviewComment{ReviewComment: &forgev1.ReviewCommentEvent{
			Action: c.Action, Repo: repo(c.Owner, c.Repo), PrNumber: int32(c.PRNumber), ReviewId: c.ReviewID, CommentId: c.CommentID, //nolint:gosec // issue numbers fit int32
			Author: c.Author, Body: c.Body, Path: c.Path, Line: int32(c.Line), //nolint:gosec // line numbers fit int32
		}}}, nil
	}
	return &forgev1.ParseWebhookResponse{}, nil
}
