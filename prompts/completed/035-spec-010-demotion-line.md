---
status: completed
spec: [010-bug-review-body-disagrees-with-posted-event]
summary: Added a demotion reason line to gate-demoted review bodies, covering all four demotion producers, with table and negative tests.
execution_id: c1fb3e45-826f-5fb9-b58a-f8cc73282a59-exec-035-spec-010-demotion-line
dark-factory-version: dev
created: "2026-11-04T17:49:28Z"
queued: "2026-11-04T18:07:51Z"
started: "2026-11-04T18:20:14Z"
completed: "2026-11-04T18:31:07Z"
branch: dark-factory/bug-review-body-disagrees-with-posted-event
---

<summary>
- A review demoted by a Go-side gate now says so on the first line of its posted body.
- The full review prose survives beneath the demotion reason; the reason is never substituted for the review.
- Each of the four demotion producers names its own reason: funnel did not run, unverified concerns at budget, blocking finding present, and the chunked fold.
- A model-authored request-changes gains no demotion line and keeps its prose verbatim, minus the verdict JSON.
- The posted event mapping, the gate precedence, and the two existing comment-only posting paths are untouched.
- The posted body still carries no verdict JSON of any kind.
</summary>

<objective>
In `postAndRoute`, record which Go-side gate demoted an `approve` to `request-changes` (including the chunked fold) and prepend that reason to the stripped review body before it is posted. A demoted review must never post a `CHANGES_REQUESTED` event with a body that says nothing about why. This is the second of two prompts and depends on the first having landed structural verdict-block removal.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Go 1.26, Ginkgo v2/Gomega, Counterfeiter mocks, `github.com/bborbe/errors`, `glog`).

Read these files before changing anything:
- `pkg/steps_checkout_execution.go` — `postAndRoute` is here (not in `pkg/githubposter/poster.go`). The three gates and the `Summary: summary` `PostRequest` field are in this method.
- `pkg/verdict.go` — `ReasonFunnelDidNotRun`, `ReasonConcernsNotVerified`, `ReasonBlockingFindingPresent`, `ApplyBlockingGate`, `ParseVerdict`, `StripJSONVerdict`.
- `pkg/chunk.go` — `MergeChunkReviews` and the unexported `chunkReviewChangesReason` constant (`"chunked review: at least one chunk requested changes"`). Both files are package `pkg`, so `postAndRoute` can reference the constant directly.
- `pkg/steps_checkout_execution_test.go` — the existing "posting behavior" `Describe`, `buildMD`, `mocks.PrPoster`, and `PostArgsForCall` assertions.
- `pkg/export_test.go` — `PostAndRouteForTest` / `PostAndRouteWithBudgetForTest` (signatures must not change).
- `pkg/githubposter/poster.go` — read `mapVerdictAndSummary`, `PostLGTM`, `postHallucinationComment`. **Do not modify this file.**

Reference this coding guide (it lives in the YOLO container, not on the host):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
</context>

<requirements>
1. **Add demotion tracking to `postAndRoute` in `pkg/steps_checkout_execution.go`.** The method currently computes:

   ```go
   verdict := ParseVerdict(reviewBody)
   summary := StripJSONVerdict(reviewBody)
   ```

   Immediately after those two lines, declare `var demotionReason string`. Keep every existing gate condition, comment, and precedence exactly as-is; only add the `demotionReason` assignment shown.

2. **Funnel gate.** In the existing `if !funnelRan && verdict.Verdict == VerdictApprove` block, set `demotionReason = ReasonFunnelDidNotRun` alongside the existing `verdict = Result{...}` assignment.

3. **Unverified-concerns gate.** In the existing `if verdict.Verdict == VerdictApprove && s.demotesUnverifiedConcerns(reviewBody, budgetUse)` block, set `demotionReason = ReasonConcernsNotVerified` alongside the existing `verdict = Result{...}` assignment.

4. **Blocking gate.** The existing line is `verdict = ApplyBlockingGate(verdict, reviewBody)`. Capture the verdict before the call and set the reason only when the gate actually converted approve to request-changes:

   ```go
   preBlockingVerdict := verdict.Verdict
   verdict = ApplyBlockingGate(verdict, reviewBody)
   if preBlockingVerdict == VerdictApprove && verdict.Verdict == VerdictRequestChanges {
   	demotionReason = ReasonBlockingFindingPresent
   }
   ```

