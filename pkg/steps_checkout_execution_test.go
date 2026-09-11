// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
			nil,
			libtime.Duration(25*time.Minute),
			nil,
			pkg.DefaultReviewChunkConfig(),
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
						nil,
						libtime.Duration(25*time.Minute),
						nil,
						pkg.DefaultReviewChunkConfig(),
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
						nil,
						libtime.Duration(25*time.Minute),
						nil,
						pkg.DefaultReviewChunkConfig(),
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
						nil,
						libtime.Duration(25*time.Minute),
						nil,
						pkg.DefaultReviewChunkConfig(),
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
						nil,
						libtime.Duration(25*time.Minute),
						nil,
						pkg.DefaultReviewChunkConfig(),
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
						nil,
						libtime.Duration(25*time.Minute),
						nil,
						pkg.DefaultReviewChunkConfig(),
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
				result, err := pkg.PostAndRouteForTest(ctx, nil, md, prURL, "", fixedTime, true)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("ai_review"))
			})
		})

		Context("fail-closed gate when the mechanical funnel did not run", func() {
			It("overrides an approve verdict to request-changes (funnelRan=false)", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 7})

				md := buildMD(ctx, reviewBody) // reviewBody parses to approve
				result, err := pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime,
					false,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("ai_review"))

				Expect(fakePoster.PostCallCount()).To(Equal(1))
				_, req := fakePoster.PostArgsForCall(0)
				Expect(req.Verdict).To(Equal(pkg.VerdictRequestChanges))
			})

			It("leaves an approve verdict untouched when the funnel ran (funnelRan=true)", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 8})

				md := buildMD(ctx, reviewBody)
				_, err := pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime, true)
				Expect(err).NotTo(HaveOccurred())

				_, req := fakePoster.PostArgsForCall(0)
				Expect(req.Verdict).To(Equal(pkg.VerdictApprove))
			})
		})

		Context("fail-closed gate when a concern is flagged not verified", func() {
			// funnelRan=true so the funnel gate does not interfere — the unverified-
			// concerns gate must be what demotes the approve.
			It("fail-closes an approve carrying an unverified concern to request-changes", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 9})

				md := buildMD(ctx,
					"LGTM.\n\n```json\n"+
						`{"verdict":"approve","reason":"looks ok","concerns_addressed":["security: rate-limit not verified — stopped at the time budget"]}`+
						"\n```\n")
				result, err := pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime,
					true,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("ai_review"))

				Expect(fakePoster.PostCallCount()).To(Equal(1))
				_, req := fakePoster.PostArgsForCall(0)
				Expect(req.Verdict).To(Equal(pkg.VerdictRequestChanges))
			})

			It("leaves an approve with no unverified concerns untouched (no over-trigger)", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 10})

				md := buildMD(ctx,
					"LGTM.\n\n```json\n"+
						`{"verdict":"approve","reason":"clean","concerns_addressed":["security: rate-limit addressed in handler.go:45"]}`+
						"\n```\n")
				_, err := pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime,
					true,
				)
				Expect(err).NotTo(HaveOccurred())

				_, req := fakePoster.PostArgsForCall(0)
				Expect(req.Verdict).To(Equal(pkg.VerdictApprove))
			})

			// Object-shape rows: the disposition field is authoritative at the
			// posting boundary too — funnelRan=true so the funnel gate does not
			// interfere.
			It("fail-closes an object-shape not-verified concern to request-changes", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 11})

				md := buildMD(ctx,
					"LGTM.\n\n```json\n"+
						`{"verdict":"approve","reason":"looks ok","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not-verified"}]}`+
						"\n```\n")
				result, err := pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime,
					true,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("ai_review"))

				Expect(fakePoster.PostCallCount()).To(Equal(1))
				_, req := fakePoster.PostArgsForCall(0)
				Expect(req.Verdict).To(Equal(pkg.VerdictRequestChanges))
			})

			// Posting-level regression lock for the 2026-08-30
			// bborbe/discord-assistant#37 incident: the run-2 object shape with
			// disposition `not-an-issue` (prose contains `not verified`) must post
			// as an approval.
			It("posts an object-shape not-an-issue approve untouched (incident shape)", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 12})

				md := buildMD(ctx,
					"LGTM.\n\n```json\n"+
						`{"verdict":"approve","reason":"looks ok","concerns_addressed":[{"concern":"tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested","disposition":"not-an-issue"}]}`+
						"\n```\n")
				_, err := pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime,
					true,
				)
				Expect(err).NotTo(HaveOccurred())

				Expect(fakePoster.PostCallCount()).To(Equal(1))
				_, req := fakePoster.PostArgsForCall(0)
				Expect(req.Verdict).To(Equal(pkg.VerdictApprove))
			})

			// Budget-keyed posting boundary (the permanent replacement for the
			// deleted RED probe): the elapsed reaches the gate through the real
			// demotion site. The nuke#216 body on a run that finished well inside
			// its 30m budget must post APPROVED — the regression that must never
			// come back — while the same admission on a budget-heavy run still
			// fail-closes. These rows assert the VERDICT the poster receives, so
			// the budget-keyed gate is locked at the posting boundary.
			It(
				"posts the nuke#216 body as approve on a short run (budget 30m, elapsed 2m)",
				func() {
					body, err := os.ReadFile("testdata/review_bborbe_nuke_216_run1.md")
					Expect(err).NotTo(HaveOccurred())
					fakePoster := &mocks.PrPoster{}
					fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 17})

					md := buildMD(ctx, string(body))
					_, err = pkg.PostAndRouteWithBudgetForTest(
						ctx,
						fakePoster,
						md,
						prURL,
						"",
						fixedTime,
						true,
						libtime.Duration(30*time.Minute),
						2*time.Minute,
					)
					Expect(err).NotTo(HaveOccurred())

					Expect(fakePoster.PostCallCount()).To(Equal(1))
					_, req := fakePoster.PostArgsForCall(0)
					Expect(req.Verdict).To(Equal(pkg.VerdictApprove))
				},
			)

			It(
				"fail-closes the toolchain body on a budget-heavy run (budget 30m, elapsed 27m)",
				func() {
					fakePoster := &mocks.PrPoster{}
					fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 18})

					md := buildMD(ctx,
						"LGTM.\n\n```json\n"+
							`{"verdict":"approve","reason":"clean","concerns_addressed":[{"concern":"correctness: go.mod go directive 1.27.0 dep compatibility","detail":"not verified - module files internally consistent (tidy ran, no downgrades, all hashes present) but transitive go-directive compatibility requires a Go 1.27 toolchain not available in the review sandbox; repo CI precommit (go mod tidy/verify + build) is the gate","disposition":"not-verified"}]}`+
							"\n```\n")
					result, err := pkg.PostAndRouteWithBudgetForTest(
						ctx,
						fakePoster,
						md,
						prURL,
						"",
						fixedTime,
						true,
						libtime.Duration(30*time.Minute),
						27*time.Minute,
					)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.NextPhase).To(Equal("ai_review"))

					Expect(fakePoster.PostCallCount()).To(Equal(1))
					_, req := fakePoster.PostArgsForCall(0)
					Expect(req.Verdict).To(Equal(pkg.VerdictRequestChanges))
				},
			)
		})

		Context("fail-closed gate when a comment is blocking", func() {
			// funnelRan=true so the funnel gate does not interfere — the blocking
			// gate must be what demotes the approve.
			It("fail-closes an approve carrying a blocking comment to request-changes", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 13})

				md := buildMD(ctx,
					"LGTM.\n\n```json\n"+
						`{"verdict":"approve","reason":"looks ok","comments":[{"file":"main.go","line":9,"severity":"nit","blocking":true,"blocking_reason":"isHealthy() is inverted","message":"real defect"}]}`+
						"\n```\n")
				result, err := pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime,
					true,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("ai_review"))

				Expect(fakePoster.PostCallCount()).To(Equal(1))
				_, req := fakePoster.PostArgsForCall(0)
				Expect(req.Verdict).To(Equal(pkg.VerdictRequestChanges))
			})

			// Explicit blocking:false wins over severity even when the severity is
			// critical — pre-existing debt the model consciously accepted.
			It(
				"posts an approve with a blocking:false critical comment untouched (pre-existing debt)",
				func() {
					fakePoster := &mocks.PrPoster{}
					fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 14})

					md := buildMD(ctx,
						"LGTM.\n\n```json\n"+
							`{"verdict":"approve","reason":"looks ok","comments":[{"file":"main.go","line":9,"severity":"critical","blocking":false,"blocking_reason":"accepted debt","message":"known issue"}]}`+
							"\n```\n")
					_, err := pkg.PostAndRouteForTest(
						ctx,
						fakePoster,
						md,
						prURL,
						"",
						fixedTime,
						true,
					)
					Expect(err).NotTo(HaveOccurred())

					_, req := fakePoster.PostArgsForCall(0)
					Expect(req.Verdict).To(Equal(pkg.VerdictApprove))
				},
			)

			// A comment carrying neither blocking nor severity is skipped per-entry —
			// the gate must not over-trigger on comments without the new field.
			It(
				"posts an approve when comments carry neither blocking nor severity (per-entry skip)",
				func() {
					fakePoster := &mocks.PrPoster{}
					fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 15})

					md := buildMD(ctx,
						"LGTM.\n\n```json\n"+
							`{"verdict":"approve","reason":"looks ok","comments":[{"file":"main.go","line":9,"message":"note only"}]}`+
							"\n```\n")
					_, err := pkg.PostAndRouteForTest(
						ctx,
						fakePoster,
						md,
						prURL,
						"",
						fixedTime,
						true,
					)
					Expect(err).NotTo(HaveOccurred())

					_, req := fakePoster.PostArgsForCall(0)
					Expect(req.Verdict).To(Equal(pkg.VerdictApprove))
				},
			)
		})

		Context("chain-precedence: funnel gate fires before the blocking gate", func() {
			// funnelRan=false: the funnel gate demotes the approve first (to
			// ReasonFunnelDidNotRun), so the blocking gate — composed after it —
			// never gets a chance to rewrite the verdict. PostRequest carries only
			// Verdict, not Reason, so the posted verdict is all this test can
			// assert; the reason-preservation contract is asserted at the
			// pure-function level by the ApplyBlockingGate unit test in
			// verdict_blocking_test.go.
			It(
				"demotes the blocking-comment approve to request-changes via the funnel gate",
				func() {
					fakePoster := &mocks.PrPoster{}
					fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 16})

					md := buildMD(ctx,
						"LGTM.\n\n```json\n"+
							`{"verdict":"approve","reason":"looks ok","comments":[{"file":"main.go","line":9,"severity":"nit","blocking":true,"blocking_reason":"isHealthy() is inverted","message":"real defect"}]}`+
							"\n```\n")
					result, err := pkg.PostAndRouteForTest(
						ctx,
						fakePoster,
						md,
						prURL,
						"",
						fixedTime,
						false,
					)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.NextPhase).To(Equal("ai_review"))

					Expect(fakePoster.PostCallCount()).To(Equal(1))
					_, req := fakePoster.PostArgsForCall(0)
					Expect(req.Verdict).To(Equal(pkg.VerdictRequestChanges))
				},
			)
		})

		Context("when post succeeds", func() {
			It("advances to ai_review and writes a success diagnostic", func() {
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 42})

				md := buildMD(ctx, reviewBody)
				result, err := pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime,
					true,
				)
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
				result, err := pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime,
					true,
				)
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
				result, err := pkg.PostAndRouteForTest(
					ctx,
					fakePoster,
					md,
					prURL,
					"",
					fixedTime,
					true,
				)
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
					_, err := pkg.PostAndRouteForTest(
						ctx,
						fakePoster,
						md,
						prURL,
						"",
						fixedTime,
						true,
					)
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
				_, err := pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime, true)
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
					true,
				)
				Expect(err).NotTo(HaveOccurred())

				diagSection, ok := md.FindSection("## Diagnostics")
				Expect(ok).To(BeTrue())
				Expect(diagSection.Body).To(ContainSubstring("review_id: 1"))
				Expect(diagSection.Body).To(ContainSubstring("review_id: 2"))
			})
		})
	})

	Describe("soft time budget expiry", func() {
		// AC 2: a budget-terminated execution run routes to human_review with a
		// budget-naming message BEFORE writing ## Review or posting — never to
		// the failed/controller-retry path.
		It("routes to human_review without writing ## Review or posting", func() {
			tmpDir, err := os.MkdirTemp("", "exec-budget-*")
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(tmpDir)).To(Succeed())
			}()

			cmdDir := filepath.Join(tmpDir, "plugins", "marketplaces", "coding", "commands")
			Expect(os.MkdirAll(cmdDir, 0750)).To(Succeed())
			Expect(os.WriteFile(
				filepath.Join(cmdDir, "pr-review.md"),
				[]byte(
					"---\ndescription: Test plugin\nallowed-tools: Task\n---\n# PR Review\n\nProcedure body.\n",
				),
				0600,
			)).To(Succeed())

			fakeRunner := &mocks.ClaudeRunnerMock{}
			fakeRunner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
				<-runCtx.Done() // block until the soft budget deadline fires
				return nil, runCtx.Err()
			}

			repoManager.EnsureWorktreeReturns("/work/test", nil)

			currentDateTime := libtime.NewCurrentDateTime()
			budgetStep := pkg.NewCheckoutExecutionStep(
				repoManager,
				claudelib.ClaudeConfigDir(tmpDir),
				"agent",
				"sonnet",
				map[string]string{},
				claudelib.AllowedTools{"Read"},
				"standard",
				nil,
				nil,
				nil,
				currentDateTime,
				fakeRunner,
				libtime.Duration(20*time.Millisecond),
				nil,
				pkg.DefaultReviewChunkConfig(),
			)

			md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: abc123
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())

			result, err := budgetStep.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))
			Expect(result.Message).To(ContainSubstring("soft time budget"))
			Expect(result.Message).To(ContainSubstring("20ms"))
			Expect(repoManager.EnsureWorktreeCallCount()).To(Equal(1))
			// Budget-terminated runs never write ## Review and never post.
			_, exists := md.FindSection("## Review")
			Expect(exists).To(BeFalse())
			_, exists = md.FindSection("## Diagnostics")
			Expect(exists).To(BeFalse())
		})

		Context("salvage", func() {
			// AC 5: a budget-terminated execution run with a captured streamed
			// partial persists it under the distinct ## Salvage heading (never
			// ## Review) and never posts — the budget path returns BEFORE
			// postAndRoute (AC 6 never-posts).
			It("persists the captured partial under ## Salvage and never posts", func() {
				tmpDir, err := os.MkdirTemp("", "exec-salvage-*")
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(os.RemoveAll(tmpDir)).To(Succeed())
				}()

				cmdDir := filepath.Join(tmpDir, "plugins", "marketplaces", "coding", "commands")
				Expect(os.MkdirAll(cmdDir, 0750)).To(Succeed())
				Expect(os.WriteFile(
					filepath.Join(cmdDir, "pr-review.md"),
					[]byte(
						"---\ndescription: Test plugin\nallowed-tools: Task\n---\n# PR Review\n\nProcedure body.\n",
					),
					0600,
				)).To(Succeed())

				fakeRunner := &mocks.ClaudeRunnerMock{}
				fakeRunner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
					<-runCtx.Done() // block until the soft budget deadline fires
					// A killed run returns the bounded streamed partial alongside the
					// fired-deadline error — the capture shape ExtractBudgetPartial reads.
					return &claudelib.ClaudeResult{Partial: "partial review output"}, runCtx.Err()
				}
				fakePoster := &mocks.PrPoster{}

				repoManager.EnsureWorktreeReturns("/work/test", nil)

				currentDateTime := libtime.NewCurrentDateTime()
				budgetStep := pkg.NewCheckoutExecutionStep(
					repoManager,
					claudelib.ClaudeConfigDir(tmpDir),
					"agent",
					"sonnet",
					map[string]string{},
					claudelib.AllowedTools{"Read"},
					"standard",
					nil,
					fakePoster,
					nil,
					currentDateTime,
					fakeRunner,
					libtime.Duration(20*time.Millisecond),
					nil,
					pkg.DefaultReviewChunkConfig(),
				)

				md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: abc123
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
				Expect(err).NotTo(HaveOccurred())

				result, err := budgetStep.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))
				// The partial is persisted under ## Salvage, clearly marked incomplete.
				section, exists := md.FindSection("## Salvage")
				Expect(exists).To(BeTrue())
				Expect(section.Body).To(ContainSubstring("Incomplete"))
				Expect(section.Body).To(ContainSubstring("partial review output"))
				// ## Review is never written and nothing is ever posted on the budget path.
				_, exists = md.FindSection("## Review")
				Expect(exists).To(BeFalse())
				Expect(fakePoster.PostCallCount()).To(Equal(0))
			})

			// AC 5 negative: an empty capture must not produce a ## Salvage section —
			// the salvage write is a no-op, yet the run still routes to human_review.
			It("writes no ## Salvage when the run captured nothing", func() {
				tmpDir, err := os.MkdirTemp("", "exec-salvage-*")
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					Expect(os.RemoveAll(tmpDir)).To(Succeed())
				}()

				cmdDir := filepath.Join(tmpDir, "plugins", "marketplaces", "coding", "commands")
				Expect(os.MkdirAll(cmdDir, 0750)).To(Succeed())
				Expect(os.WriteFile(
					filepath.Join(cmdDir, "pr-review.md"),
					[]byte(
						"---\ndescription: Test plugin\nallowed-tools: Task\n---\n# PR Review\n\nProcedure body.\n",
					),
					0600,
				)).To(Succeed())

				fakeRunner := &mocks.ClaudeRunnerMock{}
				fakeRunner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
					<-runCtx.Done() // block until the soft budget deadline fires
					return nil, runCtx.Err()
				}
				fakePoster := &mocks.PrPoster{}

				repoManager.EnsureWorktreeReturns("/work/test", nil)

				currentDateTime := libtime.NewCurrentDateTime()
				budgetStep := pkg.NewCheckoutExecutionStep(
					repoManager,
					claudelib.ClaudeConfigDir(tmpDir),
					"agent",
					"sonnet",
					map[string]string{},
					claudelib.AllowedTools{"Read"},
					"standard",
					nil,
					fakePoster,
					nil,
					currentDateTime,
					fakeRunner,
					libtime.Duration(20*time.Millisecond),
					nil,
					pkg.DefaultReviewChunkConfig(),
				)

				md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: abc123
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
				Expect(err).NotTo(HaveOccurred())

				result, err := budgetStep.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))
				Expect(result.Message).To(ContainSubstring("soft time budget"))
				// Empty capture → salvage is a no-op.
				_, exists := md.FindSection("## Salvage")
				Expect(exists).To(BeFalse())
				Expect(fakePoster.PostCallCount()).To(Equal(0))
			})
		})
	})

	Describe("advanceIfAlreadyReviewed with a salvaged partial", func() {
		// AC 6: the ## Review-present idempotency guard must NEVER fire on a
		// salvaged partial. A budget-terminated run persists its partial under
		// ## Salvage (a heading deliberately distinct from ## Review), so a
		// partial can never advance into ai_review on a later trigger.
		It("returns nil when the task holds only a ## Salvage section (no ## Review)", func() {
			md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: abc123
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
## Salvage

_Incomplete: this run was terminated at the soft time budget before producing a final result._

partial review text
`)
			Expect(err).NotTo(HaveOccurred())
			Expect(pkg.AdvanceIfAlreadyReviewedForTest(md)).To(BeNil())
		})

		It("returns the done/ai_review result when ## Review is present (regression)", func() {
			md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: abc123
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
## Review

complete review body
`)
			Expect(err).NotTo(HaveOccurred())
			result := pkg.AdvanceIfAlreadyReviewedForTest(md)
			Expect(result).NotTo(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("ai_review"))
		})

		It(
			"returns the done/ai_review result when both ## Salvage and ## Review are present",
			func() {
				// A stale salvage from an earlier budget-terminated trigger must not
				// block a completed ## Review from advancing.
				md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: abc123
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
## Salvage

_Incomplete: partial output._

partial review text

## Review

complete review body
`)
				Expect(err).NotTo(HaveOccurred())
				result := pkg.AdvanceIfAlreadyReviewedForTest(md)
				Expect(result).NotTo(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("ai_review"))
			},
		)
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

var _ = Describe("checkoutExecutionStep chunked review", func() {
	var (
		ctx         context.Context
		tmpDir      string
		repoManager *mocks.RepoManager
	)

	// twoChunkInventory partitions into exactly two chunks under the default
	// config: 301 + 300 added lines is above the 500 engage threshold, and the
	// second file does not fit alongside the first under the 300 max-additions
	// bound.
	twoChunkFunnel := func() pkg.FunnelResult {
		return pkg.FunnelResult{
			Ran: true,
			FindingsJSON: `{"stats":{"yamls_run":1,"findings_count":1,"elapsed_ms":1},` +
				`"findings_by_owner":{"go-error-assistant":[{"rule_id":"r1","file":"a.go","line":3}]},` +
				`"errors":[]}`,
			ChangedFiles: []pkg.ChangedFile{
				{Path: "a.go", Additions: 301},
				{Path: "b.go", Additions: 300},
			},
		}
	}

	// chunkApproveWithUnverifiedConcern is one chunk's output: a clean approve
	// carrying a single not-verified concern, so the merged body still trips the
	// concerns gate when the summed elapsed crosses the budget fraction.
	const chunkApproveWithUnverifiedConcern = "chunk body\n\n```json\n" +
		`{"verdict":"approve","reason":"ok","concerns_addressed":[{"concern":"c1","disposition":"not-verified"}]}` +
		"\n```\n"

	buildMD := func() *agentlib.Markdown {
		md, err := agentlib.ParseMarkdown(ctx, `---
clone_url: https://github.com/bborbe/maintainer.git
ref: abc123
base_ref: main
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
		Expect(err).NotTo(HaveOccurred())
		return md
	}

	newStep := func(
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
			nil,
			pkg.DefaultReviewChunkConfig(),
		)
	}

	BeforeEach(func() {
		ctx = context.Background()
		repoManager = &mocks.RepoManager{}
		repoManager.EnsureWorktreeReturns("/work/test", nil)

		var err error
		tmpDir, err = os.MkdirTemp("", "exec-chunk-*")
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

	It("salvages the cut-off chunk and routes to human_review when a chunk deadline fires", func() {
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			<-runCtx.Done() // block until the chunk's share of the budget fires
			return &claudelib.ClaudeResult{Partial: "partial chunk output"}, runCtx.Err()
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(twoChunkFunnel(), nil)
		fakePoster := &mocks.PrPoster{}

		step := newStep(fakeRunner, fakePoster, funnelRunner, libtime.Duration(20*time.Millisecond))

		md := buildMD()
		result, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(result.NextPhase).To(Equal("human_review"))

		// The cut-off chunk's partial is salvaged under ## Salvage, naming the chunk.
		section, exists := md.FindSection("## Salvage")
		Expect(exists).To(BeTrue())
		Expect(section.Body).To(ContainSubstring("Chunk "))
		Expect(section.Body).To(ContainSubstring("Chunk 1/2"))
		Expect(section.Body).To(ContainSubstring("partial chunk output"))

		// No ## Review is written — the run never reaches the merge.
		_, exists = md.FindSection("## Review")
		Expect(exists).To(BeFalse())
		Expect(fakeRunner.RunCallCount()).To(BeNumerically("<", 2))
		// The clone and the funnel each ran exactly once, never per chunk.
		Expect(repoManager.EnsureWorktreeCallCount()).To(Equal(1))
		Expect(funnelRunner.RunCallCount()).To(Equal(1))
		Expect(fakePoster.PostCallCount()).To(Equal(0))
	})

	It(
		"keeps the failed path with no ## Review and no ## Salvage on a non-deadline chunk error",
		func() {
			fakeRunner := &mocks.ClaudeRunnerMock{}
			fakeRunner.RunStub = func(_ context.Context, _ string) (*claudelib.ClaudeResult, error) {
				return nil, fmt.Errorf("runner exploded")
			}
			funnelRunner := &mocks.FunnelRunner{}
			funnelRunner.RunReturns(twoChunkFunnel(), nil)

			step := newStep(fakeRunner, nil, funnelRunner, libtime.Duration(25*time.Minute))

			md := buildMD()
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("execution claude run failed"))

			_, exists := md.FindSection("## Review")
			Expect(exists).To(BeFalse())
			_, exists = md.FindSection("## Salvage")
			Expect(exists).To(BeFalse())
		},
	)

	It("runs one unscoped review when the added-line counts could not be computed", func() {
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, _ string) (*claudelib.ClaudeResult, error) {
			return &claudelib.ClaudeResult{
				Result: "review body\n\n```json\n{\"verdict\":\"approve\",\"reason\":\"ok\"}\n```\n",
			}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(pkg.FunnelResult{
			Ran:             true,
			FindingsJSON:    `{"stats":{"yamls_run":0,"findings_count":0,"elapsed_ms":0},"findings_by_owner":{},"errors":[]}`,
			InventoryDetail: "could not compute added-line counts for base_ref main",
		}, nil)

		step := newStep(fakeRunner, nil, funnelRunner, libtime.Duration(25*time.Minute))

		md := buildMD()
		result, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(result.NextPhase).To(Equal("ai_review"))

		// Exactly one runner call: the review ran once, unscoped.
		Expect(fakeRunner.RunCallCount()).To(Equal(1))
		section, exists := md.FindSection("## Review")
		Expect(exists).To(BeTrue())
		Expect(section.Body).To(ContainSubstring("review body"))
	})

	It("neutralizes code fences in the chunk's file paths before they reach the prompt", func() {
		const rawPath = "a```b.go"
		var prompts []string
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			prompts = append(prompts, prompt)
			return &claudelib.ClaudeResult{
				Result: "review body\n\n```json\n{\"verdict\":\"approve\",\"reason\":\"ok\"}\n```\n",
			}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelResult := twoChunkFunnel()
		funnelResult.ChangedFiles = []pkg.ChangedFile{
			{Path: rawPath, Additions: 301},
			{Path: "b.go", Additions: 300},
		}
		funnelRunner.RunReturns(funnelResult, nil)

		step := newStep(fakeRunner, nil, funnelRunner, libtime.Duration(25*time.Minute))

		result, err := step.Run(ctx, buildMD())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(prompts).To(HaveLen(2))

		// The PR-author-controlled path reaches the prompt neutralized, never raw.
		Expect(prompts[0]).To(ContainSubstring("a[code-fence]b.go"))
		Expect(prompts[0]).NotTo(ContainSubstring(rawPath))
	})

	It("hands the concerns gate the SUM of the chunk runs' elapsed time", func() {
		// Per-chunk sleep 100ms under a 220ms budget: each chunk's deadline is the
		// outer deadline (the 60s floor caps there), so both complete, but the
		// summed ~200ms crosses 0.8 × 220ms = 176ms while a single chunk's ~100ms
		// does not. The merged approve carries a not-verified concern, so the
		// summed elapsed is what demotes it.
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, _ string) (*claudelib.ClaudeResult, error) {
			time.Sleep(100 * time.Millisecond)
			return &claudelib.ClaudeResult{Result: chunkApproveWithUnverifiedConcern}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(twoChunkFunnel(), nil)
		fakePoster := &mocks.PrPoster{}
		fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 1})

		step := newStep(
			fakeRunner,
			fakePoster,
			funnelRunner,
			libtime.Duration(220*time.Millisecond),
		)

		md := buildMD()
		result, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		// One runner call per chunk, and the merged body carries exactly one
		// verdict block plus the per-chunk concern.
		Expect(fakeRunner.RunCallCount()).To(Equal(2))
		section, exists := md.FindSection("## Review")
		Expect(exists).To(BeTrue())
		Expect(section.Body).To(ContainSubstring("### Chunk 1/2"))
		Expect(section.Body).To(ContainSubstring("### Chunk 2/2"))
		Expect(section.Body).To(ContainSubstring("not-verified"))

		// The summed elapsed demoted the merged approve to request-changes.
		Expect(fakePoster.PostCallCount()).To(Equal(1))
		_, postReq := fakePoster.PostArgsForCall(0)
		Expect(postReq.Verdict).To(Equal(pkg.VerdictRequestChanges))
	})
})
