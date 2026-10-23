// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
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

		Context("no-review gate: a run that produced no review posts nothing", func() {
			// Regression lock for bborbe/nuke#446 (2026-10-11). A run whose result
			// was the literal `(tool 'WebFetch' unavailable)` carries no verdict
			// block, so ParseVerdict fail-closes to request-changes. Posting that
			// published a CHANGES_REQUESTED review the model never wrote — which
			// blocks the merge exactly like a real change request. The gate must
			// suppress the post for exactly the two "no review at all" reasons, and
			// for nothing else.
			It("posts nothing and routes to human_review for the crash body", func() {
				fakePoster := &mocks.PrPoster{}

				md := buildMD(ctx, "(tool 'WebFetch' unavailable)")
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
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))

				// The whole point: nothing reaches GitHub, so no false blocking
				// review can hold the merge.
				Expect(fakePoster.PostCallCount()).To(Equal(0))

				// The skip stays visible rather than failing silently, and is not
				// mislabelled as a failure.
				diag, ok := md.FindSection("## Diagnostics")
				Expect(ok).To(BeTrue())
				Expect(diag.Body).To(ContainSubstring("outcome: skipped"))
				Expect(diag.Body).To(ContainSubstring("no_review_produced"))
				Expect(diag.Body).NotTo(ContainSubstring("outcome: failed"))
			})

			It("posts nothing when the ## Review section is absent (empty review text)", func() {
				fakePoster := &mocks.PrPoster{}

				md := buildMD(ctx, "") // buildMD skips ReplaceSection -> no ## Review
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
				Expect(fakePoster.PostCallCount()).To(Equal(0))
			})

			It("still posts a legitimate fail-closed demotion (unknown verdict)", func() {
				// Over-trigger guard. `unknown verdict` is a fail-closed reason, but
				// the model DID write a review — it must post as request-changes, not
				// be swallowed by the no-review gate.
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 11})

				md := buildMD(ctx, "Some review prose.\n\n{\"verdict\":\"maybe\"}")
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

			It("still posts a real review that omitted the JSON verdict block", func() {
				// The sharpest over-trigger guard, and the regression this gate must
				// not cause. A body whose only content is a `### Must Fix` section
				// parses to the SAME reason as the crash marker (`no verdict block`),
				// but the model DID find must-fix issues — suppressing it would hide
				// them, which is worse than the false blocking review being fixed.
				fakePoster := &mocks.PrPoster{}
				fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 12})

				md := buildMD(ctx,
					"### Must Fix (Critical)\n\n- Security issue in authentication")
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

// chunkHeadingPattern matches the one "### Chunk <i>/<n>" section heading
// MergeChunkReviews emits per chunk, so a merged body's section count is
// countable exactly.
var chunkHeadingPattern = regexp.MustCompile(`(?m)^### Chunk `)

// chunkIndexPattern matches the 1-based chunk index named in the prompt's
// chunk-scope preamble ("chunk 1/7").
var chunkIndexPattern = regexp.MustCompile(`chunk (\d+)/`)

// chunkIndexFromPrompt returns the 1-based chunk index named in the prompt, or
// 0 when the prompt carries no chunk-scope preamble.
func chunkIndexFromPrompt(prompt string) int {
	match := chunkIndexPattern.FindStringSubmatch(prompt)
	if match == nil {
		return 0
	}
	idx, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return idx
}

// fakeClock is a mutex-guarded settable clock for the step's
// libtime.CurrentDateTimeGetter.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now implements libtime.CurrentDateTimeGetter.
func (c *fakeClock) Now() libtime.DateTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return libtime.DateTime(c.now)
}

