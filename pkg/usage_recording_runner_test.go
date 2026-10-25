// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

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

var _ = Describe("NewWorktreeRunner", func() {
	var (
		ctx        context.Context
		dir        string
		jobMetrics *mocks.JobMetrics
	)

	BeforeEach(func() {
		ctx = context.Background()
		dir = GinkgoT().TempDir()
		jobMetrics = &mocks.JobMetrics{}
	})

	// The execution phase builds its runner lazily inside buildRunner, which no
	// external test can reach — so the wrap is exercised through the constructor
	// that buildRunner delegates to. Without this the phase carrying the bulk of
	// the token spend could stop recording and every other test would still pass.
	It("records usage from the runner the execution phase builds", func() {
		shim := filepath.Join(dir, "claude")
		Expect(os.WriteFile(shim, []byte( //nolint:gosec // shim must be executable
			"#!/bin/sh\n"+
				`echo '{"type":"result","result":"ok","usage":{"input_tokens":9,"output_tokens":4},"num_turns":2}'`+
				"\nexit 0\n",
		), 0o700)).To(Succeed())
		originalPath := os.Getenv("PATH")
		DeferCleanup(func() { Expect(os.Setenv("PATH", originalPath)).To(Succeed()) })
		Expect(os.Setenv("PATH", dir+":"+originalPath)).To(Succeed())

		runner := prpkg.NewWorktreeRunner(prpkg.WorktreeRunnerConfig{}, dir, jobMetrics)

		result, err := runner.Run(ctx, "prompt")

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Result).To(Equal("ok"))
		Expect(jobMetrics.RecordUsageCallCount()).To(Equal(1))
		Expect(jobMetrics.RecordUsageArgsForCall(0).InputTokens).To(Equal(int64(9)))
		Expect(jobMetrics.RecordUsageArgsForCall(0).Turns).To(Equal(int64(2)))
	})
})
