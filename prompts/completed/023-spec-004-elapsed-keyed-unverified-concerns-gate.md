---
status: completed
spec: [004-bug-benign-gap-whitelist-demotes-clean-approve]
summary: 'Replaced the prose-keyed unverified-concerns fail-close with a budget-keyed demotion: runWithSoftBudget now returns the run''s elapsed wall-clock time, DemotesUnverifiedConcerns keys the posting gate on >=0.8 of the soft budget consumed (nuke#216 short run''s not-verified is a mislabel and its approve posts; a budget-heavy run still fail-closes), both prose regexes are deleted (MustCompile==1), the model-facing schema no longer teaches the benign-gap escape hatch, and the incident is regression-locked by a verbatim fixture plus budget-keyed and posting-boundary rows. RED evidence: row a posted request-changes (TeamVault wording escaped the benign whitelist) and row b posted approve (toolchain wording matched it).'
execution_id: github-pr-review-agent-exec-023-spec-004-elapsed-keyed-unverified-concerns-gate
dark-factory-version: dev
created: "2026-09-11T09:28:14Z"
queued: "2026-09-11T10:25:19Z"
started: "2026-09-11T10:33:03Z"
completed: "2026-09-11T10:49:38Z"
---

# Key the unverified-concerns demotion on budget consumption, not on concern prose

<summary>
- A clean `approve` from a review that finished well inside its time budget is no longer downgraded to `CHANGES_REQUESTED` because of how the model worded an unverifiable concern
- The demotion decision now keys on how much of the run's time budget was consumed, instead of pattern-matching the concern text
- A review that consumed its budget and left concerns unexamined still fails closed, so an incomplete review can never green-light a PR
- The two prose-matching rule tables that caused every prior recurrence are deleted; no pattern match over concern prose remains anywhere in the demotion path
- The verbatim review body that triggered the recurrence is stored as a regression fixture and locks both halves of the fix
- The model-facing output schema stops teaching the "explain the gap as benign" escape hatch that kept re-introducing the bug
- Older task files that carry the pre-change flat-string concern list keep working unchanged
- Reviews with no unexamined concerns, no verdict block, or a non-list concern field still never over-trigger
- The fix is demonstrated by two regression rows that fail on the current code and pass after the change
</summary>

<objective>
Replace the prose-keyed unverified-concerns fail-close with the elapsed-time cross-check spec 004 named as its own follow-up lever, so an `approve` that a short run mislabelled `not-verified` posts as `APPROVED` while a budget-heavy run that genuinely never examined its concerns still fail-closes — and no regular expression matches concern prose anywhere in the demotion path.
</objective>

<context>
This is a follow-up prompt for an already-shipped spec whose fix did not hold. Read the spec before changing anything:

- `specs/in-progress/004-bug-benign-gap-whitelist-demotes-clean-approve.md` — read **Goal**, **Desired Behavior 3–6**, **Constraints** (especially the "Residual risk, accepted and named" bullet, which names this exact lever) and the **Failure Modes** table row "Model marks an examined concern `not-verified` anyway". Do NOT edit the spec: a failed fix is re-opened with a new prompt that links to the spec, never by rewriting the spec.

Read `CLAUDE.md` for project conventions (Ginkgo v2 / Gomega, Counterfeiter mocks, external `*_test` packages, `github.com/bborbe/errors`, `glog`, and the releasing rules).

Read these files fully before writing anything:

