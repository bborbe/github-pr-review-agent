// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/golang/glog"
)

// PRBranches holds the source and target branch names of a pull request.
type PRBranches struct {
	Source string
	Target string
}

// Client interacts with GitHub via the gh CLI.
//
//counterfeiter:generate -o ../../mocks/github-client.go --fake-name GitHubClient . Client
type Client interface {
	GetPRBranches(ctx context.Context, owner, repo string, number int) (PRBranches, error)
	PostComment(ctx context.Context, owner, repo string, number int, body string) error
	SubmitReview(
		ctx context.Context,
		owner, repo string,
		number int,
		body string,
		verdict prpkg.Verdict,
	) error
	// PRState fetches the current GitHub state of a pull request:
	// state (OPEN|MERGED|CLOSED), mergedAt (RFC3339, empty if not
	// merged), and headRefOid (the PR's current HEAD commit SHA).
	PRState(ctx context.Context, prURL string) (state, mergedAt, headRefOid string, err error)
	// PRDiff fetches the raw unified diff of a pull request via the gh CLI.
	// Same auth path as PRState.
	PRDiff(ctx context.Context, prURL string) (string, error)
	// NotifySuperseded posts a best-effort COMMENT review naming the ref the
	// review ran against and the head that replaced it. It posts no verdict, so
	// it cannot satisfy or block branch protection.
	NotifySuperseded(ctx context.Context, prURL, ref, headRefOid, runID string) error
}

// NewGHClient creates a Client that uses the gh CLI.
// If token is non-empty, it will be passed to gh CLI via GH_TOKEN env var.
func NewGHClient(token string) Client {
	return &ghClient{token: token}
}

type ghClient struct {
	token string
}

// GetPRBranches fetches the source and target branch names for a pull request.
func (c *ghClient) GetPRBranches(
	ctx context.Context,
	owner, repo string,
	number int,
) (PRBranches, error) {
	repoArg := fmt.Sprintf("%s/%s", owner, repo)
	numberArg := strconv.Itoa(number)

	// #nosec G204 -- args are validated by caller, owner/repo from URL parsing
	cmd := exec.CommandContext(ctx, "gh", "pr", "view",
		numberArg,
		"--repo", repoArg,
		"--json", "headRefName,baseRefName",
		"--jq", ".headRefName + \"\\n\" + .baseRefName",
	)

	// Set GH_TOKEN if configured
	if c.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return PRBranches{}, errors.Wrapf(
			ctx,
			err,
			"gh pr view failed: %s",
			strings.TrimSpace(stderr.String()),
		)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 || lines[0] == "" || lines[1] == "" {
		return PRBranches{}, errors.Errorf(ctx, "gh pr view returned incomplete branch info")
	}

	return PRBranches{
		Source: lines[0],
		Target: lines[1],
	}, nil
}

// PostComment posts a comment on a pull request.
func (c *ghClient) PostComment(
	ctx context.Context,
	owner, repo string,
	number int,
	body string,
) error {
	repoArg := fmt.Sprintf("%s/%s", owner, repo)
	numberArg := strconv.Itoa(number)

	// #nosec G204 -- args are validated by caller, owner/repo from URL parsing
	cmd := exec.CommandContext(ctx, "gh", "pr", "comment",
		numberArg,
		"--repo", repoArg,
		"--body", body,
	)

	// Set GH_TOKEN if configured
	if c.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return errors.Wrapf(
			ctx,
			err,
			"gh pr comment failed: %s",
			strings.TrimSpace(stderr.String()),
		)
	}

	return nil
}

// PRState fetches the current GitHub state of a pull request via
// `gh pr view <pr_url> --json state,mergedAt,headRefOid`. Returns
// state (OPEN|MERGED|CLOSED), mergedAt (RFC3339, empty if not merged),
// and headRefOid (the PR's current HEAD commit SHA).
func (c *ghClient) PRState(
	ctx context.Context,
	prURL string,
) (state, mergedAt, headRefOid string, err error) {
	// #nosec G204 -- args are validated by caller, prURL from URL parsing
	cmd := exec.CommandContext(ctx, "gh", "pr", "view",
		prURL,
		"--json", "state,mergedAt,headRefOid",
	)

	// Set GH_TOKEN if configured
	if c.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		glog.V(2).Infof("gh pr view pr_url=%s err=%v", prURL, err)
		return "", "", "", errors.Wrapf(
			ctx,
			err,
			"gh pr view failed: %s",
			strings.TrimSpace(stderr.String()),
		)
	}

	var result struct {
		State      string `json:"state"`
		MergedAt   string `json:"mergedAt"`
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		glog.V(2).
			Infof("gh pr view pr_url=%s err=%v body=%q", prURL, err, strings.TrimSpace(stdout.String()))
		return "", "", "", errors.Wrapf(
			ctx,
			err,
			"gh pr view returned unparseable JSON: %s",
			strings.TrimSpace(stdout.String()),
		)
	}

	glog.V(2).Infof(
		"gh pr view pr_url=%s state=%s merged_at=%s head_ref_oid=%s",
		prURL,
		result.State,
		result.MergedAt,
		result.HeadRefOid,
	)
	return result.State, result.MergedAt, result.HeadRefOid, nil
}

