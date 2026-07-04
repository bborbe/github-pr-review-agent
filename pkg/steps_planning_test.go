// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/json"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("planningStep", func() {
	var (
		ctx      context.Context
		runner   *mocks.ClaudeRunnerMock
		prPoster *mocks.PrPoster
		step     agentlib.Step
		botLogin string
	)

	BeforeEach(func() {
		ctx = context.Background()
		runner = &mocks.ClaudeRunnerMock{}
		prPoster = &mocks.PrPoster{}
		botLogin = "ben-s-pull-request-reviewer-dev[bot]"
		currentDateTime := libtime.NewCurrentDateTime()
		step = pkg.NewPlanningStep(
			runner,
			claudelib.Instructions{},
			prPoster,
			botLogin,
			currentDateTime,
		)
	})

	Describe("Name", func() {
		It("returns pr-plan", func() {
			Expect(step.Name()).To(Equal("pr-plan"))
		})
	})

	Describe("ShouldRun", func() {
		// ShouldRun now always returns true. Idempotency for the "## Plan
		// already present" case is enforced inside Run (skip claude, re-route
		// from existing body). The previous "skip step if ## Plan present"
		// behaviour silently dropped the routing decision on retrigger —
		// see the trading#136 incident (2026-05-25).
		DescribeTable("always returns true so the routing decision is never skipped",
			func(content string) {
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.ShouldRun(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeTrue())
			},
			Entry("no plan section", "# PR Review\n\nsome text"),
			Entry("plan section present", "# PR Review\n\n## Plan\n\n{}"),
			Entry("empty content", ""),
		)
	})

	Describe("Run — empty concerns path (LGTM)", func() {
		var md *agentlib.Markdown

		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when ## Plan has concerns: [] and POST succeeds", func() {
			BeforeEach(func() {
				planBody, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"README.md"},
					"scope":         "docs",
					"focus_areas":   []string{"docs"},
					"concerns":      []interface{}{},
				})
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "```json\n" + string(planBody) + "\n```",
				}, nil)
				prPoster.PostLGTMReturns(pkg.PostResult{
					Outcome:     "success",
					ReviewID:    12345,
					PostedEvent: "COMMENT",
				})
			})

			It("calls PrPoster.PostLGTM with correct arguments", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(prPoster.PostLGTMCallCount()).To(Equal(1))
				_, prArg, headSHAArg, workDirArg, botLoginArg := prPoster.PostLGTMArgsForCall(0)
				Expect(prArg.Owner).To(Equal("bborbe"))
				Expect(prArg.Repo).To(Equal("maintainer"))
				Expect(prArg.Number).To(Equal(14))
				Expect(headSHAArg).To(Equal("abc123"))
				Expect(workDirArg).To(Equal(""))
				Expect(botLoginArg).To(Equal(botLogin))
			})

			It("returns status done with NextPhase done", func() {
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("done"))
			})

			It("writes ## Plan section with the LLM output", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				planSection, exists := md.FindSection("## Plan")
				Expect(exists).To(BeTrue())
				Expect(planSection.Body).To(ContainSubstring("concerns"))
			})

			It("writes ## Verdict section naming review id and COMMENT", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				verdictSection, exists := md.FindSection("## Verdict")
				Expect(exists).To(BeTrue())
				Expect(verdictSection.Body).To(ContainSubstring("review_id: 12345"))
				Expect(verdictSection.Body).To(ContainSubstring("event: COMMENT"))
			})

			It("appends a success diagnostics one-liner", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				diagSection, exists := md.FindSection("## Diagnostics")
				Expect(exists).To(BeTrue())
				Expect(diagSection.Body).To(ContainSubstring("outcome: success"))
				Expect(diagSection.Body).To(ContainSubstring("review_id: 12345"))
			})
		})

		Context("when ## Plan has concerns: [] and POST returns failure", func() {
			BeforeEach(func() {
				planBody, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"README.md"},
					"scope":         "docs",
					"focus_areas":   []string{"docs"},
					"concerns":      []interface{}{},
				})
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "```json\n" + string(planBody) + "\n```",
				}, nil)
				prPoster.PostLGTMReturns(pkg.PostResult{
					Outcome:      "failed",
					FailureStep:  "POST /pulls/N/reviews",
					Class:        pkg.ErrorClassTransient,
					ErrorMessage: "network timeout",
					HTTPStatus:   500,
				})
			})

			It("returns status done with NextPhase human_review", func() {
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))
				Expect(result.Message).To(ContainSubstring("LGTM POST failed"))
			})

			It("appends a failure diagnostic block", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				diagSection, exists := md.FindSection("## Diagnostics")
				Expect(exists).To(BeTrue())
				Expect(diagSection.Body).To(ContainSubstring("outcome: failed"))
				Expect(diagSection.Body).To(ContainSubstring("network timeout"))
			})

			It("does NOT write ## Verdict section", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				_, exists := md.FindSection("## Verdict")
				Expect(exists).To(BeFalse())
			})
		})

		Context("when prPoster is nil (cmd/run-task mode)", func() {
			BeforeEach(func() {
				currentDateTime := libtime.NewCurrentDateTime()
				step = pkg.NewPlanningStep(
					runner,
					claudelib.Instructions{},
					nil,
					botLogin,
					currentDateTime,
				)
				planBody, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"README.md"},
					"scope":         "docs",
					"focus_areas":   []string{"docs"},
					"concerns":      []interface{}{},
				})
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "```json\n" + string(planBody) + "\n```",
				}, nil)
			})

			It("returns done without calling PostLGTM", func() {
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("done"))
			})
		})
	})

	Describe("Run — non-empty concerns path (execution)", func() {
		var md *agentlib.Markdown

		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when ## Plan has non-empty concerns", func() {
			BeforeEach(func() {
				planBody, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"pkg/auth/handler.go"},
					"scope":         "feature",
					"focus_areas":   []string{"security"},
					"concerns": []map[string]string{
						{
							"area": "security",
							"file": "pkg/auth/handler.go",
							"note": "missing rate limit",
						},
					},
				})
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "```json\n" + string(planBody) + "\n```",
				}, nil)
			})

			It(
				"returns status done with NextPhase execution (canonical phase per spec 032)",
				func() {
					result, err := step.Run(ctx, md)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
					// Boundary contract: the value emitted here must equal the canonical
					// domain.TaskPhase constant — string-typed because agentlib.Result.NextPhase
					// is plain string. Reverting to "in_progress" causes the agentlib frontmatter
					// validator to reject the write at delivery time (spec 035 root cause).
					Expect(result.NextPhase).To(Equal(string(domain.TaskPhaseExecution)))
				},
			)

			It("does NOT call PostLGTM", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(prPoster.PostLGTMCallCount()).To(Equal(0))
			})

			It("does NOT write ## Verdict section", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				_, exists := md.FindSection("## Verdict")
				Expect(exists).To(BeFalse())
			})

			It("does NOT append diagnostics", func() {
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				_, exists := md.FindSection("## Diagnostics")
				Expect(exists).To(BeFalse())
			})
		})
	})

	Describe("Run — retrigger with existing ## Plan (re-route without claude)", func() {
		// Reproduces the trading#136 incident: a previous trigger wrote
		// ## Plan with concerns then execution failed → controller reset
		// trigger_count → new pod runs planning → with the old skip-via-
		// ShouldRun behaviour the step was skipped entirely, NextPhase
		// ended up empty, and the task short-circuited to phase: done
		// without any review running. The fix is to always run the step
		// but skip the claude call when ## Plan is already present.

		buildMarkdownWithExistingPlan := func(concerns []map[string]string) *agentlib.Markdown {
			planJSON, _ := json.Marshal(map[string]interface{}{
				"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
				"pr_title":      "test PR",
				"base_branch":   "main",
				"head_branch":   "feat/test",
				"files_changed": []string{"pkg/x.go"},
				"scope":         "feature",
				"focus_areas":   []string{"tests"},
				"concerns":      concerns,
			})
			content := "---\nref: abc123\ntask_identifier: 00000000-0000-0000-0000-000000000001\n---\n" +
				"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n\n" +
				"## Plan\n\n```json\n" + string(
				planJSON,
			) + "\n```\n"
			md, err := agentlib.ParseMarkdown(ctx, content)
			Expect(err).NotTo(HaveOccurred())
			return md
		}

		It("does NOT call the claude runner when ## Plan already exists", func() {
			md := buildMarkdownWithExistingPlan([]map[string]string{
				{"area": "tests", "file": "pkg/x.go", "note": "missing coverage"},
			})
			_, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(runner.RunCallCount()).To(Equal(0))
		})

		It("routes non-empty concerns to execution from existing plan", func() {
			md := buildMarkdownWithExistingPlan([]map[string]string{
				{"area": "tests", "file": "pkg/x.go", "note": "missing coverage"},
			})
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("execution"))
		})

		It("routes empty concerns to LGTM/done path from existing plan", func() {
			prPoster.PostLGTMReturns(pkg.PostResult{
				Outcome:     "success",
				ReviewID:    12345,
				PostedEvent: "COMMENT",
			})
			md := buildMarkdownWithExistingPlan(nil)
			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
			Expect(prPoster.PostLGTMCallCount()).To(Equal(1))
		})
	})

	Describe("Run — in-agent retry on malformed JSON", func() {
		// happy markdown with valid PR URL — reused across all sub-cases
		buildBaseMarkdown := func() *agentlib.Markdown {
			md, err := agentlib.ParseMarkdown(ctx, `---
ref: abc123
task_identifier: 00000000-0000-0000-0000-000000000001
---
# PR Review

https://github.com/bborbe/maintainer/pull/14
`)
			Expect(err).NotTo(HaveOccurred())
			return md
		}

		goodPlanBody := func() string {
			planBody, _ := json.Marshal(map[string]interface{}{
				"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
				"pr_title":      "test PR",
				"base_branch":   "main",
				"head_branch":   "feat/test",
				"files_changed": []string{"README.md"},
				"scope":         "docs",
				"focus_areas":   []string{"docs"},
				"concerns":      []interface{}{},
			})
			return "```json\n" + string(planBody) + "\n```"
		}

		Context("attempt 1 succeeds", func() {
			BeforeEach(func() {
				runner.RunReturns(&claudelib.ClaudeResult{Result: goodPlanBody()}, nil)
				prPoster.PostLGTMReturns(pkg.PostResult{
					Outcome:     "success",
					ReviewID:    12345,
					PostedEvent: "COMMENT",
				})
			})

			It("calls runner exactly once and returns done", func() {
				md := buildBaseMarkdown()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(runner.RunCallCount()).To(Equal(1))
			})

			It("writes ## Plan section with the valid response", func() {
				md := buildBaseMarkdown()
				_, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				planSection, exists := md.FindSection("## Plan")
				Expect(exists).To(BeTrue())
				Expect(planSection.Body).To(ContainSubstring("concerns"))
			})
		})

		Context("attempt 2 succeeds", func() {
			BeforeEach(func() {
				// First call: malformed (no JSON)
				runner.RunReturnsOnCall(0, &claudelib.ClaudeResult{
					Result: "Based on the diff, here is the plan...",
				}, nil)
				// Second call: valid JSON
				runner.RunReturnsOnCall(1, &claudelib.ClaudeResult{Result: goodPlanBody()}, nil)
				prPoster.PostLGTMReturns(pkg.PostResult{
					Outcome:     "success",
					ReviewID:    12345,
					PostedEvent: "COMMENT",
				})
			})

			It("retries and persists the second (valid) response", func() {
				md := buildBaseMarkdown()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).NotTo(Equal(agentlib.AgentStatusFailed))
				Expect(runner.RunCallCount()).To(Equal(2))
				planSection, exists := md.FindSection("## Plan")
				Expect(exists).To(BeTrue())
				Expect(planSection.Body).To(ContainSubstring("concerns"))
			})
		})

		Context("all 3 attempts fail", func() {
			BeforeEach(func() {
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "Based on the diff, here is the plan...",
				}, nil)
			})

			It("returns AgentStatusFailed after exhausting all attempts", func() {
				md := buildBaseMarkdown()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("malformed JSON after 3 attempts"))
				Expect(runner.RunCallCount()).To(Equal(3))
				_, exists := md.FindSection("## Plan")
				Expect(exists).To(BeFalse())
			})
		})

		Context("runner transport error not retried", func() {
			BeforeEach(func() {
				runner.RunReturns(nil, context.DeadlineExceeded)
			})

			It("fails immediately without retrying", func() {
				md := buildBaseMarkdown()
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(runner.RunCallCount()).To(Equal(1))
			})
		})

		Context("idempotent re-entry — runner not called", func() {
			buildMarkdownWithExistingPlan := func(concerns []map[string]string) *agentlib.Markdown {
				planJSON, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"pkg/x.go"},
					"scope":         "feature",
					"focus_areas":   []string{"tests"},
					"concerns":      concerns,
				})
				content := "---\nref: abc123\ntask_identifier: 00000000-0000-0000-0000-000000000001\n---\n" +
					"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n\n" +
					"## Plan\n\n```json\n" + string(
					planJSON,
				) + "\n```\n"
				md, err := agentlib.ParseMarkdown(ctx, content)
				Expect(err).NotTo(HaveOccurred())
				return md
			}

			It("skips the runner entirely when ## Plan already exists", func() {
				md := buildMarkdownWithExistingPlan(nil)
				prPoster.PostLGTMReturns(pkg.PostResult{
					Outcome:     "success",
					ReviewID:    12345,
					PostedEvent: "COMMENT",
				})
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(runner.RunCallCount()).To(Equal(0))
			})
		})
	})

	Describe("Run — error cases", func() {
		Context("when ## Plan JSON is malformed", func() {
			var md *agentlib.Markdown

			BeforeEach(func() {
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "not valid json at all",
				}, nil)
				var err error
				md, err = agentlib.ParseMarkdown(
					ctx,
					"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n",
				)
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns AgentStatusFailed without writing ## Plan", func() {
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("planning: malformed JSON"))
				_, exists := md.FindSection("## Plan")
				Expect(exists).To(BeFalse())
			})
		})

		Context(
			"when Claude returns a ## Plan body containing unescaped double quotes (live bug vault-cli#27)",
			func() {
				BeforeEach(func() {
					// The exact class of failure observed on 2026-06-26: a Go zero-string-check
					// `name != ""` embedded literally inside a JSON string value. The outer
					// quote parser closes the string at the first `"`, leaving `!= ` as
					// unexpected tokens.
					liveSample := "```json\n" +
						"{\"pr_url\":\"https://github.com/bborbe/vault-cli/pull/27\",\"pr_title\":\"fix\",\"base_branch\":\"main\",\"head_branch\":\"fix/args\",\"files_changed\":[\"cmd/main.go\"],\"scope\":\"bugfix\",\"focus_areas\":[\"correctness\"],\"concerns\":[{\"area\":\"correctness\",\"file\":\"cmd/main.go\",\"note\":\"Arg order matters: name != \"\" must appear after --print\"}]}" +
						"\n```"
					runner.RunReturns(&claudelib.ClaudeResult{Result: liveSample}, nil)
				})

				It("returns AgentStatusFailed and does not write ## Plan", func() {
					md, err := agentlib.ParseMarkdown(
						ctx,
						"---\nref: abc123\ntask_identifier: 00000000-0000-0000-0000-000000000001\n---\n# PR Review\n\nhttps://github.com/bborbe/vault-cli/pull/27\n",
					)
					Expect(err).NotTo(HaveOccurred())
					result, err := step.Run(ctx, md)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
					Expect(result.Message).To(ContainSubstring("planning: malformed JSON"))
					_, exists := md.FindSection("## Plan")
					Expect(exists).To(BeFalse())
				})
			},
		)

		Context("when Claude runner returns an error", func() {
			BeforeEach(func() {
				runner.RunReturns(nil, context.DeadlineExceeded)
			})

			It("returns AgentStatusFailed", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"# PR Review\n\nhttps://github.com/bborbe/maintainer/pull/14\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			})
		})

		Context("when PR URL is absent from task", func() {
			BeforeEach(func() {
				planBody, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://github.com/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"README.md"},
					"scope":         "docs",
					"focus_areas":   []string{"docs"},
					"concerns":      []interface{}{},
				})
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "```json\n" + string(planBody) + "\n```",
				}, nil)
			})

			It("returns human_review when PR URL missing", func() {
				md, err := agentlib.ParseMarkdown(ctx, "# PR Review\n")
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("human_review"))
				Expect(result.Message).To(ContainSubstring("no GitHub PR URL"))
			})
		})

		Context("when non-GitHub platform", func() {
			BeforeEach(func() {
				planBody, _ := json.Marshal(map[string]interface{}{
					"pr_url":        "https://bitbucket.org/bborbe/maintainer/pull/14",
					"pr_title":      "test PR",
					"base_branch":   "main",
					"head_branch":   "feat/test",
					"files_changed": []string{"README.md"},
					"scope":         "docs",
					"focus_areas":   []string{"docs"},
					"concerns":      []interface{}{},
				})
				runner.RunReturns(&claudelib.ClaudeResult{
					Result: "```json\n" + string(planBody) + "\n```",
				}, nil)
			})

			It("skips posting and returns done", func() {
				md, err := agentlib.ParseMarkdown(
					ctx,
					"# PR Review\n\nhttps://bitbucket.org/bborbe/maintainer/pull/14\n",
				)
				Expect(err).NotTo(HaveOccurred())
				result, err := step.Run(ctx, md)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.NextPhase).To(Equal("done"))
				Expect(prPoster.PostLGTMCallCount()).To(Equal(0))
			})
		})
	})

	Describe("parsePlanningConcerns", func() {
		DescribeTable(
			"extracts concerns array from various JSON wrapping",
			func(body, want string) {
				concerns, err := pkg.ParsePlanningConcernsForTest(body)
				if want == "error" {
					Expect(err).To(HaveOccurred())
					return
				}
				Expect(err).NotTo(HaveOccurred())
				if want == "empty" {
					Expect(concerns).To(BeEmpty())
				} else {
					Expect(concerns).NotTo(BeEmpty())
				}
			},
			Entry("bare JSON array", `{"concerns":[]}`, "empty"),
			Entry("json fence", "```json\n{\"concerns\":[]}\n```", "empty"),
			Entry(
				"non-empty concerns",
				"```json\n{\"concerns\":[{\"area\":\"security\"}]}\n```",
				"non-empty",
			),
			Entry("malformed JSON", "not json at all", "error"),
		)
	})
})