- `pkg/verdict.go` — the demotion path. `verdictFieldRegexp` is the only `MustCompile` that may remain in this file; `mustTierBlockerPattern` and `benignVerificationGapPattern` and `unverifiedConcernDemotes` are deleted by this prompt; `HasUnverifiedConcerns` becomes a pure admission reader; `ReasonConcernsNotVerified` keeps its string value; `ParseVerdict`, `ApplyBlockingGate`, `HasBlockingFinding`, `isFailClosedReason` and `StripJSONVerdict` are NOT changed.
- `pkg/steps_checkout_execution.go` — `runClaude` (calls the budget runner, writes `## Review`, calls `postAndRoute`) and `postAndRoute` (the three fail-closed gates: funnel, concerns, blocking). Note the comment above the concerns gate and the budget-expiry early return in `runClaude`, which routes an expired run to `human_review` before any posting — a budget-expired run never reaches `postAndRoute`.
- `pkg/soft_budget.go` — `runWithSoftBudget` and `budgetExpiredResult`.
- `pkg/steps_planning.go` and `pkg/steps_review.go` — READ ONLY. Both call `runWithSoftBudget`; they are the two sibling call sites whose call shape changes with requirement 2.
- `pkg/verdict_unverified_test.go` — the table this prompt rewrites. Note the `fence` helper, the `DescribeTable` rows, the incident-regression `Describe` that reads a testdata fixture, and the rows whose expectations were keyed on concern prose.
- `pkg/steps_checkout_execution_test.go` — the `posting behavior` Describe: its `buildMD` helper, the `fail-closed gate when a concern is flagged not verified` Context, and the rows that drive `PostAndRouteForTest`.
- `pkg/export_test.go` — the `ForTest` re-export convention (`PostAndRouteForTest` in particular).
- `pkg/testdata/review_discord_assistant_37_run2.md` — the fixture shape the new fixture follows.
- `pkg/prompts/execution_output-format.md` and `pkg/prompts/execution.go` — the model-facing demotion rule (requirement 9).
- `docs/dod.md` — the project validation checklist you self-review against.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — the MUST rule against stdlib `t.Run` table tests, and the `DescribeTable` / `Entry` shape
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md` — doc comments start with the identifier and describe behaviour, not implementation
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — the `## Unreleased` bullet format

Background — what went wrong, so you do not re-introduce it. Spec 004 replaced three prose regexes with a structured `disposition` field, which was correct, but a later change (commit `348cb3c`, 2026-09-01, released v0.6.7) re-added two prose regexes and made the gate tier-keyed again: a `not-verified` concern whose wording missed a hand-maintained "benign" whitelist fell through to a bare-unexamined-admission default and demoted a clean `approve`. That violates spec 004's Desired Behavior 4 and Acceptance Criterion 3. Each round of this bug has added phrases to a pattern over natural language; that enumeration cannot converge, which is why this prompt removes the prose layer entirely and uses the elapsed-time signal instead.
</context>

