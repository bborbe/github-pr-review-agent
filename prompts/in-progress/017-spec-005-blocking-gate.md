---
status: approved
spec: [005-blocking-verdict]
created: "2026-09-08T15:43:03Z"
queued: "2026-09-08T15:57:25Z"
branch: dark-factory/blocking-verdict
---

# Go-side blocking gate — `ReasonBlockingFindingPresent`, comment parsing, gate composition

<summary>
- The posting path gains a fail-closed gate: an `approve` verdict whose verdict block carries any blocking comment is overridden to `request-changes` with the new reason `ReasonBlockingFindingPresent`
- The gate reads each comment's `blocking` field; when present it is authoritative regardless of severity, and a `blocking: true` with an empty `blocking_reason` still blocks (the reason is a model-quality requirement, not a gate condition)
- When `blocking` is absent, severity falls back exactly as today's roll-up did: `critical`/`major` block, `nit`/`minor` do not — no regression in either direction for a model that omits the new field
- Comment parsing is per-entry resilient: an unparseable comment, or one carrying neither `blocking` nor `severity`, is skipped; a missing or malformed `comments` list never over-triggers
- The blocking gate composes after the existing funnel and concerns gates (funnel first, then concerns, then blocking); each gate only ever converts `approve` → `request-changes`, so an earlier gate's reason is preserved
- The new reason is registered in `isFailClosedReason` so the existing fail-closed diagnostic logging fires for it
- Unit tests cover every parsing branch and the gate composition; posting-boundary tests cover the gate firing, no-over-trigger, and the funnel-first chain-precedence contract
</summary>

<objective>
Make the Go side fail-closed to `request-changes` whenever an `approve` verdict contradicts its own comments' blocking flags, while preserving today's severity roll-up exactly for models that omit the new field.
</objective>

<context>
Read `CLAUDE.md` for project conventions (error wrapping, Ginkgo v2 / Gomega tests, doc-comment style, Counterfeiter mocks).

Read these files fully:
- `pkg/verdict.go` — the file this prompt extends. Focus on:
  - `findVerdictBlock` (fenced-first, brace-walk fallback) — reused as-is by the new detector.
  - `jsonVerdict` (the `json:"verdict"`/`json:"reason"` payload used by `ParseVerdict`).
  - `HasUnverifiedConcerns` (~line 316) — the pattern the new detector mirrors: find block → unmarshal a `[]json.RawMessage` payload → per-entry switch → `false` on missing/malformed block (no over-trigger).
  - `ReasonFunnelDidNotRun` / `ReasonConcernsNotVerified` (~lines 252-259) — the const style the new reason follows.
  - `isFailClosedReason` (~line 366) — the registration point for the new reason.
  - Imports: `encoding/json`, `regexp`, `strings` are all already imported; `json.RawMessage` is already used; no import changes.
- `pkg/steps_checkout_execution.go` — `postAndRoute` (~lines 364-423). The current gate chain is:
  ```go
  if !funnelRan && verdict.Verdict == VerdictApprove {
  	verdict = Result{Verdict: VerdictRequestChanges, Reason: ReasonFunnelDidNotRun}
  }
  if verdict.Verdict == VerdictApprove && HasUnverifiedConcerns(reviewBody) {
  	verdict = Result{Verdict: VerdictRequestChanges, Reason: ReasonConcernsNotVerified}
  }
  ```
  followed by the fail-closed diagnostic `if verdict.Verdict == VerdictRequestChanges && isFailClosedReason(verdict.Reason)`.
