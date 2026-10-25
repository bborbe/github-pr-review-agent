// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"errors"

	claudelib "github.com/bborbe/agent/claude"
	libmetrics "github.com/bborbe/agent/metrics"
	"github.com/bborbe/agent/metrics/mocks"
	agentmocks "github.com/bborbe/agent/mocks"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("UsageRecordingRunner", func() {
	var (
		ctx        context.Context
		inner      *agentmocks.ClaudeRunner
		jobMetrics *mocks.JobMetrics
	)

	BeforeEach(func() {
		ctx = context.Background()
		inner = &agentmocks.ClaudeRunner{}
		jobMetrics = &mocks.JobMetrics{}
	})

	Context("when the run succeeds", func() {
		BeforeEach(func() {
			inner.RunReturns(&claudelib.ClaudeResult{
				Result:              "done",
				InputTokens:         11,
				OutputTokens:        22,
				CacheReadTokens:     33,
				CacheCreationTokens: 44,
				NumTurns:            5,
				RateLimitRetries:    2,
			}, nil)
		})

		It("records the token, turn and retry counts once", func() {
			runner := prpkg.NewUsageRecordingRunner(inner, jobMetrics)

			result, err := runner.Run(ctx, "prompt")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Result).To(Equal("done"))
			Expect(jobMetrics.RecordUsageCallCount()).To(Equal(1))
			Expect(jobMetrics.RecordUsageArgsForCall(0)).To(Equal(libmetrics.JobUsage{
				InputTokens:         11,
				OutputTokens:        22,
				CacheReadTokens:     33,
				CacheCreationTokens: 44,
				Turns:               5,
				RateLimitRetries:    2,
			}))
		})
	})

	Context("when the run fails on a rate limit", func() {
		BeforeEach(func() {
			// Since agent v0.129.0 the error path carries the usage the abandoned
			// attempt really spent; dropping it would under-report exactly the runs
			// the rate-limit counters exist to explain.
			inner.RunReturns(
				&claudelib.ClaudeResult{InputTokens: 7, OutputTokens: 3, NumTurns: 2},
				errors.New("claude CLI failed: api_retry: rate_limit (HTTP 429) — attempt 10/10"),
			)
		})

		It("still records what the attempt consumed", func() {
			runner := prpkg.NewUsageRecordingRunner(inner, jobMetrics)

			_, err := runner.Run(ctx, "prompt")

			Expect(err).To(HaveOccurred())
			Expect(jobMetrics.RecordUsageCallCount()).To(Equal(1))
			Expect(jobMetrics.RecordUsageArgsForCall(0).InputTokens).To(Equal(int64(7)))
			Expect(jobMetrics.RecordUsageArgsForCall(0).Turns).To(Equal(int64(2)))
		})
	})

	Context("when the CLI never started", func() {
		BeforeEach(func() {
			inner.RunReturns(nil, errors.New("start claude CLI"))
		})

		It("records nothing", func() {
			runner := prpkg.NewUsageRecordingRunner(inner, jobMetrics)

			_, err := runner.Run(ctx, "prompt")

			Expect(err).To(HaveOccurred())
			Expect(jobMetrics.RecordUsageCallCount()).To(Equal(0))
		})
	})

	Context("when no metrics are supplied", func() {
		BeforeEach(func() {
			inner.RunReturns(&claudelib.ClaudeResult{Result: "done", InputTokens: 1}, nil)
		})

		It("returns the runner unwrapped and still runs", func() {
			runner := prpkg.NewUsageRecordingRunner(inner, nil)

			result, err := runner.Run(ctx, "prompt")

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Result).To(Equal("done"))
			Expect(inner.RunCallCount()).To(Equal(1))
		})
	})
})