5. **Chunked fold.** `MergeChunkReviews` sets the merged verdict to request-changes with `chunkReviewChangesReason` before `postAndRoute` parses the merged body, so that path must prepend a first line too. After the blocking gate, add:

   ```go
   if demotionReason == "" && verdict.Verdict == VerdictRequestChanges &&
   	verdict.Reason == chunkReviewChangesReason {
   	demotionReason = chunkReviewChangesReason
   }
   ```

   The `demotionReason == ""` guard keeps the funnel/concerns/blocking reason when one of those gates already fired.

   <!-- Reviewer note: the chunked fold is detected by the parsed reason literal because `postAndRoute` and `chunkReviewChangesReason` are in the same package. A model-authored body using that exact reason would gain the same (harmless) first line. If a stronger signal is preferred, thread the `MergeChunkReviews` result into `postAndRoute` instead of comparing the reason. -->

6. **Prepend, never replace.** After the gates (and before the existing fail-closed diagnostic `glog.Warningf` and `s.noReviewResult` call), add:

   ```go
   if demotionReason != "" {
   	summary = demotionReason + "\n\n" + summary
   }
   ```

   The posted `Summary` must be exactly `demotionReason + "\n\n" + StripJSONVerdict(reviewBody)`. An implementation that sets the summary to the reason alone must fail the tests. When the stripped review body is empty, the summary is the reason followed by two newlines — non-empty, so `mapVerdictAndSummary`'s empty-summary default does not fire.

   Do not change `PostRequest.Verdict`: it continues to carry the demoted `VerdictRequestChanges`. Do not change `mapVerdictAndSummary` or the event mapping.

   If the added branches push `postAndRoute` past the repo's `gocognit` threshold of 20, extract the three gate checks into a helper method `demotionReasonFor(verdict Result, reviewBody string, funnelRan bool, budgetUse float64) (Result, string)` and call it from `postAndRoute`; keep every gate condition, comment, and the funnel → concerns → blocking precedence verbatim inside the helper, and keep the prepend in `postAndRoute`. Do not add a `//nolint` directive.

7. **Add the AC4 table test in `pkg/steps_checkout_execution_test.go`**, inside the existing `Describe("posting behavior")` block (after the blocking-gate context). Use `mocks.PrPoster` and capture `req.Summary` via `PostArgsForCall`. For each row, assert the exact body and the first line:

   ```go
   Context("demotion line", func() {
   	approveChunk := "```json\n{\"verdict\":\"approve\",\"reason\":\"ok\"}\n```\n"
   	changesChunk := "```json\n{\"verdict\":\"request-changes\",\"reason\":\"bad\"}\n```\n"
   	chunkedBody, _ := pkg.MergeChunkReviews([]string{approveChunk, changesChunk})

   	DescribeTable("prepends the demotion reason once per producer",
   		func(body string, funnelRan bool, wantReason string) {
   			fakePoster := &mocks.PrPoster{}
   			fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 99})
   			md := buildMD(ctx, body)

   			result, err := pkg.PostAndRouteForTest(
   				ctx, fakePoster, md, prURL, "", fixedTime, funnelRan,
   			)
   			Expect(err).NotTo(HaveOccurred())
   			Expect(result.NextPhase).To(Equal("ai_review"))
   			Expect(fakePoster.PostCallCount()).To(Equal(1))

   			_, req := fakePoster.PostArgsForCall(0)
   			section, ok := md.FindSection("## Review")
   			Expect(ok).To(BeTrue())
   			Expect(req.Verdict).To(Equal(pkg.VerdictRequestChanges))
   			Expect(req.Summary).To(
   				Equal(wantReason + "\n\n" + pkg.StripJSONVerdict(section.Body)),
   			)
   			Expect(strings.SplitN(req.Summary, "\n", 2)[0]).To(Equal(wantReason))
   			Expect(req.Summary).NotTo(ContainSubstring(`"verdict"`))
   		},
   		Entry("funnel did not run",
   			"LGTM. All checks pass.\n\n```json\n{\"verdict\":\"approve\",\"reason\":\"ok\"}\n```\n",
   			false,
   			pkg.ReasonFunnelDidNotRun),
   		Entry("unverified concerns at budget",
   			"LGTM.\n\n```json\n{\"verdict\":\"approve\",\"reason\":\"ok\",\"concerns_addressed\":[{\"concern\":\"security: rate-limit\",\"disposition\":\"not-verified\"}]}\n```\n",
   			true,
   			pkg.ReasonConcernsNotVerified),
   		Entry("blocking finding present",
   			"LGTM.\n\n```json\n{\"verdict\":\"approve\",\"reason\":\"ok\",\"comments\":[{\"blocking\":true}]}\n```\n",
   			true,
   			pkg.ReasonBlockingFindingPresent),
   		Entry("chunked fold",
   			chunkedBody,
   			true,
   			"chunked review: at least one chunk requested changes"),
   	)
   ```

   The exact-equality assertion is the load-bearing one: an implementation that replaces the review with the reason alone, or that leaves the original approve JSON in place, must fail. Reading the `## Review` section back after `postAndRoute` is intentional — it guarantees the expected value uses the same body `postAndRoute` read.