// Set replaces the clock's current time.
func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// captureGlog redirects glog's stderr output into a pipe for the duration of
// fn and returns everything written. glog binds -logtostderr and -v to its
// logging struct via flag.Var/flag.BoolVar at init, so flag.Set changes them
// at runtime; V(2) lines require -v=2. Restore both flags afterwards.
func captureGlog(fn func()) string {
	vFlag := flag.Lookup("v")
	logToStderrFlag := flag.Lookup("logtostderr")
	oldV := vFlag.Value.String()
	oldLogToStderr := logToStderrFlag.Value.String()
	Expect(flag.Set("logtostderr", "true")).To(Succeed())
	Expect(flag.Set("v", "2")).To(Succeed())
	defer func() {
		Expect(flag.Set("logtostderr", oldLogToStderr)).To(Succeed())
		Expect(flag.Set("v", oldV)).To(Succeed())
	}()

	oldStderr := os.Stderr
	reader, writer, err := os.Pipe()
	Expect(err).NotTo(HaveOccurred())
	os.Stderr = writer
	defer func() { os.Stderr = oldStderr }()

	fn()
	glog.Flush()
	Expect(writer.Close()).To(Succeed())

	out, err := io.ReadAll(reader)
	Expect(err).NotTo(HaveOccurred())
	return string(out)
}

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

	// approveChunkOutput and requestChangesChunkOutput are minimal valid chunk
	// review outputs — one parseable verdict block each.
	const approveChunkOutput = "chunk body\n\n```json\n" +
		`{"verdict":"approve","reason":"ok"}` + "\n```\n"
	const requestChangesChunkOutput = "chunk body\n\n```json\n" +
		`{"verdict":"request-changes","reason":"bad"}` + "\n```\n"

	// thirtyFileFunnel is a 30-file inventory of 20 added lines each. 600 added
	// lines is above the 500 engage threshold and the 300-line / 15-file bounds
	// split the path-sorted files into exactly two chunks: f00–f14 and f15–f29.
	// The findings straddle both chunks (f00.go, f20.go) so each chunk's prompt
	// can be checked for carrying only its own filtered findings.
	thirtyFileFunnel := func() pkg.FunnelResult {
		files := make([]pkg.ChangedFile, 0, 30)
		for i := 0; i < 30; i++ {
			files = append(
				files,
				pkg.ChangedFile{Path: fmt.Sprintf("f%02d.go", i), Additions: 20},
			)
		}
		return pkg.FunnelResult{
			Ran: true,
			FindingsJSON: `{"stats":{"yamls_run":1,"findings_count":2,"elapsed_ms":1},` +
				`"findings_by_owner":{"go-error-assistant":[` +
				`{"rule_id":"rule-chunk1","file":"f00.go","line":3},` +
				`{"rule_id":"rule-chunk2","file":"f20.go","line":5}]},` +
				`"errors":[]}`,
			ChangedFiles: files,
		}
	}

	// belowThresholdFunnel is 500 added lines over two files — at (not above)
	// the 500 engage threshold, so the partition yields exactly one chunk and
	// the review runs once, unscoped. Its findings span both files.
	belowThresholdFunnel := func() pkg.FunnelResult {
		return pkg.FunnelResult{
			Ran: true,
			FindingsJSON: `{"stats":{"yamls_run":1,"findings_count":2,"elapsed_ms":1},` +
				`"findings_by_owner":{"go-error-assistant":[` +
				`{"rule_id":"rule-chunk1","file":"a.go","line":3},` +
				`{"rule_id":"rule-chunk2","file":"b.go","line":5}]},` +
				`"errors":[]}`,
			ChangedFiles: []pkg.ChangedFile{
				{Path: "a.go", Additions: 300},
				{Path: "b.go", Additions: 200},
			},
		}
	}

	// chunkHeadingCount counts the merged body's per-chunk section headings.
	chunkHeadingCount := func(body string) int {
		return len(chunkHeadingPattern.FindAllString(body, -1))
	}

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

	newStepWithClock := func(
		runner claudelib.ClaudeRunner,
		poster pkg.PrPoster,
		funnel pkg.FunnelRunner,
		budget libtime.Duration,
		cfg pkg.ReviewChunkConfig,
		clock libtime.CurrentDateTimeGetter,
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
			clock,
			runner,
			budget,
			nil,
			cfg,
		)
	}

	newStepWithConfig := func(
		runner claudelib.ClaudeRunner,
		poster pkg.PrPoster,
		funnel pkg.FunnelRunner,
		budget libtime.Duration,
		cfg pkg.ReviewChunkConfig,
	) agentlib.Step {
		return newStepWithClock(
			runner,
			poster,
			funnel,
			budget,
			cfg,
			libtime.NewCurrentDateTime(),
		)
	}

	newStep := func(
		runner claudelib.ClaudeRunner,
		poster pkg.PrPoster,
		funnel pkg.FunnelRunner,
		budget libtime.Duration,
	) agentlib.Step {
		return newStepWithConfig(
			runner,
			poster,
			funnel,
			budget,
			pkg.DefaultReviewChunkConfig(),
		)
	}

	// bigFileFunnel is an inventory of count files with 301 added lines each.
	// The total is above the 500 engage threshold and each file alone exceeds the
	// 300 max-additions bound, so every file becomes its own chunk.
	bigFileFunnel := func(count int) pkg.FunnelResult {
		files := make([]pkg.ChangedFile, 0, count)
		for i := 0; i < count; i++ {
			files = append(
				files,
				pkg.ChangedFile{Path: fmt.Sprintf("s%d.go", i), Additions: 301},
			)
		}
		return pkg.FunnelResult{
			Ran: true,
			FindingsJSON: `{"stats":{"yamls_run":1,"findings_count":0,"elapsed_ms":1},` +
				`"findings_by_owner":{},"errors":[]}`,
			ChangedFiles: files,
		}
	}

	// chunkOutput is the canned per-chunk review output naming the chunk.
	chunkOutput := func(index int) string {
		return fmt.Sprintf(
			"body chunk %d\n\n```json\n{\"verdict\":\"approve\",\"reason\":\"ok\"}\n```\n",
			index,
		)
	}

	// runOutcome carries a step.Run result out of its goroutine.
	type runOutcome struct {
		result *agentlib.Result
		err    error
	}

	// runAsync starts step.Run in its own goroutine so the test goroutine keeps
	// control and can drive blocking runner stubs.
	runAsync := func(step agentlib.Step, md *agentlib.Markdown) <-chan runOutcome {
		outcomeCh := make(chan runOutcome, 1)
		go func() {
			result, err := step.Run(ctx, md)
			outcomeCh <- runOutcome{result: result, err: err}
		}()
		return outcomeCh
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
		// Chunk 1 always expires on its own deadline; chunk 2 waits for chunk 1
		// before returning, so the salvaged chunk is deterministically chunk 1
		// (which chunk's goroutine gets scheduled first must not decide it).
		chunk1Expired := make(chan struct{})
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(runCtx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			if chunkIndexFromPrompt(prompt) == 2 {
				<-chunk1Expired
				return &claudelib.ClaudeResult{Partial: "partial chunk output"}, runCtx.Err()
			}
			<-runCtx.Done() // block until chunk 1's share of the budget fires
			close(chunk1Expired)
			return &claudelib.ClaudeResult{Partial: "partial chunk output"}, runCtx.Err()
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(twoChunkFunnel(), nil)
		fakePoster := &mocks.PrPoster{}

		step := newStep(
			fakeRunner,
			fakePoster,
			funnelRunner,
			libtime.Duration(100*time.Millisecond),
		)

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
		Expect(fakeRunner.RunCallCount()).To(Equal(2))
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
		var promptsMu sync.Mutex
		var prompts []string
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			promptsMu.Lock()
			prompts = append(prompts, prompt)
			promptsMu.Unlock()
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

		promptsMu.Lock()
		defer promptsMu.Unlock()
		Expect(prompts).To(HaveLen(2))

		// The PR-author-controlled path reaches at least one prompt neutralized,
		// and no prompt ever carries the raw path.
		foundNeutralized := false
		for _, prompt := range prompts {
			Expect(prompt).NotTo(ContainSubstring(rawPath))
			if strings.Contains(prompt, "a[code-fence]b.go") {
				foundNeutralized = true
			}
		}
		Expect(foundNeutralized).To(BeTrue())
	})

	It("hands the concerns gate the wall-clock span, so a fast review is not demoted", func() {
		// Six chunks, three at a time, under a 25m budget. The first wave runs
		// from t0 to t0+5m, the second from t0+5m to t0+10m. The wall span is 10m,
		// below 0.8 × 25m = 20m, so the merged approve carrying a not-verified
		// concern stays an approve — the summed 30m would have demoted it.
		const chunkCount = 6
		t0 := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
		clock := &fakeClock{now: t0}
		startedCh := make(chan int, chunkCount)
		releases := make([]chan struct{}, chunkCount)
		for i := range releases {
			releases[i] = make(chan struct{})
		}
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			idx := chunkIndexFromPrompt(prompt)
			startedCh <- idx
			<-releases[idx-1]
			return &claudelib.ClaudeResult{Result: chunkApproveWithUnverifiedConcern}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(bigFileFunnel(chunkCount), nil)
		fakePoster := &mocks.PrPoster{}
		fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 1})

		cfg := pkg.DefaultReviewChunkConfig()
		cfg.Concurrency = 3
		step := newStepWithClock(
			fakeRunner,
			fakePoster,
			funnelRunner,
			libtime.Duration(25*time.Minute),
			cfg,
			clock,
		)

		outcomeCh := runAsync(step, buildMD())
		for i := 0; i < 3; i++ {
			<-startedCh
		}
		clock.Set(t0.Add(5 * time.Minute))
		close(releases[0])
		close(releases[1])
		close(releases[2])
		for i := 0; i < 3; i++ {
			<-startedCh
		}
		clock.Set(t0.Add(10 * time.Minute))
		close(releases[3])
		close(releases[4])
		close(releases[5])

		outcome := <-outcomeCh
		Expect(outcome.err).NotTo(HaveOccurred())
		Expect(outcome.result.Status).To(Equal(agentlib.AgentStatusDone))

		Expect(fakePoster.PostCallCount()).To(Equal(1))
		_, postReq := fakePoster.PostArgsForCall(0)
		Expect(postReq.Verdict).To(Equal(pkg.VerdictApprove))
	})

	It(
		"hands the concerns gate the wall-clock span, demoting a review that spanned the budget",
		func() {
			// Six chunks, three at a time, under a 25m budget. Chunks 2/3 finish at
			// t0+1m, chunks 4/5 at t0+2m, chunk 1 at t0+11m, chunk 6 at t0+21m. The
			// span is 21m (>= 0.8 × 25m = 20m), so the merged approve carrying a
			// not-verified concern demotes to request-changes — while max chunk time
			// (19m) and sum/N (≈11.3m) both stay below the threshold.
			const chunkCount = 6
			t0 := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
			clock := &fakeClock{now: t0}
			startedCh := make(chan int, chunkCount)
			releases := make([]chan struct{}, chunkCount)
			for i := range releases {
				releases[i] = make(chan struct{})
			}
			fakeRunner := &mocks.ClaudeRunnerMock{}
			fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
				idx := chunkIndexFromPrompt(prompt)
				startedCh <- idx
				<-releases[idx-1]
				return &claudelib.ClaudeResult{Result: chunkApproveWithUnverifiedConcern}, nil
			}
			funnelRunner := &mocks.FunnelRunner{}
			funnelRunner.RunReturns(bigFileFunnel(chunkCount), nil)
			fakePoster := &mocks.PrPoster{}
			fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 1})

			cfg := pkg.DefaultReviewChunkConfig()
			cfg.Concurrency = 3
			step := newStepWithClock(
				fakeRunner,
				fakePoster,
				funnelRunner,
				libtime.Duration(25*time.Minute),
				cfg,
				clock,
			)

			outcomeCh := runAsync(step, buildMD())
			for i := 0; i < 3; i++ {
				<-startedCh
			}
			clock.Set(t0.Add(1 * time.Minute))
			close(releases[1])
			close(releases[2])
			for i := 0; i < 2; i++ {
				<-startedCh
			}
			clock.Set(t0.Add(2 * time.Minute))
			close(releases[3])
			close(releases[4])
			<-startedCh // chunk 6 starts once a slot frees
			clock.Set(t0.Add(11 * time.Minute))
			close(releases[0])
			clock.Set(t0.Add(21 * time.Minute))
			close(releases[5])

			outcome := <-outcomeCh
			Expect(outcome.err).NotTo(HaveOccurred())
			Expect(outcome.result.Status).To(Equal(agentlib.AgentStatusDone))

			Expect(fakePoster.PostCallCount()).To(Equal(1))
			_, postReq := fakePoster.PostArgsForCall(0)
			Expect(postReq.Verdict).To(Equal(pkg.VerdictRequestChanges))
		},
	)

	It("invokes the runner once per chunk, with the clone and funnel each running once", func() {
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, _ string) (*claudelib.ClaudeResult, error) {
			return &claudelib.ClaudeResult{Result: approveChunkOutput}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(thirtyFileFunnel(), nil)

		step := newStep(fakeRunner, nil, funnelRunner, libtime.Duration(25*time.Minute))

		result, err := step.Run(ctx, buildMD())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		// Two chunks, two runner invocations — no extra synthesis invocation.
		Expect(fakeRunner.RunCallCount()).To(Equal(2))
		// The clone and the mechanical funnel run once per review, never per chunk.
		Expect(repoManager.EnsureWorktreeCallCount()).To(Equal(1))
		Expect(funnelRunner.RunCallCount()).To(Equal(1))
	})

	It("scopes each chunk's prompt to its own files and its own funnel findings", func() {
		var promptsMu sync.Mutex
		var prompts []string
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			promptsMu.Lock()
			prompts = append(prompts, prompt)
			promptsMu.Unlock()
			return &claudelib.ClaudeResult{Result: approveChunkOutput}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(thirtyFileFunnel(), nil)

		step := newStep(fakeRunner, nil, funnelRunner, libtime.Duration(25*time.Minute))

		_, err := step.Run(ctx, buildMD())
		Expect(err).NotTo(HaveOccurred())

		promptsMu.Lock()
		defer promptsMu.Unlock()
		Expect(prompts).To(HaveLen(2))

		// Select each prompt by the chunk index it names, never by slice position:
		// the two chunk goroutines append in completion order.
		byChunk := map[int]string{}
		for _, prompt := range prompts {
			byChunk[chunkIndexFromPrompt(prompt)] = prompt
		}
		chunk1, ok := byChunk[1]
		Expect(ok).To(BeTrue())
		chunk2, ok := byChunk[2]
		Expect(ok).To(BeTrue())

		// Each prompt carries the chunk-scope preamble naming its own chunk.
		Expect(chunk1).To(ContainSubstring("## Chunk scope"))
		Expect(chunk1).To(ContainSubstring("chunk 1/2"))
		Expect(chunk2).To(ContainSubstring("## Chunk scope"))
		Expect(chunk2).To(ContainSubstring("chunk 2/2"))

		// Chunk 1 owns f00–f14, chunk 2 owns f15–f29; neither prompt names a file
		// that belongs only to the other chunk.
		for i := 0; i < 15; i++ {
			Expect(chunk1).To(ContainSubstring(fmt.Sprintf("- f%02d.go", i)))
		}
		for i := 15; i < 30; i++ {
			Expect(chunk1).NotTo(ContainSubstring(fmt.Sprintf("f%02d.go", i)))
			Expect(chunk2).To(ContainSubstring(fmt.Sprintf("- f%02d.go", i)))
		}
		for i := 0; i < 15; i++ {
			Expect(chunk2).NotTo(ContainSubstring(fmt.Sprintf("f%02d.go", i)))
		}

		// Each prompt carries only its chunk's filtered findings (count 1), never
		// the whole-review findings (count 2).
		Expect(chunk1).To(ContainSubstring("rule-chunk1"))
		Expect(chunk1).NotTo(ContainSubstring("rule-chunk2"))
		Expect(chunk1).To(ContainSubstring(`"findings_count":1`))
		Expect(chunk1).NotTo(ContainSubstring(`"findings_count":2`))
		Expect(chunk2).To(ContainSubstring("rule-chunk2"))
		Expect(chunk2).NotTo(ContainSubstring("rule-chunk1"))
		Expect(chunk2).To(ContainSubstring(`"findings_count":1`))
		Expect(chunk2).NotTo(ContainSubstring(`"findings_count":2`))
	})

	It("runs once with the unscoped prompt at or below the engage threshold", func() {
		var prompts []string
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			prompts = append(prompts, prompt)
			return &claudelib.ClaudeResult{Result: approveChunkOutput}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(belowThresholdFunnel(), nil)

		step := newStep(fakeRunner, nil, funnelRunner, libtime.Duration(25*time.Minute))

		result, err := step.Run(ctx, buildMD())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		// Exactly one run, with no chunk-scope preamble and the unfiltered findings.
		Expect(fakeRunner.RunCallCount()).To(Equal(1))
		Expect(prompts).To(HaveLen(1))
		Expect(prompts[0]).NotTo(ContainSubstring("## Chunk scope"))
		Expect(prompts[0]).To(ContainSubstring(`"findings_count":2`))

		// Identity: the below-threshold prompt is byte-identical to the prompt the
		// forced-unscoped path (inventory unavailable) produces for the same run.
		unscoped := belowThresholdFunnel()
		unscoped.InventoryDetail = "could not compute added-line counts for base_ref main"
		var unscopedPrompts []string
		unscopedRunner := &mocks.ClaudeRunnerMock{}
		unscopedRunner.RunStub = func(
			_ context.Context,
			prompt string,
		) (*claudelib.ClaudeResult, error) {
			unscopedPrompts = append(unscopedPrompts, prompt)
			return &claudelib.ClaudeResult{Result: approveChunkOutput}, nil
		}
		unscopedFunnel := &mocks.FunnelRunner{}
		unscopedFunnel.RunReturns(unscoped, nil)
		unscopedStep := newStep(
			unscopedRunner,
			nil,
			unscopedFunnel,
			libtime.Duration(25*time.Minute),
		)

		_, err = unscopedStep.Run(ctx, buildMD())
		Expect(err).NotTo(HaveOccurred())
		Expect(unscopedPrompts).To(HaveLen(1))
		Expect(prompts[0]).To(Equal(unscopedPrompts[0]))
	})

	It("merges two approving chunks into one posted review with a single verdict block", func() {
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, _ string) (*claudelib.ClaudeResult, error) {
			return &claudelib.ClaudeResult{Result: approveChunkOutput}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(thirtyFileFunnel(), nil)
		fakePoster := &mocks.PrPoster{}
		fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 1})

		step := newStep(fakeRunner, fakePoster, funnelRunner, libtime.Duration(25*time.Minute))

		md := buildMD()
		result, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		// One section per chunk, exactly one verdict block, parsing to approve.
		section, exists := md.FindSection("## Review")
		Expect(exists).To(BeTrue())
		Expect(chunkHeadingCount(section.Body)).To(Equal(2))
		Expect(strings.Count(section.Body, "```json")).To(Equal(1))
		Expect(pkg.ParseVerdict(section.Body).Verdict).To(Equal(pkg.VerdictApprove))

		// Posted exactly once through the unchanged posting path, carrying the
		// merged body (verdict block stripped) and the merged verdict.
		Expect(fakePoster.PostCallCount()).To(Equal(1))
		_, postReq := fakePoster.PostArgsForCall(0)
		Expect(postReq.Verdict).To(Equal(pkg.VerdictApprove))
		Expect(postReq.Summary).To(ContainSubstring("### Chunk 1/2"))
		Expect(postReq.Summary).To(ContainSubstring("### Chunk 2/2"))
		Expect(strings.Count(postReq.Summary, "```json")).To(Equal(0))
	})

	It("merges a request-changes chunk into a posted request-changes verdict", func() {
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			if chunkIndexFromPrompt(prompt) == 2 {
				return &claudelib.ClaudeResult{Result: requestChangesChunkOutput}, nil
			}
			return &claudelib.ClaudeResult{Result: approveChunkOutput}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(thirtyFileFunnel(), nil)
		fakePoster := &mocks.PrPoster{}
		fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 1})

		step := newStep(fakeRunner, fakePoster, funnelRunner, libtime.Duration(25*time.Minute))

		md := buildMD()
		result, err := step.Run(ctx, md)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		// Worst-wins: the single merged verdict block parses to request-changes.
		section, exists := md.FindSection("## Review")
		Expect(exists).To(BeTrue())
		Expect(chunkHeadingCount(section.Body)).To(Equal(2))
		Expect(strings.Count(section.Body, "```json")).To(Equal(1))
		Expect(pkg.ParseVerdict(section.Body).Verdict).To(Equal(pkg.VerdictRequestChanges))

		Expect(fakePoster.PostCallCount()).To(Equal(1))
		_, postReq := fakePoster.PostArgsForCall(0)
		Expect(postReq.Verdict).To(Equal(pkg.VerdictRequestChanges))
	})

	It("reaches but never exceeds the configured concurrency", func() {
		const chunkCount = 7
		var mu sync.Mutex
		active := 0
		maxActive := 0
		started := make([]int, 0, chunkCount)
		startedCh := make(chan int, chunkCount)
		releaseCh := make(chan struct{})

		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			idx := chunkIndexFromPrompt(prompt)
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			started = append(started, idx)
			mu.Unlock()
			startedCh <- idx
			<-releaseCh
			mu.Lock()
			active--
			mu.Unlock()
			return &claudelib.ClaudeResult{Result: approveChunkOutput}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(bigFileFunnel(chunkCount), nil)

		cfg := pkg.DefaultReviewChunkConfig()
		cfg.Concurrency = 3
		step := newStepWithConfig(
			fakeRunner,
			nil,
			funnelRunner,
			libtime.Duration(25*time.Minute),
			cfg,
		)

		outcomeCh := runAsync(step, buildMD())
		firstWave := make([]int, 0, 3)
		for i := 0; i < 3; i++ {
			firstWave = append(firstWave, <-startedCh)
		}
		Expect(firstWave).To(ConsistOf(1, 2, 3))
		mu.Lock()
		Expect(maxActive).To(Equal(3))
		mu.Unlock()
		Consistently(func() int {
			mu.Lock()
			defer mu.Unlock()
			return maxActive
		}, 100*time.Millisecond).Should(Equal(3))

		close(releaseCh)

		outcome := <-outcomeCh
		Expect(outcome.err).NotTo(HaveOccurred())
		Expect(outcome.result.Status).To(Equal(agentlib.AgentStatusDone))

		mu.Lock()
		defer mu.Unlock()
		Expect(maxActive).To(Equal(3))
		Expect(started).To(ConsistOf(1, 2, 3, 4, 5, 6, 7))
		Expect(fakeRunner.RunCallCount()).To(Equal(7))
	})

	It("runs chunks one at a time in chunk order when concurrency is 1", func() {
		const chunkCount = 7
		var mu sync.Mutex
		active := 0
		maxActive := 0
		started := make([]int, 0, chunkCount)

		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			idx := chunkIndexFromPrompt(prompt)
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			started = append(started, idx)
			active--
			mu.Unlock()
			return &claudelib.ClaudeResult{Result: approveChunkOutput}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(bigFileFunnel(chunkCount), nil)

		cfg := pkg.DefaultReviewChunkConfig()
		cfg.Concurrency = 1
		step := newStepWithConfig(
			fakeRunner,
			nil,
			funnelRunner,
			libtime.Duration(25*time.Minute),
			cfg,
		)

		result, err := step.Run(ctx, buildMD())
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		mu.Lock()
		defer mu.Unlock()
		Expect(maxActive).To(Equal(1))
		Expect(started).To(Equal([]int{1, 2, 3, 4, 5, 6, 7}))
		Expect(fakeRunner.RunCallCount()).To(Equal(7))
	})

	It("merges chunk sections in chunk order regardless of completion order", func() {
		const chunkCount = 3
		chunk1Release := make(chan struct{})
		chunk3Done := make(chan struct{})

		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, prompt string) (*claudelib.ClaudeResult, error) {
			idx := chunkIndexFromPrompt(prompt)
			switch idx {
			case 1:
				<-chunk1Release
			case 3:
				close(chunk3Done)
			}
			return &claudelib.ClaudeResult{Result: chunkOutput(idx)}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(bigFileFunnel(chunkCount), nil)

		cfg := pkg.DefaultReviewChunkConfig()
		cfg.Concurrency = 3
		step := newStepWithConfig(
			fakeRunner,
			nil,
			funnelRunner,
			libtime.Duration(25*time.Minute),
			cfg,
		)

		md := buildMD()
		outcomeCh := runAsync(step, md)

		// Chunk 3 completes first, then chunk 1 is released.
		<-chunk3Done
		close(chunk1Release)

		outcome := <-outcomeCh
		Expect(outcome.err).NotTo(HaveOccurred())
		Expect(outcome.result.Status).To(Equal(agentlib.AgentStatusDone))

		section, exists := md.FindSection("## Review")
		Expect(exists).To(BeTrue())

		expected, _ := pkg.MergeChunkReviews(
			[]string{chunkOutput(1), chunkOutput(2), chunkOutput(3)},
		)
		Expect(section.Body).To(Equal(expected))

		chunk1Idx := strings.Index(section.Body, "### Chunk 1/3")
		chunk2Idx := strings.Index(section.Body, "### Chunk 2/3")
		chunk3Idx := strings.Index(section.Body, "### Chunk 3/3")
		Expect(chunk1Idx).To(BeNumerically(">=", 0))
		Expect(chunk1Idx).To(BeNumerically("<", chunk2Idx))
		Expect(chunk2Idx).To(BeNumerically("<", chunk3Idx))
	})

	It("logs one fan-out start line, one start per chunk, and one finished per chunk", func() {
		const chunkCount = 3
		fakeRunner := &mocks.ClaudeRunnerMock{}
		fakeRunner.RunStub = func(_ context.Context, _ string) (*claudelib.ClaudeResult, error) {
			return &claudelib.ClaudeResult{Result: approveChunkOutput}, nil
		}
		funnelRunner := &mocks.FunnelRunner{}
		funnelRunner.RunReturns(bigFileFunnel(chunkCount), nil)

		cfg := pkg.DefaultReviewChunkConfig()
		cfg.Concurrency = 3
		step := newStepWithConfig(
			fakeRunner,
			nil,
			funnelRunner,
			libtime.Duration(25*time.Minute),
			cfg,
		)

		md := buildMD()
		var result *agentlib.Result
		var runErr error
		output := captureGlog(func() {
			result, runErr = step.Run(ctx, md)
		})
		Expect(runErr).NotTo(HaveOccurred())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		lines := strings.Split(output, "\n")
		countMatching := func(pattern string) int {
			re := regexp.MustCompile(pattern)
			count := 0
			for _, line := range lines {
				if re.MatchString(line) {
					count++
				}
			}
			return count
		}

		Expect(countMatching(`fan-out start concurrency=3 chunks=3`)).To(Equal(1))
		Expect(
			countMatching(`review chunk \d+/3 files=\d+ additions=\d+`),
		).To(Equal(3))
		Expect(
			countMatching(`fan-out chunk \d+/3 finished elapsed=\S+`),
		).To(Equal(3))
		for _, line := range lines {
			if strings.Contains(line, "fan-out") {
				Expect(line).NotTo(ContainSubstring("review chunk"))
				Expect(line).NotTo(ContainSubstring("execution: chunk"))
			}
		}
	})
})