<requirements>
1. **Capture the RED evidence before changing any production code.**

   a. Add the fixture `pkg/testdata/review_bborbe_nuke_216_run1.md`. It is the verbatim `## Review` section body of the run-1 review on `bborbe/nuke#216` (2026-09-10, review_id `5172635280`), where `ParseVerdict` yields `approve` and all four `concerns_addressed` entries carry `disposition: not-verified` with benign operational wording. Write it byte-for-byte as given in the **Verbatim fixture for requirement 1a** block below this list, with no added header, comment, indentation, or reformatting — the surrounding prose is part of the fixture (a paraphrase would destroy the regression value). The four `SENTRY_DSN_KEY` token values in that block are **pre-redacted to the same-length placeholders** `AAAAAA`, `BBBBBB`, `CCCCCC`, `DDDDDD` (6 chars each, matching the originals): write the placeholders, never the originals. This repo (`bborbe/github-pr-review-agent`) is public while the source PR (`bborbe/nuke#216`) is private, so committing the real key identifiers would publish private-repo credentials — and nothing the fixture locks reads them: the gate keys on `disposition` alone, so the redaction costs the fixture no regression value.


   b. Add a **temporary** Ginkgo file `pkg/red_probe_test.go` (`package pkg_test`, with the same three-line license header the other `_test.go` files carry) containing the two rows below. It drives the existing `pkg.PostAndRouteForTest` — the same call shape as the `posting behavior` Describe in `pkg/steps_checkout_execution_test.go` — with `funnelRan=true` so the funnel gate cannot interfere, and asserts the verdict handed to the poster:

   ```go
   package pkg_test

   import (
   	"context"
   	"os"
   	"time"

   	agentlib "github.com/bborbe/agent"
   	"github.com/bborbe/github-pr-review-agent/mocks"
   	pkg "github.com/bborbe/github-pr-review-agent/pkg"
   	. "github.com/onsi/ginkgo/v2"
   	. "github.com/onsi/gomega"
   )

   var _ = Describe("RED probe (temporary - delete before finishing)", func() {
   	const (
   		prURL  = "https://github.com/bborbe/maintainer/pull/2"
   		taskMD = "---\nref: abc123\ntrigger_count: 1\n---\n\nReview the pull request at " + prURL + ".\n"
   	)

   	buildMD := func(ctx context.Context, reviewSection string) *agentlib.Markdown {
   		md, err := agentlib.ParseMarkdown(ctx, taskMD)
   		Expect(err).NotTo(HaveOccurred())
   		md.ReplaceSection(agentlib.Section{Heading: "## Review", Body: reviewSection})
   		return md
   	}

   	post := func(ctx context.Context, reviewSection string) pkg.Verdict {
   		poster := &mocks.PrPoster{}
   		poster.PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 1})
   		_, err := pkg.PostAndRouteForTest(ctx, poster, buildMD(ctx, reviewSection), prURL, "", time.Now(), true)
   		Expect(err).NotTo(HaveOccurred())
   		Expect(poster.PostCallCount()).To(Equal(1))
   		_, req := poster.PostArgsForCall(0)
   		return req.Verdict
   	}

   	It("row a: the nuke#216 body must post as approve", func() {
   		body, err := os.ReadFile("testdata/review_bborbe_nuke_216_run1.md")
   		Expect(err).NotTo(HaveOccurred())
   		Expect(post(context.Background(), string(body))).To(Equal(pkg.VerdictApprove))
   	})

   	It("row b: benign wording on a budget-heavy run must post as request-changes", func() {
   		body := "LGTM.\n\n```json\n" +
   			`{"verdict":"approve","reason":"clean","concerns_addressed":[{"concern":"correctness: go.mod go directive 1.27.0 dep compatibility","detail":"not verified - module files internally consistent (tidy ran, no downgrades, all hashes present) but transitive go-directive compatibility requires a Go 1.27 toolchain not available in the review sandbox; repo CI precommit (go mod tidy/verify + build) is the gate","disposition":"not-verified"}]}` +
   			"\n```\n"
   		Expect(post(context.Background(), body)).To(Equal(pkg.VerdictRequestChanges))
   	})
   })
   ```

   c. Run `go test -mod=mod ./pkg/... -count=1` and confirm **both rows fail on the current code**: row a posts `request-changes` although the review body says `approve` (the current benign whitelist does not match the TeamVault wording, so the concern falls through to the bare-admission default), and row b posts `approve` although the concern is a `not-verified` admission (the current benign whitelist matches the toolchain wording). Note both observed verdicts in your final report.

   d. **Delete `pkg/red_probe_test.go`.** It is a RED-evidence probe, not a permanent test: after the fix, row a's expectation only holds for a run that finished well inside its budget and row b's only holds for a budget-heavy run, neither of which the budget-less `PostAndRouteForTest` can express. Requirements 7 and 8 replace it with permanent rows. Do NOT weaken or extend `PostAndRouteForTest`'s zero-budget behaviour to make the probe pass.

2. **Return the run's elapsed time from the soft-budget runner.** In `pkg/soft_budget.go`, change `runWithSoftBudget` to return a fourth value, the elapsed wall-clock time of the run: `(*claudelib.ClaudeResult, error, bool, time.Duration)` — result, error, expired, elapsed. Measure it around the `runner.Run(runCtx, prompt)` call with `time.Since` (start the clock immediately before the call, read it immediately after), mirroring the elapsed measurement that already exists in `pkg/funnel.go` (`start := time.Now()` before the work, `time.Since(start)` after it) — do not invent a clock abstraction or inject a time getter. Add the stdlib `time` import, keep the existing `//nolint:revive` directive and update its explanation from the `(result, error, expired)` triple to the four-value contract, and update the doc comment to describe the four-value contract (the elapsed is what the concerns gate keys on; expiry is still detected from `runCtx.Err()`, never inferred from the returned error).

   Update **all three** call sites in the same change — Go has no default parameters, so a missed call site is a compile error:
   - `pkg/steps_checkout_execution.go` — `runClaude` keeps the elapsed (requirement 5).
   - `pkg/steps_review.go` — `reviewStep.Run` discards it with `_` (the ai-review phase posts no review and has no concerns gate).
   - `pkg/steps_planning.go` — `planningStep.Run` discards it with `_` (same reason).

   Do not otherwise change the budget contract: `budgetExpiredResult`, the expiry routing, and the salvage path are unchanged.