8. **Add the empty-body failure-mode test.** A gate that demotes a body whose only content is the verdict block must still produce a non-empty summary:

   ```go
   It("prepends the reason even when the stripped body is empty", func() {
   	fakePoster := &mocks.PrPoster{}
   	fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 100})
   	md := buildMD(ctx, `{"verdict":"approve","reason":"ok"}`)

   	_, err := pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime, false)
   	Expect(err).NotTo(HaveOccurred())
   	Expect(fakePoster.PostCallCount()).To(Equal(1))

   	_, req := fakePoster.PostArgsForCall(0)
   	Expect(req.Summary).To(Equal(pkg.ReasonFunnelDidNotRun + "\n\n"))
   	Expect(req.Summary).NotTo(BeEmpty())
   })
   ```

9. **Add the AC5 negative test.** A model-authored `request-changes` must gain no demotion line:

   ```go
   It("leaves a model-authored request-changes body exactly stripped, with no demotion line", func() {
   	fakePoster := &mocks.PrPoster{}
   	fakePoster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 101})
   	md := buildMD(ctx,
   		"The code has issues.\n\n```json\n{\"verdict\":\"request-changes\",\"reason\":\"missing tests\"}\n```\n")

   	_, err := pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime, true)
   	Expect(err).NotTo(HaveOccurred())
   	Expect(fakePoster.PostCallCount()).To(Equal(1))

   	_, req := fakePoster.PostArgsForCall(0)
   	section, ok := md.FindSection("## Review")
   	Expect(ok).To(BeTrue())
   	Expect(req.Verdict).To(Equal(pkg.VerdictRequestChanges))
   	Expect(req.Summary).To(Equal(pkg.StripJSONVerdict(section.Body)))
   	Expect(req.Summary).NotTo(HavePrefix("mechanical funnel did not run"))
   	Expect(req.Summary).NotTo(HavePrefix("one or more ## Plan concerns not verified"))
   	Expect(req.Summary).NotTo(HavePrefix("blocking finding present"))
   	Expect(req.Summary).NotTo(HavePrefix("chunked review:"))
   })
   ```

10. **Do not change `pkg/githubposter/poster.go`, `pkg/export_test.go`, or `pkg/chunk.go`.** The event mapping stays verdict-only (spec 060); `PostLGTM` and `postHallucinationComment` are out of scope. The 65,536-character truncation path is unchanged — prepending a short reason line keeps the reason at the start, which truncation preserves.
</requirements>

<constraints>
- The fail-closed gates keep their exact precedence: funnel, then concerns, then blocking. This spec makes the body honest; it does not loosen a gate and does not change when a demotion fires.
- `mapVerdictAndSummary` keeps mapping verdict → event only. No new posting path, no new `COMMENT` event (spec 060). Do not touch `PostLGTM` or `postHallucinationComment`.
- A model-authored `request-changes` body equals `StripJSONVerdict(reviewBody)` exactly: it keeps its prose and findings verbatim, gains no demotion line, and loses only its JSON block.
- A demoted body carries no `verdict` JSON of any kind — neither the original approve nor a rewritten request-changes block.
- `ParseVerdict`'s block locator is the reference implementation; this prompt does not change it.
- The 65,536-character truncation path (`pkg/githubposter/poster.go`) keeps working after the block is removed.
- Go 1.26, Ginkgo v2/Gomega, Counterfeiter mocks, errors via `github.com/bborbe/errors`, logging via `glog`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).
- `go test ./pkg/... -count=1` — must pass, including the new demotion-line contexts and the pre-existing gate tests.
- `grep -n 'case prpkg.Verdict' pkg/githubposter/poster.go` — prints exactly two lines: `case prpkg.VerdictRequestChanges:` and `case prpkg.VerdictApprove:`.
- `git diff --stat pkg/githubposter/poster.go` — prints nothing (the poster file is untouched). (`.dark-factory.yaml` uses `workflow: direct`, so `.git` is present.)
</verification>
