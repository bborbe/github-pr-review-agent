// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	claudelib "github.com/bborbe/agent/claude"
	libmetrics "github.com/bborbe/agent/metrics"
)

// NewUsageRecordingRunner wraps a ClaudeRunner so the token and turn cost of
// every invocation is recorded to JobMetrics. A nil usage returns the runner
// unwrapped — the agent then behaves exactly as it did before the metric
// existed.
//
// Wrapping rather than calling RecordUsage from each step is deliberate: every
// review step reaches the CLI through the same runner, so one decorator covers
// planning, execution, ai_review and the healthcheck path, instead of five call
// sites each having to remember and each needing a metrics dependency.
func NewUsageRecordingRunner(
	inner claudelib.ClaudeRunner,
	usage libmetrics.JobMetrics,
) claudelib.ClaudeRunner {
	if usage == nil {
		return inner
	}
	return &usageRecordingRunner{inner: inner, usage: usage}
}

// WorktreeRunnerConfig is the per-worktree runner configuration the execution
// phase needs: the tools, model and env are fixed for the run, only the working
// directory varies with the checked-out worktree.
type WorktreeRunnerConfig struct {
	ClaudeConfigDir claudelib.ClaudeConfigDir
	AllowedTools    claudelib.AllowedTools
	Model           claudelib.ClaudeModel
	Env             map[string]string
}

// NewWorktreeRunner builds the execution phase's runner for one worktree and
// records its usage.
//
// This exists as its own constructor rather than inline in the step so the
// wrapping is reachable from a test: the step builds the runner lazily, inside
// buildRunner, which no external test can call — and the execution phase is
// where the bulk of the token spend happens, so an untested wrap there is
// exactly the gap that would let the metric go quiet without anyone noticing.
func NewWorktreeRunner(
	config WorktreeRunnerConfig,
	worktreePath string,
	usage libmetrics.JobMetrics,
) claudelib.ClaudeRunner {
	return NewUsageRecordingRunner(claudelib.NewClaudeRunner(claudelib.ClaudeRunnerConfig{
		ClaudeConfigDir:  config.ClaudeConfigDir,
		AllowedTools:     config.AllowedTools,
		Model:            config.Model,
		WorkingDirectory: claudelib.AgentDir(worktreePath),
		Env:              config.Env,
	}), usage)
}

type usageRecordingRunner struct {
	inner claudelib.ClaudeRunner
	usage libmetrics.JobMetrics
}

// Run delegates to the wrapped runner and records what the invocation cost.
//
// The error path records too. Since agent v0.129.0 a failed attempt carries the
// usage it actually consumed, and a run that spent three rate-limited
// invocations before succeeding cost four times what its result reports —
// skipping the error path would under-report exactly the runs the rate-limit
// counters exist to explain. A nil result is not recorded: the CLI never
// started, so nothing was spent.
func (r *usageRecordingRunner) Run(
	ctx context.Context,
	prompt string,
) (*claudelib.ClaudeResult, error) {
	result, err := r.inner.Run(ctx, prompt)
	if result == nil {
		return result, err
	}
	r.usage.RecordUsage(libmetrics.JobUsage{
		InputTokens:         result.InputTokens,
		OutputTokens:        result.OutputTokens,
		CacheReadTokens:     result.CacheReadTokens,
		CacheCreationTokens: result.CacheCreationTokens,
		Turns:               result.NumTurns,
		RateLimitRetries:    result.RateLimitRetries,
	})
	return result, err
}