var _ = Describe("isGitHubPRURL", func() {
	DescribeTable(
		"identifies GitHub PR URLs",
		func(rawURL string, want bool) {
			Expect(pkg.IsGitHubPRURLForTest(rawURL)).To(Equal(want))
		},
		Entry("github.com PR URL", "https://github.com/owner/repo/pull/123", true),
		Entry(
			"github.com PR URL with extra path",
			"https://github.com/owner/repo/pull/123/head",
			true,
		),
		Entry("bitbucket PR URL", "https://bitbucket.org/owner/repo/pull/123", false),
		Entry("gitlab PR URL", "https://gitlab.com/owner/repo/-/merge_requests/123", false),
		Entry("random URL", "https://example.com/something", false),
		Entry("empty string", "", false),
	)
})

var _ = Describe("hasAnyPRURL", func() {
	It("returns true when preamble contains a PR URL", func() {
		md, err := agentlib.ParseMarkdown(
			context.Background(),
			"See https://github.com/owner/repo/pull/123\n\n## Review\n\nsome content",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pkg.HasAnyPRURLForTest(md)).To(BeTrue())
	})

	It("returns false when no PR URL is present", func() {
		md, err := agentlib.ParseMarkdown(
			context.Background(),
			"No PR here, just some content\n\n## Review\n\nsome content",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(pkg.HasAnyPRURLForTest(md)).To(BeFalse())
	})
})

var _ = Describe("writePlanningVerdict", func() {
	It("writes verdict section with review ID and event", func() {
		md, err := agentlib.ParseMarkdown(
			context.Background(),
			"---\nref: abc\n---\n\n## Plan\n\nsome plan",
		)
		Expect(err).NotTo(HaveOccurred())
		pkg.WritePlanningVerdictForTest(md, 42, "APPROVE")
		sec, exists := md.FindSection("## Verdict")
		Expect(exists).To(BeTrue())
		Expect(sec).NotTo(BeNil())
		Expect(sec.Body).To(ContainSubstring("review_id: 42"))
		Expect(sec.Body).To(ContainSubstring("event: APPROVE"))
	})
})