3. **Make `HasUnverifiedConcerns` a pure admission reader and delete the prose layer.** In `pkg/verdict.go`:

   - Delete `mustTierBlockerPattern`, `benignVerificationGapPattern`, and `unverifiedConcernDemotes` (the `var ... = regexp.MustCompile(...)` declarations with their doc comments, and the predicate function). After this change `verdictFieldRegexp` is the only `regexp.MustCompile` left in the file; keep the `regexp` import.
   - Rewrite `HasUnverifiedConcerns` keeping its exact signature `func HasUnverifiedConcerns(reviewText string) bool` and this contract (spec 004 Desired Behavior 3 and 5): return true iff at least one `concerns_addressed` entry is an unexamined admission —
     - an object entry whose `disposition` is neither `addressed` nor `not-an-issue` (including absent and unrecognised values, which stay fail-safe) is an admission → true;
     - a bare-string entry whose lowercased text contains `not verified` or `unverified` is an admission → true (this is the legacy path from spec 004 Desired Behavior 5, implemented with `strings.Contains`, never a regex);
     - every other entry is skipped and the scan continues; scan the whole list rather than returning false on the first non-admission;
     - a missing or malformed verdict block, an empty `concerns_addressed`, or a `concerns_addressed` value that is not a list still returns false (no over-trigger).
   - Concern prose is never inspected on the object path: declare only the `disposition` field on the local unmarshal struct (drop the `concern` and `detail` fields), so no code path can read the prose.
   - Refresh the doc comments of `HasUnverifiedConcerns` and `ReasonConcernsNotVerified` to describe the new trigger. `ReasonConcernsNotVerified`'s string value and `isFailClosedReason` are unchanged (spec 004 Desired Behavior 6) — `isFailClosedReason` compares against the literal.

4. **Add the budget-keyed demotion predicate** next to `HasUnverifiedConcerns` in `pkg/verdict.go`:

   - `func DemotesUnverifiedConcerns(reviewText string, elapsed, budget time.Duration) bool` (stdlib `time`) — the demotion decision for an `approve`: the review flags an unexamined concern **and** the run consumed at least 0.8 of its soft budget. Add the `time` import. The predicate reads no clock of its own: `elapsed` is the value requirement 2 measures with the same `start := time.Now()` / `time.Since(start)` shape `pkg/funnel.go` already uses — do not add a `time.Now()` call inside the predicate, a clock abstraction, or an injected time getter.
   - Express the 0.8 threshold once, as an unexported constant pair `unverifiedConcernsBudgetNumerator = 4` and `unverifiedConcernsBudgetDenominator = 5`, and compare with the integer form `elapsed*unverifiedConcernsBudgetDenominator >= budget*unverifiedConcernsBudgetNumerator`. Rationale to record in the doc comment: the integer form is exact at the threshold, and it never divides by the budget — a zero or unknown budget therefore demotes (fail-safe) instead of producing a NaN comparison that would silently let the approve through.
   - Doc comment must state the reasoning the fix rests on: a `not-verified` disposition on a run that finished well inside the budget is provably a mislabel (the model had budget left and examined the concern), so the `approve` stands; on a run that consumed the budget the disposition is credible and the `approve` fail-closes with `ReasonConcernsNotVerified`. Prose is never inspected.

5. **Wire the elapsed through the execution step into the demotion site.** In `pkg/steps_checkout_execution.go`:

   - `runClaude` captures the elapsed returned by `runWithSoftBudget` and passes it to `postAndRoute`. Do not change the expiry early-return above it: a budget-expired run still returns `budgetExpiredResult("execution", s.maxDuration)` and never reaches `postAndRoute`, which is why the threshold keys on the fraction of budget consumed and not on expiry.
   - `postAndRoute` takes a new final parameter `runElapsed time.Duration` and its concerns gate becomes `DemotesUnverifiedConcerns(reviewBody, runElapsed, s.maxDuration.Duration())`, still guarded by `verdict.Verdict == VerdictApprove`, still producing `Result{Verdict: VerdictRequestChanges, Reason: ReasonConcernsNotVerified}`.
   - Keep the gate order and precedence exactly as they are (funnel first, then concerns, then `ApplyBlockingGate`), and rewrite the comment above the concerns gate so it describes the budget-keyed rule instead of the removed prose tiering. The funnel gate, `ApplyBlockingGate`, `resolvePRInfo`, and the diagnostics block are untouched.

