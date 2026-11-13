// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"fmt"

	agentlib "github.com/bborbe/agent"
)

// Frontmatter keys that make a run's terminal routing machine-readable.
//
// The defect these exist for: every terminal routing in this agent returns
// `Done` + `human_review` — a failed post, a no-review skip, and an expired soft
// budget alike. The controller then writes `phase: human_review` and clears
// `current_job`, so the record reads exactly like a review that completed and is
// awaiting the operator. Nothing downstream — the queue scorer, the operator
// inbox, a human reading the frontmatter — can separate "reviewed, awaiting you"
// from "post failed, nothing was posted".
//
// Measured 2026-11-09 over the 2026-11-07→09 window: of 33 records that had
// started, 15 ended with no verdict and every one of them read `human_review`
// with an empty `current_job`; only a read of the body distinguished them.
//
// Absence is meaningful: a record WITHOUT `run_outcome` took a path that does not
// stamp it, so a reader must not treat absence as success.
const (
	runOutcomeKey    = "run_outcome"
	failureReasonKey = "failure_reason"
)

// Run outcome values. Closed vocabulary — one per routing that ends without a
// posted review, so a consumer can switch on the value without parsing prose.
const (
	// RunOutcomePostFailed means the review was produced but the GitHub write
	// failed, so the PR carries no verdict and its merge stays gated.
	RunOutcomePostFailed = "post_failed"
	// RunOutcomeNoReview means the run produced no review at all and the post was
	// skipped deliberately, rather than publishing a blocking review nobody wrote.
	RunOutcomeNoReview = "no_review"
	// RunOutcomeBudgetExpired means the run hit its soft time budget before
	// producing anything.
	RunOutcomeBudgetExpired = "budget_expired"
)

// stampRunOutcome records the terminal routing on the record's frontmatter.
//
// The keys are neither controller-owned nor operator-owned (see
// `controllerOwnedFields` / `operatorOwnedFields` in the controller's result
// writer), so the merge preserves them; and `TaskFrontmatter` is a free-form
// map, so no allowlist rejects them. `assignee` is the precedent that proves a
// step-written frontmatter key reaches the record.
//
// md is nil-tolerant so a caller without a markdown keeps its old behaviour.
func stampRunOutcome(md *agentlib.Markdown, outcome, reason string) {
	if md == nil {
		return
	}
	if md.Frontmatter == nil {
		md.Frontmatter = agentlib.TaskFrontmatter{}
	}
	md.Frontmatter[runOutcomeKey] = outcome
	if reason != "" {
		md.Frontmatter[failureReasonKey] = reason
	}
}

// postFailureReason builds the bounded reason string for a failed post: the step
// that failed plus its HTTP status, which is what a reader needs to tell a rate
// limit from a permissions fault. Deliberately not the full error message — that
// can carry a response body, and the body already lives in ## Diagnostics.
func postFailureReason(result PostResult) string {
	if result.HTTPStatus != 0 {
		return fmt.Sprintf("%s (HTTP %d)", result.FailureStep, result.HTTPStatus)
	}
	return result.FailureStep
}