- `pkg/verdict_unverified_test.go` — the `fence` helper (`return "# Code Review\n\n```json\n" + body + "\n```\n"`) and the `DescribeTable` pattern to mirror for the new detector.
- `pkg/verdict_diagnostics_test.go` — the `DescribeTable("classifies request-changes reasons")` for `isFailClosedReason` (uses `pkg.IsFailClosedReasonForTest`).
- `pkg/steps_checkout_execution_test.go` — the `Describe("posting behavior")` block (~line 446) with its `buildMD` helper, `prURL`, `fixedTime`, and the `&mocks.PrPoster{}` pattern (`github.com/bborbe/github-pr-review-agent/mocks`); the "fail-closed gate when a concern is flagged not verified" Context (~line 514) is the template for the new blocking-gate Contexts.
- `pkg/export_test.go` — `PostAndRouteForTest(ctx, prPoster, md, prURLStr, worktreePath, jobRunTime, funnelRan)` exposes the posting path; `IsFailClosedReasonForTest` exposes `isFailClosedReason`.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega table tests
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md` — the new doc comments start with the symbol name and describe behavior, not implementation

The contract this gate enforces (spec Desired Behavior 3, 4, 7): the `blocking`/`blocking_reason` fields and the severity fallback are defined in `pkg/prompts/execution_output-format.md`, which the previous prompt in this batch rewrote; you may read it to confirm the vocabulary, but this prompt changes no prompt files. The schema and this gate co-ship in one binary via `//go:embed`.
</context>

<requirements>
1. **Add the new fail-closed reason const to `pkg/verdict.go`**, immediately after `ReasonConcernsNotVerified`:
   ```go
   // ReasonBlockingFindingPresent is the fail-closed Result.Reason set when the
   // Go-side blocking gate sees an `approve` verdict whose verdict block carries
   // a comment marked `blocking: true` (or whose severity fallback says a
   // comment blocks) — the model contradicted itself, or its severity roll-up
   // contradicts the approve. The verdict is overridden to request-changes so
   // an approve never posts while a blocking finding exists. Recognized by
   // isFailClosedReason for logging.
   const ReasonBlockingFindingPresent = "blocking finding present"
   ```

2. **Add `HasBlockingFinding` to `pkg/verdict.go`** (place it after `HasUnverifiedConcerns`, ~line 356). Keep the exported signature `func HasBlockingFinding(reviewText string) bool`:
   ```go
   // HasBlockingFinding reports whether the review body's verdict JSON carries
   // any comment that blocks the merge. A comment's `blocking` field is
   // authoritative when present, regardless of severity and regardless of
   // whether its `blocking_reason` is empty (the reason is a model-quality
   // requirement, not a gate condition). When `blocking` is absent, severity
   // falls back: `critical` and `major` block, `nit` and `minor` do not —
   // reproducing today's severity roll-up exactly for a model that omits the
   // new field. A comment that is unparseable, or carries neither `blocking`
   // nor `severity`, is skipped and does not block; a missing or malformed
   // `comments` list never over-triggers. Returns false for a missing or
   // malformed verdict block.
   func HasBlockingFinding(reviewText string) bool {
   	block, _, ok := findVerdictBlock(reviewText)
   	if !ok {
   		return false
   	}
   	var payload struct {
   		Comments []json.RawMessage `json:"comments"`
   	}
   	if err := json.Unmarshal([]byte(block), &payload); err != nil {
   		return false
   	}
   	for _, raw := range payload.Comments {
   		var comment struct {
   			Blocking *bool  `json:"blocking"`
   			Severity string `json:"severity"`
   		}
   		if err := json.Unmarshal(raw, &comment); err != nil {
   			continue // unparseable comment: skip, never over-trigger
   		}
   		if comment.Blocking != nil {
   			if *comment.Blocking {
   				return true // explicit blocking: true wins, regardless of severity/reason
   			}
   			continue // explicit blocking: false: this comment does not block — keep scanning
   		}
   		if comment.Severity == "critical" || comment.Severity == "major" {
   			return true // severity fallback for a comment missing `blocking`
   		}
   		// nit/minor, absent severity, or unrecognised severity: does not block
   	}
   	return false
   }
   ```
   Hard constraints on this function:
   - NO new `regexp` use and NO new imports — `encoding/json` and `strings` are already imported; the detector is pure JSON parsing (spec Security: no new trust boundary).
   - The severity fallback matches exactly `critical`/`major` → block, everything else → no block (spec DB 3 + DB 7).
   - A `blocking: true` with an empty `blocking_reason` still returns `true` (spec DB 7 — the reason is not read at all, it is not a gate condition).
   - A missing/malformed `comments` list or verdict block returns `false` (no over-trigger, spec DB 7 + Security).

