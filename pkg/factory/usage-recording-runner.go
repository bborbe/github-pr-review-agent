// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"

	claudelib "github.com/bborbe/agent/claude"
	libmetrics "github.com/bborbe/agent/metrics"
)

// usageRecordingRunner records the token and turn cost of every claude
// invocation to JobMetrics.
//
// It wraps the runner rather than being called from each step on purpose. Every
// review step reaches the CLI through the same runner, so one decorator covers
// planning, execution, ai_review and the healthcheck path without threading a
// recorder through five call sites — and without giving each step a dependency
// it would have to remember to use.
//
// The record happens on the error path too. Since agent v0.129.0 a failed
// attempt carries the usage it actually consumed, and a run that spent three
// rate-limited invocations before succeeding cost four times what its result
// reports. Skipping the error path would under-report exactly the runs the
// rate-limit counters exist to explain.
type usageRecordingRunner struct {
	inner claudelib.ClaudeRunner
	usage libmetrics.JobMetrics
}

// Run delegates to the wrapped runner and records what the invocation cost. A
// nil result is not recorded: the CLI never started, so nothing was spent.
func (r *usageRecordingRunner) Run(
	ctx context.Context,
	prompt string,
) (*claudelib.ClaudeResult, error) {
	result, err := r.inner.Run(ctx, prompt)
	if result != nil {
		r.usage.RecordUsage(libmetrics.JobUsage{
			InputTokens:         result.InputTokens,
			OutputTokens:        result.OutputTokens,
			CacheReadTokens:     result.CacheReadTokens,
			CacheCreationTokens: result.CacheCreationTokens,
			Turns:               result.NumTurns,
			RateLimitRetries:    result.RateLimitRetries,
		})
	}
	return result, err
}
