// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	libtime "github.com/bborbe/time"
)

// runWithSoftBudget runs the claude runner under a context whose deadline is
// the soft REVIEW_MAX_DURATION budget. Returns the run result/error, whether
// the budget expired, and the elapsed wall-clock time of the run. Expiry is
// detected PRECISELY from the run context's own deadline (runCtx.Err() ==
// context.DeadlineExceeded), never inferred from the returned error — a
// non-expired runner error (crash, network, or an error that merely wraps
// context.DeadlineExceeded without the deadline firing) keeps the existing
// failed/controller-retry path. The elapsed is measured around runner.Run with
// the same start := time.Now() / time.Since(start) shape pkg/funnel.go uses and
// is what the execution concerns gate keys its demotion decision on — per chunk
// under fan-out, against that chunk's own budget
// (DemotesUnverifiedConcerns compares the fraction against its threshold).
//
//nolint:revive,staticcheck // error-return: the (result, error, expired, elapsed) quadruple is the deliberate budget contract (ST1008 tolerates a trailing bool but not a fourth value).
func runWithSoftBudget(
	ctx context.Context,
	runner claudelib.ClaudeRunner,
	prompt string,
	maxDuration libtime.Duration,
) (*claudelib.ClaudeResult, error, bool, time.Duration) {
	runCtx, cancel := context.WithTimeout(ctx, maxDuration.Duration())
	defer cancel()
	start := time.Now()
	result, err := runner.Run(runCtx, prompt)
	elapsed := time.Since(start)
	expired := runCtx.Err() == context.DeadlineExceeded
	return result, err, expired, elapsed
}

// budgetExpiredResult builds the bounded-outcome routing for a budget-terminated
// claude run: Status done + NextPhase human_review with a message naming both
// the phase and the soft budget. Shared by all three phase steps so the
// budget-naming message and routing stay consistent; each step calls it only
// after its own runWithSoftBudget reported expired=true (a fired deadline).
//
// It also records WHY in ## Diagnostics. A budget-terminated run writes no review
// and posts nothing, and before this block it wrote nothing to the record either:
// its cause lived only in Result.Message, which reaches the pod log and is
// garbage-collected with the Job. That made it the one shape of dead run a record
// could not explain at all — measured 2026-11-09 on bborbe/nuke#1007, whose task
// file stops after ## Plan carrying phase: human_review and an empty current_job,
// indistinguishable from a review awaiting the operator. The diagnostic is what
// makes the record self-describing. md is nil-tolerant, so a caller without a
// markdown still routes exactly as before.
func budgetExpiredResult(
	md *agentlib.Markdown,
	stepName string,
	maxDuration libtime.Duration,
) *agentlib.Result {
	message := fmt.Sprintf(
		"%s claude run exceeded the %s soft time budget — routed to human review",
		stepName,
		maxDuration,
	)
	if md != nil {
		appendDiagnosticsSection(md, buildDiagnosticBlock(
			time.Now(),
			md.Frontmatter.TriggerCount(),
			PostResult{
				Outcome: "failed",
				// The step name is part of failure_step so a reader can tell a
				// planning-phase expiry from an execution one without inferring it
				// from which section happens to be missing.
				FailureStep:  "soft_budget_expired:" + stepName,
				Class:        ErrorClassPermanent,
				EscalateHint: true,
				ErrorMessage: message,
			},
		))
	}
	stampRunOutcome(md, RunOutcomeBudgetExpired, message)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: "human_review",
		Message:   message,
	}
}