6. **Expose a budget-aware posting helper for tests.** In `pkg/export_test.go`, keep `PostAndRouteForTest` with its exact current signature and behaviour (it constructs a step with no configured budget, so the concerns gate fail-closes — that is what its existing rows assert, and those rows must stay green). Add a second helper `PostAndRouteWithBudgetForTest(ctx context.Context, prPoster PrPoster, md *agentlib.Markdown, prURLStr string, worktreePath string, jobRunTime time.Time, funnelRan bool, budget libtime.Duration, runElapsed time.Duration) (*agentlib.Result, error)` that constructs the step with `maxDuration: budget` and passes `runElapsed` through to `postAndRoute`. Add the `libtime "github.com/bborbe/time"` import.

7. **Regression-lock the fix in `pkg/verdict_unverified_test.go`.**

   a. Migrate the three existing rows whose expectation came from the deleted prose layer. Each now encodes the correct new contract (the disposition is the admission; only the budget decides), so their expectation becomes `true`, and their "a benign gap must not demote" intent moves to the budget-keyed table below:
      - the `benign toolchain-limited not verified (Go 1.27 toolchain unavailable, CI is the gate)` object row;
      - the `benign toolchain-limited not verified — legacy flat-string shape (octopus posted form)` row;
      - the `wording 2 not-verified passes (benign cross-check gap explained)` row of the tier-keyed table.
      Rewrite their comments to say the prose layer is gone and the elapsed/budget ratio is now the mechanism. Do NOT restore any pattern matching, and do NOT delete the rows — they keep locking the legacy `strings.Contains` path and the disposition read.

   b. Add rows to the `HasUnverifiedConcerns` table:
      - the `pkg/testdata/review_bborbe_nuke_216_run1.md` fixture, read via `os.ReadFile` inside the row (as the existing incident `Describe` does), expected `true` — four `not-verified` dispositions are four admissions;
      - the verbatim toolchain wording above as an object with `disposition: "not-verified"`, expected `true`.

      The existing table's closure takes `(reviewBody string, expected bool)` and cannot read a file — add the fixture as its own `DescribeTable` (or `It`) in the same `Describe`, mirroring the incident `Describe`'s `os.ReadFile`-in-leaf shape; leave the existing closure and its rows untouched.

   c. Replace the tier-keyed `DescribeTable` with a budget-keyed `DescribeTable` over `pkg.DemotesUnverifiedConcerns`, whose rows are `(review body, elapsed, budget, expected)`. Use a budget of 30 minutes throughout, so the ratios are explicit:
      - the nuke#216 fixture with elapsed `2m` (ratio ≈ 0.07) → `false` — the incident; a short run's `not-verified` is a mislabel and the `approve` stands;
      - the nuke#216 fixture with elapsed `27m` (ratio 0.9) → `true` — the same body on a budget-heavy run still fail-closes;
      - the verbatim toolchain wording with elapsed `2m` → `false` and with elapsed `27m` → `true` — the pair differs only in the elapsed value, proving the prose is inert in both directions;
      - the `wording 1` and `wording 3` strings from the deleted table with elapsed `2m` → `false` (bare admissions on a short run are mislabels too);
      - boundary: elapsed `24m` of a `30m` budget (exactly 0.8) → `true`;
      - boundary: elapsed `23m59s` of a `30m` budget (just below 0.8) → `false`;
      - unknown budget: elapsed `0`, budget `0` → `true` (fail-safe, and locks the no-division requirement);
      - an object with `disposition: "not-an-issue"` and elapsed `27m` → `false` (no admission, so no demotion regardless of budget).
      Give each `Entry` a description naming the ratio, and put the incident provenance in a comment above the first row.

   d. In the existing incident-regression `Describe`, add an `It` asserting `pkg.ParseVerdict` on the nuke#216 fixture yields `pkg.VerdictApprove` (mirroring the existing discord-fixture assertion), so the demotion rows cannot silently pass against a fixture whose verdict is not an approve.