// PRDiff fetches the raw unified diff of a pull request via
// `gh pr diff <pr_url>`. Returns the diff verbatim (no trimming or
// parsing) so the ai_review verifier can check cited file + line numbers
// against the real change set.
func (c *ghClient) PRDiff(
	ctx context.Context,
	prURL string,
) (string, error) {
	// #nosec G204 -- args are validated by caller, prURL from URL parsing
	cmd := exec.CommandContext(ctx, "gh", "pr", "diff", prURL)

	// Set GH_TOKEN if configured
	if c.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		glog.V(2).Infof("gh pr diff pr_url=%s err=%v", prURL, err)
		return "", errors.Wrapf(
			ctx,
			err,
			"gh pr diff failed: %s",
			strings.TrimSpace(stderr.String()),
		)
	}

	return stdout.String(), nil
}

// maxSupersededNoticeBody hard-caps the superseded-notice body. The notice is a
// short, bounded signal — never a place to spill a response body or a
// diagnostic block — so the cap is deliberately far below GitHub's 65 KiB limit.
const maxSupersededNoticeBody = 2000

// buildSupersededNoticeBody composes the COMMENT review body naming the ref a
// superseded review ran against and the head that replaced it. Its only inputs
// are the ref, the current head and the run identifier; it never embeds a
// response body, a raw error string or ## Diagnostics content. Empty inputs are
// rendered as-is — the builder never invents values.
func buildSupersededNoticeBody(ref, headRefOid, runID string) string {
	var sb strings.Builder
	sb.WriteString("Automated review superseded — no review was posted.\n\n")
	fmt.Fprintf(
		&sb,
		"The review ran against %s and the PR head moved to %s before it finished.\n",
		ref,
		headRefOid,
	)
	sb.WriteString(
		"The current head needs a fresh review; it will be reviewed again if it is re-queued.\n\n",
	)
	fmt.Fprintf(&sb, "run: %s\n", runID)
	fmt.Fprintf(&sb, "reviewed_ref: %s\n", ref)
	fmt.Fprintf(&sb, "current_head: %s\n", headRefOid)
	return truncateSupersededNoticeBody(sb.String())
}

// truncateSupersededNoticeBody hard-caps body at maxSupersededNoticeBody bytes,
// backing off to a UTF-8 boundary so a multibyte value is never split.
func truncateSupersededNoticeBody(body string) string {
	if len(body) <= maxSupersededNoticeBody {
		return body
	}
	b := []byte(body[:maxSupersededNoticeBody])
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}

// NotifySuperseded posts a best-effort COMMENT review naming the ref the review
// ran against and the head that replaced it. It uses the reviews API
// (`gh pr review --comment`, i.e. event: COMMENT), never the Issues API, so it
// needs only the App's existing pull_requests:write permission. It carries no
// verdict and therefore cannot satisfy or block branch protection.
func (c *ghClient) NotifySuperseded(
	ctx context.Context,
	prURL, ref, headRefOid, runID string,
) error {
	body := buildSupersededNoticeBody(ref, headRefOid, runID)

	// #nosec G204 -- args are validated by caller, prURL from URL parsing
	cmd := exec.CommandContext(ctx, "gh", "pr", "review", prURL, "--comment", "--body", body)

	// Set GH_TOKEN if configured
	if c.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return errors.Wrapf(ctx, err, "gh pr review failed: %s", strings.TrimSpace(stderr.String()))
	}

	return nil
}

// SubmitReview submits a structured review (approve or request-changes) on a pull request.
func (c *ghClient) SubmitReview(
	ctx context.Context,
	owner, repo string,
	number int,
	body string,
	v prpkg.Verdict,
) error {
	// Defensive: rejects out-of-enum verdict values (the binary set is approve/request-changes)
	if v != prpkg.VerdictApprove && v != prpkg.VerdictRequestChanges {
		return errors.Errorf(
			ctx,
			"unsupported verdict for SubmitReview: %s",
			v,
		)
	}

	repoArg := fmt.Sprintf("%s/%s", owner, repo)
	numberArg := strconv.Itoa(number)

	args := []string{"pr", "review", numberArg, "--repo", repoArg}

	if v == prpkg.VerdictApprove {
		args = append(args, "--approve")
	} else {
		args = append(args, "--request-changes")
	}

	args = append(args, "--body", body)

	// #nosec G204 -- args are validated by caller, owner/repo from URL parsing
	cmd := exec.CommandContext(ctx, "gh", args...)

	// Set GH_TOKEN if configured
	if c.token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return errors.Wrapf(ctx, err, "gh pr review failed: %s", strings.TrimSpace(stderr.String()))
	}

	return nil
}
