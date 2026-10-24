// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("mid-run PR-state watch", func() {
	const (
		reviewedRef  = "abc123"
		movedHead    = "def456"
		pollInterval = libtime.Duration(10 * time.Millisecond)
		approveOut   = "review body\n\n```json\n" + `{"verdict":"approve","reason":"ok"}` + "\n```\n"
	)

	var (
		ctx         context.Context
		tmpDir      string
		repoManager *mocks.RepoManager
		prState     *mocks.PRStateClient
		calls       atomic.Int32
	)

	// sequencePRState makes the first PRState call (the step's pre-flight)
	// report the PR open at the reviewed ref, and every later call (the
	// mid-run polls and the post-cancel re-check) answer with later().
	sequencePRState := func(later func(call int32) (string, string, string, error)) {
		calls.Store(0)
		prState.PRStateStub = func(_ context.Context, _ string) (string, string, string, error) {
			n := calls.Add(1)
			if n == 1 {
				return "OPEN", "", reviewedRef, nil
			}
			return later(n)
		}
	}

	// blockingRunner blocks until its run context ends — a long claude call.
	blockingRunner := func() *mocks.ClaudeRunnerMock {
		r := &mocks.ClaudeRunnerMock{}
		r.RunStub = func(runCtx context.Context, _ string) (*claudelib.ClaudeResult, error) {
			<-runCtx.Done()
			return nil, runCtx.Err()
		}
		return r
	}

	// slowApproveRunner runs for d, then returns a valid approve review.
	slowApproveRunner := func(d time.Duration) *mocks.ClaudeRunnerMock {
		r := &mocks.ClaudeRunnerMock{}
		r.RunStub = func(runCtx context.Context, _ string) (*claudelib.ClaudeResult, error) {
			select {
			case <-time.After(d):
				return &claudelib.ClaudeResult{Result: approveOut}, nil
			case <-runCtx.Done():
				return nil, runCtx.Err()
			}
		}
		return r
	}

	buildMD := func() *agentlib.Markdown {
		md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: `+reviewedRef+`
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
		Expect(err).NotTo(HaveOccurred())
		return md
	}

	newExecutionStep := func(
		runner claudelib.ClaudeRunner,
		poster pkg.PrPoster,
		funnel pkg.FunnelRunner,
		budget libtime.Duration,
	) agentlib.Step {
		return pkg.NewCheckoutExecutionStep(
			repoManager,
			claudelib.ClaudeConfigDir(tmpDir),
			"agent",
			"sonnet",
			map[string]string{},
			claudelib.AllowedTools{"Read"},
			"standard",
			nil,
			poster,
			funnel,
			libtime.NewCurrentDateTime(),
			runner,
			budget,
			prState,
			pkg.DefaultReviewChunkConfig(),
			pollInterval,
		)
	}

	// twoChunkFunnel partitions into exactly two chunks under the default
	// chunk config (601 added lines over two files).
	twoChunkFunnel := func() *mocks.FunnelRunner {
		f := &mocks.FunnelRunner{}
		f.RunReturns(pkg.FunnelResult{
			Ran:          true,
			FindingsJSON: `{"stats":{"yamls_run":1,"findings_count":0,"elapsed_ms":1},"findings_by_owner":{},"errors":[]}`,
			ChangedFiles: []pkg.ChangedFile{
				{Path: "a.go", Additions: 301},
				{Path: "b.go", Additions: 300},
			},
		}, nil)
		return f
	}

	resolutionBody := func(md *agentlib.Markdown) string {
		section, ok := md.FindSection("## Resolution")
		Expect(ok).To(BeTrue())
		return section.Body
	}

	BeforeEach(func() {
		ctx = context.Background()
		repoManager = &mocks.RepoManager{}
		repoManager.EnsureWorktreeReturns("/work/test", nil)
		prState = &mocks.PRStateClient{}

		var err error
		tmpDir, err = os.MkdirTemp("", "pr-state-watch-*")
		Expect(err).NotTo(HaveOccurred())
		cmdDir := filepath.Join(tmpDir, "plugins", "marketplaces", "coding", "commands")
		Expect(os.MkdirAll(cmdDir, 0750)).To(Succeed())
		Expect(os.WriteFile(
			filepath.Join(cmdDir, "pr-review.md"),
			[]byte(
				"---\ndescription: Test plugin\nallowed-tools: Task\n---\n# PR Review\n\nProcedure body.\n",
			),
			0600,
		)).To(Succeed())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	Describe("execution phase", func() {
		It(
			"cancels mid-chunk on a moved head: remaining chunks skipped, no ## Review, superseded + marker",
			func() {
				sequencePRState(func(int32) (string, string, string, error) {
					return "OPEN", "", movedHead, nil
				})
				runner := blockingRunner()
				poster := &mocks.PrPoster{}
				step := newExecutionStep(
					runner,
					poster,
					twoChunkFunnel(),
					libtime.Duration(25*time.Minute),
				)

				md := buildMD()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("done"))
				Expect(md.Frontmatter["status"]).To(Equal("aborted"))

				body := resolutionBody(md)
				Expect(body).To(ContainSubstring(`"verdict": "superseded"`))
				Expect(body).To(ContainSubstring(`"head_ref_oid": "` + movedHead + `"`))
				Expect(body).To(ContainSubstring(`"cancelled_during": "execution"`))

				_, exists := md.FindSection("## Review")
				Expect(exists).To(BeFalse())
				_, exists = md.FindSection("## Salvage")
				Expect(exists).To(BeFalse())
				Expect(runner.RunCallCount()).To(Equal(1)) // chunk 2 never ran
				Expect(poster.PostCallCount()).To(Equal(0))
			},
		)

		It("cancels on a merge with verdict merged + marker", func() {
			sequencePRState(func(int32) (string, string, string, error) {
				return "MERGED", "2026-10-23T12:00:00Z", reviewedRef, nil
			})
			step := newExecutionStep(
				blockingRunner(),
				nil,
				twoChunkFunnel(),
				libtime.Duration(25*time.Minute),
			)

			md := buildMD()
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("done"))
			Expect(md.Frontmatter["status"]).To(Equal("completed"))
			body := resolutionBody(md)
			Expect(body).To(ContainSubstring(`"verdict": "merged"`))
			Expect(body).To(ContainSubstring(`"cancelled_during": "execution"`))
		})

		It(
			"completes an unchanged PR: ## Review written, posted, no ## Resolution, polled at least once",
			func() {
				sequencePRState(func(int32) (string, string, string, error) {
					return "OPEN", "", reviewedRef, nil
				})
				poster := &mocks.PrPoster{}
				poster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 1})
				step := newExecutionStep(
					slowApproveRunner(60*time.Millisecond),
					poster,
					twoChunkFunnel(),
					libtime.Duration(25*time.Minute),
				)

				md := buildMD()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("ai_review"))
				_, exists := md.FindSection("## Review")
				Expect(exists).To(BeTrue())
				_, exists = md.FindSection("## Resolution")
				Expect(exists).To(BeFalse())
				Expect(poster.PostCallCount()).To(Equal(1))
				Expect(
					prState.PRStateCallCount(),
				).To(BeNumerically(">=", 2))
				// pre-flight + ≥1 poll
			},
		)

		It("keeps running when a poll fails, and the review completes", func() {
			sequencePRState(func(int32) (string, string, string, error) {
				return "", "", "", fmt.Errorf("gh: transient failure")
			})
			poster := &mocks.PrPoster{}
			poster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 2})
			step := newExecutionStep(
				slowApproveRunner(60*time.Millisecond),
				poster,
				twoChunkFunnel(),
				libtime.Duration(25*time.Minute),
			)

			md := buildMD()
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("ai_review"))
			_, exists := md.FindSection("## Resolution")
			Expect(exists).To(BeFalse())
			Expect(prState.PRStateCallCount()).To(BeNumerically(">=", 2))
		})

		It("fails for a retry when the post-cancel re-check finds the PR reviewable again", func() {
			sequencePRState(func(n int32) (string, string, string, error) {
				if n == 2 {
					return "OPEN", "", movedHead, nil // the poll sees a moved head
				}
				return "OPEN", "", reviewedRef, nil // the re-check sees it back
			})
			step := newExecutionStep(
				blockingRunner(),
				nil,
				twoChunkFunnel(),
				libtime.Duration(25*time.Minute),
			)

			md := buildMD()
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("re-check found the PR reviewable"))
			_, exists := md.FindSection("## Resolution")
			Expect(exists).To(BeFalse())
		})

		It("still routes a fired soft budget to human_review, not to a resolution", func() {
			sequencePRState(func(int32) (string, string, string, error) {
				return "OPEN", "", reviewedRef, nil
			})
			step := newExecutionStep(
				blockingRunner(),
				nil,
				twoChunkFunnel(),
				libtime.Duration(40*time.Millisecond),
			)

			md := buildMD()
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("human_review"))
			_, exists := md.FindSection("## Resolution")
			Expect(exists).To(BeFalse())
		})

		It("does not poll when the interval is 0", func() {
			sequencePRState(func(int32) (string, string, string, error) {
				return "OPEN", "", movedHead, nil
			})
			step := pkg.NewCheckoutExecutionStep(
				repoManager,
				claudelib.ClaudeConfigDir(tmpDir),
				"agent",
				"sonnet",
				map[string]string{},
				claudelib.AllowedTools{"Read"},
				"standard",
				nil,
				nil,
				twoChunkFunnel(),
				libtime.NewCurrentDateTime(),
				slowApproveRunner(30*time.Millisecond),
				libtime.Duration(25*time.Minute),
				prState,
				pkg.DefaultReviewChunkConfig(),
				libtime.Duration(0),
			)

			md := buildMD()
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.NextPhase).To(Equal("ai_review"))
			Expect(prState.PRStateCallCount()).To(Equal(1)) // pre-flight only
		})
	})

	Describe("ai_review phase", func() {
		It(
			"cancels on a merge with verdict merged + cancelled_during ai_review, no ## Verdict",
			func() {
				sequencePRState(func(int32) (string, string, string, error) {
					return "MERGED", "2026-10-23T12:00:00Z", reviewedRef, nil
				})
				step := pkg.NewReviewStep(
					blockingRunner(),
					nil,
					claudelib.Instructions{},
					nil,
					"",
					"",
					libtime.Duration(25*time.Minute),
					prState,
					pollInterval,
				)

				md := buildMD()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("done"))
				body := resolutionBody(md)
				Expect(body).To(ContainSubstring(`"verdict": "merged"`))
				Expect(body).To(ContainSubstring(`"cancelled_during": "ai_review"`))
				_, exists := md.FindSection("## Verdict")
				Expect(exists).To(BeFalse())
			},
		)
	})

	Describe("planning phase", func() {
		It("is never watched: only the pre-flight reads the PR state", func() {
			sequencePRState(func(int32) (string, string, string, error) {
				return "OPEN", "", movedHead, nil
			})
			runner := &mocks.ClaudeRunnerMock{}
			runner.RunStub = func(_ context.Context, _ string) (*claudelib.ClaudeResult, error) {
				time.Sleep(50 * time.Millisecond)
				return &claudelib.ClaudeResult{Result: `{"summary":"plan"}`}, nil
			}
			step := pkg.NewPlanningStep(
				runner,
				claudelib.Instructions{},
				libtime.Duration(25*time.Minute),
				prState,
			)

			_, err := step.Run(ctx, buildMD())
			Expect(err).NotTo(HaveOccurred())
			Expect(prState.PRStateCallCount()).To(Equal(1))
			Expect(runner.RunCallCount()).To(Equal(1))
		})
	})
})
