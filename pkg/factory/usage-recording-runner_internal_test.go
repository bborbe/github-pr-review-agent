// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"
	"errors"
	"testing"

	claudelib "github.com/bborbe/agent/claude"
	libmetrics "github.com/bborbe/agent/metrics"
	"github.com/bborbe/agent/metrics/mocks"
	. "github.com/onsi/gomega"
)

// stubRunner is a ClaudeRunner returning canned values, so the decorator can be
// exercised without spawning the CLI.
type stubRunner struct {
	result *claudelib.ClaudeResult
	err    error
}

func (s stubRunner) Run(context.Context, string) (*claudelib.ClaudeResult, error) {
	return s.result, s.err
}

func TestUsageRecordingRunnerRecordsEveryAttempt(t *testing.T) {
	t.Run("records the usage of a successful run", func(t *testing.T) {
		g := NewWithT(t)
		metrics := &mocks.JobMetrics{}
		runner := &usageRecordingRunner{
			inner: stubRunner{result: &claudelib.ClaudeResult{
				InputTokens:         11,
				OutputTokens:        22,
				CacheReadTokens:     33,
				CacheCreationTokens: 44,
				NumTurns:            5,
				RateLimitRetries:    2,
			}},
			usage: metrics,
		}

		_, err := runner.Run(context.Background(), "prompt")

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(metrics.RecordUsageCallCount()).To(Equal(1))
		g.Expect(metrics.RecordUsageArgsForCall(0)).To(Equal(libmetrics.JobUsage{
			InputTokens:         11,
			OutputTokens:        22,
			CacheReadTokens:     33,
			CacheCreationTokens: 44,
			Turns:               5,
			RateLimitRetries:    2,
		}))
	})

	t.Run("records the usage an abandoned attempt consumed", func(t *testing.T) {
		g := NewWithT(t)
		metrics := &mocks.JobMetrics{}
		runner := &usageRecordingRunner{
			// Since agent v0.129.0 the error path carries the usage the attempt
			// really spent. Dropping it would under-report exactly the runs the
			// rate-limit counters exist to explain.
			inner: stubRunner{
				result: &claudelib.ClaudeResult{InputTokens: 7, OutputTokens: 3, NumTurns: 2},
				err: errors.New(
					"claude CLI failed: api_retry: rate_limit (HTTP 429) — attempt 10/10",
				),
			},
			usage: metrics,
		}

		_, err := runner.Run(context.Background(), "prompt")

		g.Expect(err).To(HaveOccurred())
		g.Expect(metrics.RecordUsageCallCount()).To(Equal(1))
		g.Expect(metrics.RecordUsageArgsForCall(0).InputTokens).To(Equal(int64(7)))
		g.Expect(metrics.RecordUsageArgsForCall(0).Turns).To(Equal(int64(2)))
	})

	t.Run("records nothing when the CLI never started", func(t *testing.T) {
		g := NewWithT(t)
		metrics := &mocks.JobMetrics{}
		runner := &usageRecordingRunner{
			inner: stubRunner{result: nil, err: errors.New("start claude CLI")},
			usage: metrics,
		}

		_, err := runner.Run(context.Background(), "prompt")

		g.Expect(err).To(HaveOccurred())
		g.Expect(metrics.RecordUsageCallCount()).To(Equal(0))
	})
}