3. **Add the pure composition gate `ApplyBlockingGate` to `pkg/verdict.go`** (place it after `HasBlockingFinding`). This single function IS the blocking gate — the posting path calls it, and the regression-lock table (final prompt of this batch) calls it, so "reverting the blocking gate" (spec AC 5) flips the table rows:
   ```go
   // ApplyBlockingGate is the Go-side blocking override gate: an `approve`
   // verdict whose verdict block carries any blocking comment is overridden to
   // `request-changes` with ReasonBlockingFindingPresent. Every other verdict
   // passes through unchanged, so it only ever converts approve →
   // request-changes (never the reverse). Composed after the funnel and
   // concerns gates in postAndRoute, it never rewrites an already-fail-closed
   // verdict, so the earlier, coarser gates keep their reasons (funnel first).
   func ApplyBlockingGate(verdict Result, reviewText string) Result {
   	if verdict.Verdict == VerdictApprove && HasBlockingFinding(reviewText) {
   		return Result{Verdict: VerdictRequestChanges, Reason: ReasonBlockingFindingPresent}
   	}
   	return verdict
   }
   ```

4. **Register the new reason in `isFailClosedReason`** in `pkg/verdict.go`. Add `reason == ReasonBlockingFindingPresent` to the existing boolean chain (the function currently reads):
   ```go
   func isFailClosedReason(reason string) bool {
   	return reason == "empty review text" ||
   		reason == "no verdict block" ||
   		reason == ReasonFunnelDidNotRun ||
   		reason == ReasonConcernsNotVerified ||
   		reason == ReasonBlockingFindingPresent ||
   		strings.HasPrefix(reason, "malformed JSON:") ||
   		strings.HasPrefix(reason, "unknown verdict:")
   }
   ```
   Do not change any other clause; `ParseVerdict`'s four fail-closed reasons are byte-identical (spec Constraint).

5. **Wire the gate into `postAndRoute` in `pkg/steps_checkout_execution.go`** — insert immediately after the concerns-not-verified gate (after the block ending `verdict = Result{Verdict: VerdictRequestChanges, Reason: ReasonConcernsNotVerified}`), before the fail-closed diagnostic block:
   ```go
   	// Fail-closed gate: the model emitted `approve` while a comment carries
   	// `blocking: true` (or the severity fallback says a comment blocks) — the
   	// model contradicted itself. Override to request-changes so an approve never
   	// posts while a blocking finding exists. Composed AFTER the funnel and
   	// concerns gates: each gate only overrides approve → request-changes, so the
   	// earlier, coarser gates keep their reasons (funnel first, then concerns,
   	// then blocking) and this gate never rewrites an already-fail-closed verdict.
   	verdict = ApplyBlockingGate(verdict, reviewBody)
   ```
   The funnel gate (`ReasonFunnelDidNotRun`), the concerns gate (`ReasonConcernsNotVerified`), their precedence comments, and the diagnostic block are otherwise untouched (spec Constraint: funnel and concerns gates unchanged, ordered funnel-first).

6. **Create `pkg/verdict_blocking_test.go`** (new file, `package pkg_test`, same header + imports style as `pkg/verdict_unverified_test.go`) with a `Describe("HasBlockingFinding")` containing the `fence` helper and a `DescribeTable("detects blocking comments in the verdict block")` covering every branch of the new detector:
   - `blocking` true wins over critical severity → `true`
   - `blocking` false wins over critical severity (pre-existing debt) → `false`
   - `blocking` false wins over major severity → `false`
   - `blocking` true with empty `blocking_reason` still blocks → `true`
   - absent `blocking` + critical → `true` (severity fallback)
   - absent `blocking` + major → `true`
   - absent `blocking` + nit → `false`
   - absent `blocking` + minor → `false`
   - absent `blocking` + unrecognised severity (`"info"`) → `false`
   - comment with neither `blocking` nor `severity` → `false` (skipped)
   - unparseable comment (JSON number `42`) → `false` (skipped)
   - malformed comment object (e.g. `{"file":"main.go","line":5,"severity":}`) → `false` (skipped)
   - `comments` that is not a list (a string `"oops"`) → `false` (no over-trigger)
   - missing `comments` → `false`
   - missing verdict block (bare prose) → `false`
   - malformed verdict block → `false`
   - one `blocking: true` comment among non-blocking comments → `true`
   - `blocking: false` comment BEFORE a `blocking: true` comment → `true` (ordering — the early-return must not stop the scan)
   - `blocking: false` comment BEFORE a `critical`-severity comment missing `blocking` → `true` (ordering — severity fallback must not be masked)
   Use the exact row style of `pkg/verdict_unverified_test.go` (every row wrapped in `fence(...)`, a descriptive `Entry` description). Use single-line JSON bodies like the existing rows so the fence + brace-walk find them.

