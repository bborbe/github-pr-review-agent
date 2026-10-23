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
	"github.com/golang/glog"
)

// DefaultPRStatePollInterval is the default PR_STATE_POLL_INTERVAL: how often
// a running execution / ai_review claude call re-reads the live PR state. One
// `gh pr view` per tick hits the GraphQL bucket (5,000 points/hr per token);
// at 60s and 3 concurrent reviewer pods that is at most 180 calls/hr.
const DefaultPRStatePollInterval = libtime.Duration(60 * time.Second)

// Phase names recorded as ResolutionOutput.CancelledDuring when the mid-run
// watch cancels a claude call. Planning is never watched.
const (
	CancelledDuringExecution = "execution"
	CancelledDuringAIReview  = "ai_review"
)

// prStateWatch polls the live PR state while a claude call runs and cancels
// the call once the review is moot (merged, closed, or HEAD moved past the
// reviewed ref). The watch never writes the task: it only cancels, and the
// caller records the terminal verdict after the runner has returned.
type prStateWatch struct {
	client   PRStateClient
	prURL    string
	ref      string
	interval libtime.Duration
	phase    string
}

// newPRStateWatch returns the watch for one claude call, or nil when polling
// is disabled: nil client, non-positive interval, or no PR URL in the task.
// A nil watch makes runWatchedWithSoftBudget behave exactly like
// runWithSoftBudget.
func newPRStateWatch(
	md *agentlib.Markdown,
	client PRStateClient,
	interval libtime.Duration,
	phase string,
) *prStateWatch {
	if client == nil || interval <= 0 {
		return nil
	}
	prURL := ExtractPRURL(md)
	if prURL == "" {
		return nil
	}
	ref, _ := md.Frontmatter.String("ref")
	return &prStateWatch{
		client:   client,
		prURL:    prURL,
		ref:      ref,
		interval: interval,
		phase:    phase,
	}
}

// run polls until ctx is done. On a moot PR state it calls cancel and
// returns true; otherwise it returns false once ctx ends. A failed poll is a
// no-op (the claude call continues), matching prStateCheck's best-effort
// contract.
func (w *prStateWatch) run(ctx context.Context, cancel context.CancelFunc) bool {
	ticker := time.NewTicker(w.interval.Duration())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
		state, _, head, err := w.client.PRState(ctx, w.prURL)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			glog.V(2).Infof(
				"pr-state-watch: phase=%s PR=%s poll failed err=%v decision=continue",
				w.phase, w.prURL, err,
			)
			continue
		}
		if isMootPRState(state, w.ref, head) {
			glog.V(2).Infof(
				"pr-state-watch: phase=%s PR=%s state=%s ref=%s head=%s decision=cancel",
				w.phase, w.prURL, state, w.ref, head,
			)
			cancel()
			return true
		}
		glog.V(2).Infof(
			"pr-state-watch: phase=%s PR=%s state=%s ref=%s head=%s decision=continue",
			w.phase, w.prURL, state, w.ref, head,
		)
	}
}

// isMootPRState reports whether a review of ref is moot given the live PR
// state — the same three cases prStateCheck routes to a terminal verdict.
func isMootPRState(state, ref, head string) bool {
	return state == "MERGED" || state == "CLOSED" || (state == "OPEN" && ref != head)
}

// budgetedRun is the outcome of one watched, budgeted claude call.
type budgetedRun struct {
	Result *claudelib.ClaudeResult
	Err    error
	// Expired reports the soft budget's own deadline fired (→ human_review).
	Expired bool
	// Mooted reports the PR-state watch cancelled the call (→ terminal verdict).
	Mooted  bool
	Elapsed time.Duration
}

// runWatchedWithSoftBudget runs runWithSoftBudget while watch polls the live
// PR state. The watch goroutine is always joined before returning. A nil
// watch runs unwatched. Mooted is set only when the watch actually cancelled
// the call; a call that finished on its own before the cancel took effect
// still returns its result, and the step's post-flight check handles it.
func runWatchedWithSoftBudget(
	ctx context.Context,
	runner claudelib.ClaudeRunner,
	prompt string,
	maxDuration libtime.Duration,
	watch *prStateWatch,
) budgetedRun {
	if watch == nil {
		result, err, expired, elapsed := runWithSoftBudget(ctx, runner, prompt, maxDuration)
		return budgetedRun{Result: result, Err: err, Expired: expired, Elapsed: elapsed}
	}
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	mooted := make(chan bool, 1)
	// Owned goroutine: it exits when watchCtx ends (cancelled below once the
	// runner returns) and is joined via the mooted channel before return.
	go func() { mooted <- watch.run(watchCtx, cancel) }()
	result, err, expired, elapsed := runWithSoftBudget(watchCtx, runner, prompt, maxDuration)
	cancel()
	wasMooted := <-mooted
	return budgetedRun{
		Result:  result,
		Err:     err,
		Expired: expired && !wasMooted,
		Mooted:  wasMooted && err != nil,
		Elapsed: elapsed,
	}
}

// mootCancelledResult records the terminal verdict for a claude call the
// watch cancelled, re-reading the PR state on the step's own (uncancelled)
// ctx. When the re-check finds the PR reviewable again or gh fails, the run
// has no output to route, so it fails and the controller retries.
func mootCancelledResult(
	ctx context.Context,
	md *agentlib.Markdown,
	client PRStateClient,
	phase string,
) (*agentlib.Result, error) {
	result, err := prStateCheckCancelled(ctx, md, client, phase)
	if err != nil || result != nil {
		return result, err
	}
	return &agentlib.Result{
		Status: agentlib.AgentStatusFailed,
		Message: fmt.Sprintf(
			"%s claude run cancelled on a moot PR state, but the re-check found the PR reviewable — retry",
			phase,
		),
	}, nil
}
