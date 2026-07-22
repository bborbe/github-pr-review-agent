// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"fmt"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("checkoutExecutionStep", func() {
	var (
		ctx         context.Context
		repoManager *mocks.RepoManager
		step        agentlib.Step
	)

	BeforeEach(func() {
		ctx = context.Background()
		repoManager = &mocks.RepoManager{}
		currentDateTime := libtime.NewCurrentDateTime()
		step = pkg.NewCheckoutExecutionStep(
			repoManager,
			"",
			"agent",
			"sonnet",
			map[string]string{},
			claudelib.AllowedTools{"Read"},
			"standard",
			nil,
			nil,
			nil,
			currentDateTime,
		)
	})

	Describe("Name", func() {
		It("returns pr-execute", func() {
			Expect(step.Name()).To(Equal("pr-execute"))
		})
	})

	Describe("ShouldRun", func() {
		// ShouldRun always returns true. Idempotency for the "## Review
		// already present" case is enforced inside Run (skip clone+claude,
		// publish NextPhase=ai_review). The previous "skip if ## Review
		// present" guard silently dropped the routing decision on retrigger.
		DescribeTable("always returns true so the routing decision is never skipped",
			func(content string) {
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.ShouldRun(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeTrue())
			},
			Entry("no review section", "# PR Review\n\nsome text"),
			Entry("review section present", "# PR Review\n\n## Review\n\n{}"),
			Entry("empty content", ""),
		)
	})

	Describe("Run — retrigger with existing ## Review (advance without re-cloning)", func() {
		// Reproduces the pattern from the trading#136 planning incident,
		// but in the execution phase: a previous trigger wrote ## Review,
		// next phase failed for any reason, controller reset trigger_count,
		// new pod runs execution. With the old skip-via-ShouldRun the routing
		// decision was dropped. The fix is to always run but short-circuit
		// to NextPhase=ai_review when ## Review is already in the body.
		It("publishes NextPhase=ai_review without invoking the repo manager or runner", func() {
			md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: abc123
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

## Review

prior review body
`)
			Expect(err).NotTo(HaveOccurred())
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("ai_review"))
			Expect(repoManager.EnsureWorktreeCallCount()).To(Equal(0))
		})
	})

	Describe("Run", func() {
		Context("when clone_url is missing from frontmatter", func() {
			It("returns AgentStatusFailed without propagating error", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nref: main\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("clone_url"))
			})
		})

		Context("when ref is missing from frontmatter", func() {
			It("returns AgentStatusFailed without propagating error", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nclone_url: https://github.com/example/repo.git\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("ref"))
			})
		})

		Context("when base_ref is missing from frontmatter", func() {
			It("returns AgentStatusFailed without propagating error", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nclone_url: https://github.com/example/repo.git\nref: main\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("base_ref"))
			})
		})

		Context("when EnsureWorktree returns an error", func() {
			It("propagates the error (fail loud)", func() {
				repoManager.EnsureWorktreeReturns("", fmt.Errorf("clone failed: network error"))

				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nclone_url: https://github.com/example/repo.git\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, runErr := step.Run(ctx, md)
				Expect(runErr).To(HaveOccurred())
				Expect(result).To(BeNil())
				Expect(runErr.Error()).To(ContainSubstring("ensure worktree"))
			})
		})

		Context("when EnsureWorktree fails with a git auth-failure error", func() {
			BeforeEach(func() {
				repoManager.EnsureWorktreeReturns(
					"",
					fmt.Errorf(
						"git clone --bare: fatal: could not read Username for 'https://github.com': terminal prompts disabled",
					),
				)
			})

			It("returns AgentStatusNeedsInput", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nclone_url: https://github.com/bborbe/trading.git\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			})

			It("diagnostic names host/owner/repo", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nclone_url: https://github.com/bborbe/trading.git\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, _ := step.Run(ctx, md)
				Expect(result.Message).To(ContainSubstring("github.com/bborbe/trading"))
			})

			It("diagnostic contains GH_TOKEN hint", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nclone_url: https://github.com/bborbe/trading.git\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, _ := step.Run(ctx, md)
				Expect(result.Message).To(ContainSubstring("GH_TOKEN"))
			})

			It("diagnostic does NOT leak the underlying git error (token non-leakage)", func() {
				// Inject a distinctive fake token into the underlying clone error.
				// The diagnostic uses a fixed template and must not echo err.Error(),
				// so the fake token must NOT appear in result.Message.
				const fakeToken = "FAKE_TOKEN_DO_NOT_LEAK_xyz123" //nolint:gosec // G101: test-only sentinel value, not a real credential
				repoManager.EnsureWorktreeReturns(
					"",
					fmt.Errorf(
						"git clone --bare: fatal: could not read Username for 'https://%s@github.com': terminal prompts disabled",
						fakeToken,
					),
				)
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nclone_url: https://github.com/bborbe/trading.git\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, runErr := step.Run(ctx, md)
				Expect(runErr).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
				Expect(result.Message).NotTo(ContainSubstring(fakeToken))
			})
		})

		Context("when EnsureWorktree fails with 'Repository not found'", func() {
			// GitHub returns this exact string for unauthenticated requests to private
			// repos. Intentionally classified as auth failure; known false-positive on
			// typo'd public repo URLs (operator can verify URL when re-triggering).
			BeforeEach(func() {
				repoManager.EnsureWorktreeReturns(
					"",
					fmt.Errorf(
						"git clone --bare: remote: Repository not found.\nfatal: repository 'https://github.com/bborbe/private.git/' not found",
					),
				)
			})

			It("returns AgentStatusNeedsInput", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nclone_url: https://github.com/bborbe/private.git\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, runErr := step.Run(ctx, md)
				Expect(runErr).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
				Expect(result.Message).To(ContainSubstring("github.com/bborbe/private"))
				Expect(result.Message).To(ContainSubstring("GH_TOKEN"))
			})
		})

		Context("when EnsureWorktree fails with a non-auth error", func() {
			BeforeEach(func() {
				repoManager.EnsureWorktreeReturns(
					"",
					fmt.Errorf(
						"git clone --bare: unable to access 'https://github.com/bborbe/foo.git/': Could not resolve host: github.com",
					),
				)
			})

			It("propagates the error (not NeedsInput)", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"---\nclone_url: https://github.com/bborbe/trading.git\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, runErr := step.Run(ctx, md)
				Expect(runErr).To(HaveOccurred())
				Expect(result).To(BeNil())
			})
		})

		Context("allowlist checks", func() {
			const taskMarkdown = "---\nclone_url: https://github.com/bborbe/maintainer.git\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n"

			Context("when allowlist is empty", func() {
				It("proceeds to EnsureWorktree (allow-all behavior)", func() {
					currentDateTime := libtime.NewCurrentDateTime()
					stepWithEmpty := pkg.NewCheckoutExecutionStep(
						repoManager,
						"",
						"agent",
						"sonnet",
						map[string]string{},
						claudelib.AllowedTools{"Read"},
						"standard",
						nil,
						nil,
						nil,
						currentDateTime,
					)
					repoManager.EnsureWorktreeReturns("", fmt.Errorf("stop here"))

					md, err := agentlib.ParseMarkdown(ctx, taskMarkdown)
					Expect(err).NotTo(HaveOccurred())
					_, runErr := stepWithEmpty.Run(ctx, md)
					Expect(repoManager.EnsureWorktreeCallCount()).To(Equal(1))
					Expect(runErr).To(HaveOccurred())
				})
			})

			Context("when allowlist is non-empty and clone_url matches", func() {
				It("proceeds to EnsureWorktree", func() {
					currentDateTime := libtime.NewCurrentDateTime()
					stepWithAllowlist := pkg.NewCheckoutExecutionStep(
						repoManager,
						"",
						"agent",
						"sonnet",
						map[string]string{},
						claudelib.AllowedTools{"Read"},
						"standard",
						[]string{"github.com/bborbe/maintainer"},
						nil,
						nil,
						currentDateTime,
					)
					repoManager.EnsureWorktreeReturns("", fmt.Errorf("stop here"))

					md, err := agentlib.ParseMarkdown(ctx, taskMarkdown)
					Expect(err).NotTo(HaveOccurred())
					_, runErr := stepWithAllowlist.Run(ctx, md)
					Expect(repoManager.EnsureWorktreeCallCount()).To(Equal(1))
					Expect(runErr).To(HaveOccurred())
				})
			})

			Context("when allowlist is non-empty and clone_url does NOT match", func() {
				It("returns NeedsInput and does not call EnsureWorktree", func() {
					currentDateTime := libtime.NewCurrentDateTime()
					stepWithAllowlist := pkg.NewCheckoutExecutionStep(
						repoManager,
						"",
						"agent",
						"sonnet",
						map[string]string{},
						claudelib.AllowedTools{"Read"},
						"standard",
						[]string{"github.com/bborbe/other-repo"},
						nil,
						nil,
						currentDateTime,
					)
					const nonMatchingTask = "---\nclone_url: https://github.com/bborbe/maintainer.git\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n"

					md, err := agentlib.ParseMarkdown(ctx, nonMatchingTask)
					Expect(err).NotTo(HaveOccurred())
					result, runErr := stepWithAllowlist.Run(ctx, md)
					Expect(runErr).NotTo(HaveOccurred())
					Expect(result).NotTo(BeNil())
					Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
					Expect(result.Message).To(ContainSubstring("github.com/bborbe/maintainer"))
					Expect(repoManager.EnsureWorktreeCallCount()).To(Equal(0))
				})
			})

			Context("when allowlist contains a wildcard and clone_url matches the owner", func() {
				It("permits the clone (wildcard match)", func() {
					currentDateTime := libtime.NewCurrentDateTime()
					stepWithWildcard := pkg.NewCheckoutExecutionStep(
						repoManager,
						"",
						"agent",
						"sonnet",
						map[string]string{},
						claudelib.AllowedTools{"Read"},
						"standard",
						[]string{"github.com/bborbe/*"},
						nil,
						nil,
						currentDateTime,
					)
					repoManager.EnsureWorktreeReturns("", fmt.Errorf("stop here"))

					md, err := agentlib.ParseMarkdown(ctx, taskMarkdown)
					Expect(err).NotTo(HaveOccurred())
					result, runErr := stepWithWildcard.Run(ctx, md)
					Expect(repoManager.EnsureWorktreeCallCount()).To(Equal(1))
					_ = result
					Expect(runErr).To(HaveOccurred())
					if result != nil {
						Expect(result.Status).NotTo(Equal(agentlib.AgentStatusNeedsInput),
							"wildcard allowlist should permit bborbe repo but got needs_input")
					}
				})
			})

			Context("when allowlist is non-empty and clone_url is unparseable", func() {
				It("returns Failed (not NeedsInput) and does not call EnsureWorktree", func() {
					currentDateTime := libtime.NewCurrentDateTime()
					stepWithAllowlist := pkg.NewCheckoutExecutionStep(
						repoManager,
						"",
						"agent",
						"sonnet",
						map[string]string{},
						claudelib.AllowedTools{"Read"},
						"standard",
						[]string{"github.com/bborbe/maintainer"},
						nil,
						nil,
						currentDateTime,
					)
					const badURLTask = "---\nclone_url: not-a-url\nref: main\nbase_ref: master\ntask_identifier: bd4d883b-0000-0000-0000-000000000001\n---\n# Task\n"

					md, err := agentlib.ParseMarkdown(ctx, badURLTask)
					Expect(err).NotTo(HaveOccurred())
					result, runErr := stepWithAllowlist.Run(ctx, md)
					Expect(runErr).NotTo(HaveOccurred())
					Expect(result).NotTo(BeNil())
					Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
					Expect(result.Message).To(ContainSubstring("failed to parse clone_url"))
					Expect(repoManager.EnsureWorktreeCallCount()).To(Equal(0))
				})
			})
		})
	})

	Describe("posting behavior", func() {
		const (
			prURL      = "https://github.com/bborbe/maintainer/pull/2"
			reviewBody = "LGTM. All checks pass.\n\n{\"verdict\":\"approve\",\"reason\":\"LGTM\"}"
			taskMD     = "---\nref: abc123\ntrigger_count: 1\n---\n\nReview the pull request at " + prURL + ".\n"
		)

		buildMD := func(ctx context.Context, reviewSection string) *agentlib.Markdown {
			md, err := agentlib.ParseMarkdown(ctx, taskMD)
			Expect(err).NotTo(HaveOccurred())
			if reviewSection != "" {
				md.ReplaceSection(agentlib.Section{
					Heading: "## Review",
					Body:    reviewSection,
				})
			}
			return md
		}

		fixedTime := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

		Context("when poster is nil", func() {
			It("advances to ai_review without calling any poster", func() {
				md := buildMD(ctx, reviewBody)
				result, err := pkg.PostAndRouteForTest(ctx, nil, md, prURL, "", fixedTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("ai_review"))
			})
		})

		Context("when post succeeds", func() {
			It("advances to ai_review and writes a success diagnostic", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 42})

				md := buildMD(ctx, reviewBody)
				result, err := pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("ai_review"))
				Expect(fakePoster.PostCallCount()).To(Equal(1))

				diagSection, ok := md.FindSection("## Diagnostics")
				Expect(ok).To(BeTrue())
				Expect(diagSection.Body).To(ContainSubstring("outcome: success"))
				Expect(diagSection.Body).To(ContainSubstring("review_id: 42"))
			})
		})

		Context("when post fails with a transient error", func() {
			It("escalates to human_review and writes a failure diagnostic", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{
					Outcome:      "failed",
					Class:        pkg.ErrorClassTransient,
					ErrorMessage: "timeout",
					FailureStep:  "post",
				})

				md := buildMD(ctx, reviewBody)
				result, err := pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("human_review"))
				Expect(result.Message).To(ContainSubstring("posting failed"))

				diagSection, ok := md.FindSection("## Diagnostics")
				Expect(ok).To(BeTrue())
				Expect(diagSection.Body).To(ContainSubstring("class: transient"))
			})
		})

		Context("when post returns not-a-failure class (e.g. 422 PR closed)", func() {
			It("advances to ai_review", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{
					Outcome: "success",
					Class:   pkg.ErrorClassNotAFailure,
				})

				md := buildMD(ctx, reviewBody)
				result, err := pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("ai_review"))
			})
		})

		Context("## Review vault preserved regardless of poster outcome", func() {
			DescribeTable("review body unchanged for every ErrorClass",
				func(class pkg.ErrorClass, outcome string) {
					fakePoster := &mocks.PrPoster{}
					postResult := pkg.PostResult{
						Outcome: outcome,
						Class:   class,
					}
					if outcome == "failed" {
						postResult.ErrorMessage = "test error"
					}
					fakePoster.PostReturns(postResult)

					md := buildMD(ctx, reviewBody)
					_, err := pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime)
					Expect(err).NotTo(HaveOccurred())

					reviewSection, ok := md.FindSection("## Review")
					Expect(ok).To(BeTrue())
					Expect(reviewSection.Body).To(Equal(reviewBody))
				},
				Entry("transient failure", pkg.ErrorClassTransient, "failed"),
				Entry("permanent failure", pkg.ErrorClassPermanent, "failed"),
				Entry("unknown failure", pkg.ErrorClassUnknown, "failed"),
				Entry("not-a-failure", pkg.ErrorClassNotAFailure, "success"),
				Entry("soft-warning", pkg.ErrorClassSoftWarning, "success"),
			)
		})

		Context("diagnostic blocks are append-only", func() {
			It("second run appends after the first block", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 1})

				md := buildMD(ctx, reviewBody)

				// First posting attempt.
				_, err := pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime)
				Expect(err).NotTo(HaveOccurred())

				// Second posting attempt (simulate controller re-spawn).
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 2})
				_, err = pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime.Add(time.Minute),
				)
				Expect(err).NotTo(HaveOccurred())

				diagSection, ok := md.FindSection("## Diagnostics")
				Expect(ok).To(BeTrue())
				Expect(diagSection.Body).To(ContainSubstring("review_id: 1"))
				Expect(diagSection.Body).To(ContainSubstring("review_id: 2"))
			})
		})
	})

	Describe("ExtractPRURL", func() {
		DescribeTable("extracts PR URL from markdown",
			func(body string, expected string) {
				md, err := agentlib.ParseMarkdown(ctx, body)
				Expect(err).NotTo(HaveOccurred())
				Expect(pkg.ExtractPRURL(md)).To(Equal(expected))
			},
			// Load-bearing regression test: watcher format puts URL in H1 body, not preamble.
			// Pre-fix code only scanned md.Preamble (always empty in this layout) and failed.
			Entry(
				"URL in H1 section body (watcher format — regression)",
				"# PR Review: test\n\nhttps://github.com/bborbe/maintainer/pull/2\n## Plan\n\nbody",
				"https://github.com/bborbe/maintainer/pull/2",
			),
			Entry(
				"URL in H1 section body — generic owner/repo",
				"# H1\n\nhttps://github.com/owner/repo/pull/42\n## Plan",
				"https://github.com/owner/repo/pull/42",
			),
			// Pre-fix code handled this correctly (URL in preamble): ensure no regression.
			Entry(
				"URL in preamble — no H1",
				"https://github.com/owner/repo/pull/1\n\n## Plan",
				"https://github.com/owner/repo/pull/1",
			),
			// URL after the first H2 must NOT be matched (Claude-authored body).
			Entry(
				"URL only after H2 — not matched",
				"# H1\n\n## Plan\n\nhttps://github.com/owner/repo/pull/1",
				"",
			),
			Entry(
				"no URL anywhere",
				"# H1 only\n\nno url here\n## Plan",
				"",
			),
		)
	})
})