8. **Lock the fix at the posting boundary in `pkg/steps_checkout_execution_test.go`.** In the `fail-closed gate when a concern is flagged not verified` Context, add rows that drive `pkg.PostAndRouteWithBudgetForTest` with `funnelRan=true`:
   - the nuke#216 fixture body with budget `30m` and elapsed `2m` → the poster receives `pkg.VerdictApprove` — this is the regression that must never come back;
   - the toolchain wording body with budget `30m` and elapsed `27m` → the poster receives `pkg.VerdictRequestChanges` — the fail-close is preserved where it serves.
   Leave the existing zero-budget rows in that Context untouched. These two rows are the permanent replacements for the deleted probe and are the only place where the elapsed reaches the gate through the real demotion site.

9. **Stop the model-facing schema teaching the deleted escape hatch.** The paragraph in `pkg/prompts/execution_output-format.md` that begins "A concern listed as `not-verified` fail-closes an `approve`" currently describes the tier-keyed rule and explicitly offers "explains the gap as benign" as an escape hatch — the exact mechanism that kept the prose layer alive. Rewrite that paragraph (and, if it repeats the claim, the corresponding sentence in the `timeBudgetFooter` constant in `pkg/prompts/execution.go`) to state the rule that now holds: a `not-verified` disposition means the run stopped before examining the concern, an `approve` carrying one fail-closes when the run consumed its time budget, and the model must not attempt to argue its way out of the disposition with wording — pick the disposition that is true. Keep the three enum values and the "mutually exclusive" wording intact: `pkg/prompts/execution_test.go` asserts the enum values in both, and `mutually exclusive` in the workflow footer, and those rows must stay green.

10. **Add the CHANGELOG entry.** `CHANGELOG.md` currently starts its release sections at `## v0.9.0` with no `## Unreleased` heading. Add `## Unreleased` directly below the preamble (above `## v0.9.0`) with a single `- fix:` bullet naming the false `CHANGES_REQUESTED`: the unverified-concerns gate is keyed on the run's elapsed time against the soft budget instead of on concern prose, a `not-verified` concern on a run that finished well inside the budget no longer demotes a clean approve, a budget-heavy run still fails closed, and both prose patterns are deleted. Do not rename or reorder existing release sections, and do not create a tag — this repo releases from `.maintainer.yaml` (`release.autoRelease: true`), so the releaser bot cuts the tag after merge.

11. **Self-check before finishing.** Re-run the whole `<verification>` block and confirm every command produces its stated result; then walk each row of the spec's Failure Modes table that this change touches (the "Model marks an examined concern `not-verified` anyway" row) and the constraints below against the change. Confirm `pkg/red_probe_test.go` no longer exists, that `PostAndRouteForTest`'s signature is unchanged, and that no new phrase list, keyword list, or pattern over concern prose was added anywhere in the demotion path.
</requirements>

**Verbatim fixture for requirement 1a** — write to `pkg/testdata/review_bborbe_nuke_216_run1.md` byte-for-byte, with no added header, comment, indentation, or reformatting:

````text
No `.reviewignore` file. The changed files are `dev.env` and `prod.env` — pure config/secret key references with no code. This is not a Go/Python/Node project (no go.mod, no package.json). Step 4 early exit applies: the diff cannot match any rule.

The changes are minimal and well-documented by comments:
- **dev.env**: `SENTRY_DSN_KEY` changed from `AAAAAA` → `BBBBBB`
- **prod.env**: `SENTRY_DSN_KEY` changed from `CCCCCC` → `DDDDDD`

Both comments explain the old value was renamed and now 404s; the new value resolves.

**Plan concerns assessment:**

1. **"Ensure old value is revoked in TeamVault"** — I cannot verify TeamVault revocation; this is an operational/process concern outside the diff scope. The comment in the file describes the old key as reverted/404ing, suggesting it may already be inactive.
2. **"Verify the new key reference exists and has correct permissions"** — I cannot query TeamVault from this review. The PR comment states the new key resolves, which is the best available evidence in this context.

No build, no tests, no code to review. The diff is clean from a code review perspective.

---

