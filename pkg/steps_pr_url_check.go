// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	agentlib "github.com/bborbe/agent"
)

// NewPRURLPreflightStep returns a step that fails a pr-review task fast when
// its body carries no GitHub PR URL.
//
// Without this guard a URL-less task runs the entire pipeline — planning,
// execution, a full Claude review — and only discovers the problem in the post
// step, which fails with failure_step=pr_url_extraction and discards the
// completed review. That is the 2026-10-24 prod incident (task 55d44221,
// bborbe/git-rest#51): its body was the size-gate "## Oversized PR — parked for
// human review" text with no URL anywhere, and because the run entered at
// `execution` the planning prompt's own "missing PR URL → needs_input"
// instruction (pkg/prompts/planning_workflow.md) never ran.
//
// The step is prepended to every phase, mirroring NewGHTokenCheckStep, so the
// entry phase cannot route around it.
func NewPRURLPreflightStep() agentlib.Step {
	return &prURLPreflightStep{}
}

// prURLPreflightStep implements the URL preflight as an agentlib.Step.
type prURLPreflightStep struct{}

// Name implements agentlib.Step.
func (s *prURLPreflightStep) Name() string { return "verify-pr-url" }

// ShouldRun always returns true — the check is a pure string scan and the body
// can be rewritten between phases (the ## Plan / ## Review sections are added,
// and the frontmatter status changes), so a stale answer is not worth caching.
func (s *prURLPreflightStep) ShouldRun(
	_ context.Context,
	_ *agentlib.Markdown,
) (bool, error) {
	return true, nil
}

// Run returns NeedsInput when the task carries no PR URL, or Done+ContinueToNext
// when it does. NeedsInput (not Failed) is deliberate: a missing URL is not a
// code fault to retry — it is a task the operator must complete (add the URL,
// or abort). Retrying a permanent condition is what the controller's retry
// budget exists to survive, not to spend.
func (s *prURLPreflightStep) Run(
	_ context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	if prURL := ExtractPRURL(md); prURL != "" {
		return &agentlib.Result{
			Status:         agentlib.AgentStatusDone,
			ContinueToNext: true,
		}, nil
	}
	return needsInput(
		"no GitHub PR URL in task — expected https://github.com/<owner>/<repo>/pull/<n> " +
			"in the task body; add it, or abort the task. A pr-review task cannot run without one.",
	), nil
}
