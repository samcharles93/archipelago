package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v78/github"
)

// apiError says what GitHub refused and why, in GitHub's own words, without
// the request URL and the empty field list go-github prints around them.
func apiError(op string, err error) error {
	if err == nil {
		return nil
	}
	var rate *github.RateLimitError
	if errors.As(err, &rate) {
		return fmt.Errorf("%s: GitHub rate limit reached; it resets at %s", op, rate.Rate.Reset.Format("15:04:05 MST"))
	}
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) {
		return fmt.Errorf("%s: GitHub secondary rate limit reached: %s", op, abuse.Message)
	}
	var resp *github.ErrorResponse
	if !errors.As(err, &resp) || resp.Response == nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	status := resp.Response.StatusCode
	reasons := []string{}
	if resp.Message != "" && resp.Message != http.StatusText(status) {
		reasons = append(reasons, resp.Message)
	}
	for _, e := range resp.Errors {
		switch {
		case e.Message != "":
			reasons = append(reasons, e.Message)
		case e.Code != "" && e.Field != "":
			reasons = append(reasons, e.Field+" "+strings.ReplaceAll(e.Code, "_", " "))
		}
	}
	return &refusal{op: op, status: status, reason: fmt.Sprintf("GitHub %d %s: %s", status, http.StatusText(status), strings.Join(reasons, "; ")), err: err}
}

// refusal is a request GitHub answered with an error status.
type refusal struct {
	op     string
	status int
	reason string
	err    error
}

func (r *refusal) Error() string { return r.op + ": " + r.reason }
func (r *refusal) Unwrap() error { return r.err }

// alreadyExists reports whether GitHub refused a create because the resource
// is already there.
func alreadyExists(err error) bool {
	var resp *github.ErrorResponse
	if !errors.As(err, &resp) {
		return false
	}
	for _, e := range resp.Errors {
		if e.Code == "already_exists" {
			return true
		}
	}
	return false
}