```json
{
  "verdict": "approve",
  "summary": "PR updates SENTRY_DSN_KEY values in dev.env and prod.env, switching back from non-resolving keys (AAAAAA → BBBBBB for dev, CCCCCC → DDDDDD for prod). Changes are minimal, self-documenting via inline comments, and involve no code — only TeamVault key references in environment files. The old keys are described as reverted/404ing, suggesting prior rotation was already performed. No actionable findings from the diff.",
  "comments": [],
  "concerns_addressed": [
    {
      "concern": "security: SENTRY_DSN_KEY value updated — ensure old value is revoked in TeamVault",
      "disposition": "not-verified",
      "detail": "Cannot verify TeamVault revocation from this review context. The file comments describe the old key as 'reverted/404s', suggesting the prior rotation already rendered it inactive. Revocation confirmation is an operational concern outside diff scope."
    },
    {
      "concern": "security: SENTRY_DSN_KEY value updated — ensure old value is revoked in TeamVault",
      "disposition": "not-verified",
      "detail": "Cannot verify TeamVault revocation from this review context. The file comments describe the old key as 'reverted/404s', suggesting the prior rotation already rendered it inactive. Revocation confirmation is an operational concern outside diff scope."
    },
    {
      "concern": "correctness: verify the new key reference BBBBBB exists and has correct permissions",
      "disposition": "not-verified",
      "detail": "Cannot query TeamVault from this review context. The PR comment states the key 'resolves', which is the available evidence. Permissions verification is an operational concern outside diff scope."
    },
    {
      "concern": "correctness: verify the new key reference DDDDDD exists and has correct permissions",
      "disposition": "not-verified",
      "detail": "Cannot query TeamVault from this review context. The PR comment states the key 'resolves', which is the available evidence. Permissions verification is an operational concern outside diff scope."
    }
  ]
}
```
````


<constraints>
- **No new prose pattern or phrase list in the demotion path** (spec 004 Desired Behavior 4 and Acceptance Criterion 3). `grep -c 'MustCompile' pkg/verdict.go` must return exactly 1 — `verdictFieldRegexp`, which `ParseVerdict` owns. The legacy bare-string path stays a plain `strings.Contains` on the lowercased entry (spec 004 Desired Behavior 5); it exists because a task file written by an older binary is genuinely re-parsed after an upgrade.
- **Preserve the fail-close where it serves**: a run that consumed its budget and left concerns unexamined still demotes to `request-changes` with `ReasonConcernsNotVerified`. The threshold keys on the fraction of the soft budget consumed, never on expiry — a budget-expired run is routed to `human_review` in `runClaude` before `postAndRoute` and never posts at all.
- **Spec 002's contract is preserved for a credible flag** (spec 004 Constraints, qualified by its Residual-risk bullet): a review that flags a concern as not verified *because the run stopped at the budget* must not emit `approve`. Spec 004's Residual-risk bullet names the elapsed-time cross-check as the deliberate exception — a `not-verified` on a run that finished well inside its budget is provably a mislabel, so that `approve` stands. Only the encoding of "not verified" changes, from guessed-by-prose to budget-keyed. Do not invert the `pkg/verdict_unverified_test.go` fixtures; migrate them.
- **Do NOT touch** `ParseVerdict`'s block-finding (`findVerdictBlock`, `findFencedJSONVerdictBlock`, `findLastJSONVerdictBlock`, `recoverFencedVerdict`), the verdict-to-event mapping and `mapVerdictAndSummary` in `pkg/githubposter/`, the `autoApprove` gate, the mechanical funnel gate (`ReasonFunnelDidNotRun`) or its precedence over the concerns gate, `ApplyBlockingGate` / `HasBlockingFinding`, the severity roll-up, or the salvage and expiry paths. Per spec 060 and `CLAUDE.md`, the verdict alone decides the posted event.
- `comments[]` and `severity` play no part in the demotion decision and are not touched (spec 004 Constraints).
- An entry whose `disposition` is absent or holds an unrecognised value is treated as an admission and demotes when the budget is consumed (fail-safe); an entry that is neither a JSON string nor a JSON object is skipped as uninterpretable and the remaining entries are still evaluated; a `concerns_addressed` value that is not a list falls back to the permissive `false` (no over-trigger).
- `ReasonConcernsNotVerified`'s string value and `isFailClosedReason` are unchanged (spec 004 Desired Behavior 6), so the fail-closed diagnostic log in `postAndRoute` keeps firing for the same reason.
- **Sibling call sites**: `runWithSoftBudget` has exactly three call sites — `pkg/steps_checkout_execution.go` (`runClaude`), `pkg/steps_review.go` (`reviewStep.Run`), `pkg/steps_planning.go` (`planningStep.Run`). Update all three in the same change; the two non-execution phases discard the elapsed with `_`.
- Tests are Ginkgo v2 / Gomega with `DescribeTable` / `Entry` in external `*_test` packages — a stdlib `t.Run` table violates the coding plugin's MUST rule. Counterfeiter mocks live in `mocks/`. Errors use `github.com/bborbe/errors` with context wrapping; logging is `glog` with `V(n)`-gated `Info`.
- Do NOT commit — dark-factory handles git. Do NOT create or push a tag, and do NOT hand-rename a CHANGELOG release section.
- Existing tests must still pass: the three migrated rows change expectation (requirement 7a) but are neither deleted nor inverted; the tier-keyed rows are restructured into the budget-keyed table with their strings reused (requirement 7c), not dropped; every other existing row keeps its expectation.
- **File surface**: spec 004's Constraints confine the change to `pkg/verdict.go`, `pkg/prompts/execution_output-format.md`, `pkg/prompts/execution.go`, their tests, and the deploy doc — that sentence described the original fix. This follow-up deliberately widens the surface to the budget runner (`pkg/soft_budget.go`), its two non-execution call sites (`pkg/steps_review.go`, `pkg/steps_planning.go`), the execution step and demotion site (`pkg/steps_checkout_execution.go`), the `ForTest` helper (`pkg/export_test.go`), and `CHANGELOG.md`, because the elapsed signal the spec's own "Residual risk" bullet names has to be produced by the budget runner and consumed at the demotion site. Nothing else is in scope: `pkg/githubposter/`, `pkg/prompts/review_workflow.md`, `pkg/steps_override.go`, `docs/`, and `specs/` are untouched.
- Do NOT edit `specs/in-progress/004-bug-benign-gap-whitelist-demotes-clean-approve.md` — a failed fix is re-opened with a new prompt, not by rewriting the spec.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