7. **Add an `ApplyBlockingGate` composition block** (a `Describe("ApplyBlockingGate composition")` with four `It`s) in `pkg/verdict_blocking_test.go`:
   - converts an `approve` with a blocking comment to `request-changes` with `ReasonBlockingFindingPresent`
   - passes through an `approve` with only `blocking: false` comments (reason preserved)
   - **chain-precedence (spec AC 6):** `ApplyBlockingGate(Result{Verdict: pkg.VerdictRequestChanges, Reason: pkg.ReasonFunnelDidNotRun}, <approve body with a blocking comment>)` returns the verdict UNCHANGED with reason still `pkg.ReasonFunnelDidNotRun` — the funnel gate fires first and the blocking gate never overrides it
   - **chain-precedence (concerns):** same with `pkg.ReasonConcernsNotVerified`

8. **Add the new reason to the `isFailClosedReason` table in `pkg/verdict_diagnostics_test.go`** — add this `Entry` after the "unknown verdict" row (with a comment noting it is the reason the blocking gate emits):
   ```go
   Entry("blocking finding present", pkg.ReasonBlockingFindingPresent, true),
   ```

9. **Add posting-boundary tests in `pkg/steps_checkout_execution_test.go`**, inside the existing `Describe("posting behavior")` block (after the "fail-closed gate when a concern is flagged not verified" Context, reusing `buildMD`, `prURL`, `fixedTime`, `&mocks.PrPoster{}`):
   - New `Context("fail-closed gate when a comment is blocking")` with `funnelRan=true`:
     - "fail-closes an approve carrying a blocking comment to request-changes" — body = `"LGTM.\n\n```json\n" + \`{"verdict":"approve","reason":"looks ok","comments":[{"file":"main.go","line":9,"severity":"nit","blocking":true,"blocking_reason":"isHealthy() is inverted","message":"real defect"}]}\` + "\n```\n"`; assert `req.Verdict == pkg.VerdictRequestChanges`
     - "posts an approve with a blocking:false critical comment untouched (pre-existing debt)" — body with `"blocking":false` + `"severity":"critical"`; assert `req.Verdict == pkg.VerdictApprove`
     - "posts an approve when comments carry neither blocking nor severity (per-entry skip)" — body with a comment carrying only `file`/`line`/`message`; assert `req.Verdict == pkg.VerdictApprove`
   - New `Context("chain-precedence: funnel gate fires before the blocking gate")` with `funnelRan=false` and the same blocking-comment approve body as the first test; assert `req.Verdict == pkg.VerdictRequestChanges` (the funnel gate demotes first). Add a Go comment noting the reason-preservation contract is asserted by the `ApplyBlockingGate` unit test in requirement 7 (the reason is not part of `PostRequest`).
   Give the fake poster `PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 13})` … `16` (the existing tests use 1-12).

