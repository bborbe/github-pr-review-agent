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