Then confirm, in the repo root:

- `grep -c 'MustCompile' pkg/verdict.go` — must print exactly `1`.
- `grep -rE 'mustTierBlockerPattern|benignVerificationGapPattern|unverifiedConcernPattern|unverifiedConcernDemotes' pkg/ | grep -v '_test.go'` — must produce no output (no prose pattern and no prose-keyed predicate survives in production code, and the rewritten comments must not name the deleted identifiers; test-file comments may).
- `grep -c 'DemotesUnverifiedConcerns' pkg/verdict.go` — must print at least `1` (the predicate is declared where `HasUnverifiedConcerns` lives) and `grep -c 'DemotesUnverifiedConcerns' pkg/steps_checkout_execution.go` — must print at least `1` (the demotion site consumes it; the count may exceed 1 when requirement 5's rewritten comment names the predicate).
- `grep -n 'runElapsed' pkg/steps_checkout_execution.go pkg/export_test.go` — shows the elapsed reaching `postAndRoute` and the test helper.
- `go test -mod=mod ./pkg/... -count=1` — must pass, including the migrated rows, the nuke#216 fixture rows, the budget-keyed table, and the two posting-boundary rows.
- `! test -e pkg/red_probe_test.go` — the temporary RED probe is gone.
- `wc -l pkg/testdata/review_bborbe_nuke_216_run1.md` — prints `46` (the fixture, unreformatted, with the four token values replaced by same-length placeholders) and `grep -c '"disposition": "not-verified"' pkg/testdata/review_bborbe_nuke_216_run1.md` — prints `4` (all four concerns).
- `! grep -q 'explains the gap as benign' pkg/prompts/execution_output-format.md` — must exit 0 (the deleted escape hatch is gone from the model-facing schema).
- `grep -c 'not-verified' pkg/prompts/execution_output-format.md` — must print at least `1` (the enum vocabulary the gate switches on is still declared).
- `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'changes_requested'` — must print a number greater than or equal to `1`.
</verification>