10. **Self-check before finishing:** re-run `<verification>` and confirm it passes; walk AC 5 (the table behavior this prompt's tests establish) and AC 6 (the four greps on `pkg/verdict.go` plus the chain-precedence rows) against the change; confirm every branch of `HasBlockingFinding` (present-true, present-false, absent-critical, absent-major, absent-nit, absent-minor, absent-unrecognised, neither-field, unparseable, malformed-comment, non-list, missing-list, missing-block, malformed-block, mixed-list) is covered by a row.
</requirements>

<constraints>
- This prompt is confined to `pkg/verdict.go`, `pkg/steps_checkout_execution.go`, `pkg/verdict_blocking_test.go` (new), `pkg/verdict_diagnostics_test.go`, and `pkg/steps_checkout_execution_test.go`. Do NOT modify `pkg/prompts/*` (the schema/prompt shipped in prompt 1 of this batch), `pkg/githubposter/`, or any docs.
- `ParseVerdict`'s four fail-closed paths are byte-identical: empty text / no verdict block / malformed JSON / unknown verdict → `request-changes` with their exact existing reasons (spec Constraint).
- The funnel-did-not-run gate and the concerns-not-verified gate are unchanged, remain applied after `ParseVerdict` before posting, and stay ordered funnel-first; the blocking gate is added after them. Each gate only overrides `approve` → `request-changes` (spec Constraint).
- An explicit `blocking` field wins over severity; the severity fallback applies only when the field is absent; a comment carrying neither is skipped (spec Constraint + DB 7).
- `blocking_reason` is never a gate condition: a `blocking: true` with an empty reason still blocks (spec DB 7).
- The mechanical funnel itself (Go-side, ast-grep) is unchanged (spec Non-goals).
- Do NOT touch `CHANGELOG.md` — the changelog entry for this spec lands in the final prompt of this batch. `docs/dod.md` requires an entry under `## Unreleased`; for this four-prompt batch that criterion is satisfied by prompt 4, which lands on the same branch. Do NOT add the entry here, and do NOT report the missing CHANGELOG entry as a blocker.
- All four prompts of this batch land on the same branch before any release — the schema (prompt 1), this gate, the ai_review re-base (prompt 3), and the regression-lock (prompt 4) co-ship in one binary; do not cut a release from an intermediate state.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- AC 6 evidence on `pkg/verdict.go`: `grep -c 'ReasonFunnelDidNotRun'` returns ≥ 1, `grep -c 'ReasonConcernsNotVerified'` returns ≥ 1, `grep -c 'ReasonBlockingFindingPresent'` returns ≥ 1, and `grep -A 40 'func isFailClosedReason' pkg/verdict.go | grep -c 'ReasonBlockingFindingPresent'` returns ≥ 1.
- `grep -n 'func HasBlockingFinding' pkg/verdict.go` and `grep -n 'func ApplyBlockingGate' pkg/verdict.go` — both present.
- `grep -n 'ApplyBlockingGate' pkg/steps_checkout_execution.go` — shows the wiring after the concerns gate.
- `grep -n 'ReasonBlockingFindingPresent' pkg/verdict_diagnostics_test.go` — shows the new isFailClosedReason row.
- `go test -mod=mod ./pkg/... -count=1` — must pass, including the new detector table, the ApplyBlockingGate composition rows (chain-precedence), the isFailClosedReason row, and the posting-boundary tests.
- Coverage gate for the changed detector (fails on its own, no eyeballing): `go test -mod=mod -coverprofile=/tmp/cover.out ./pkg/ && go tool cover -func=/tmp/cover.out | awk '/HasBlockingFinding/ {gsub(/%/,"",$3); if ($3+0 < 80) {print "FAIL: coverage " $3 "% < 80%"; exit 1} print "OK: " $3 "%"}'` — the requirement-6 rows are what get it there.
</verification>

<!-- AUDITOR NOTES
1. `ApplyBlockingGate` exists as a single exported pure function (not an inline `if` in postAndRoute) specifically so spec AC 5's revert-test evidence works: the regression-lock table in the final prompt of this batch calls it, so reverting it flips rows (b) and the (c)-critical row while the (d) fail-closed rows stay green. This is the single decision; do not inline the gate in postAndRoute instead.
2. The chain-precedence reason (`ReasonFunnelDidNotRun` survives the blocking gate) is asserted at the pure-function level (requirement 7) because `PostRequest` carries only `Verdict`, not `Reason` — the posting-boundary test in requirement 9 asserts the posted verdict and defers the reason to the unit test.
3. The severity fallback reads the comment's `severity` as a raw string and matches exactly `critical`/`major`; it does not normalize case. The schema teaches only the four lowercase values, so no normalization is needed (spec Constraint: severity keeps its vocabulary).
-->
