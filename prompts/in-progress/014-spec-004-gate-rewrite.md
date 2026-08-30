---
status: approved
spec: [004-bug-benign-gap-whitelist-demotes-clean-approve]
created: "2026-08-30T19:44:12Z"
queued: "2026-08-30T20:06:13Z"
branch: dark-factory/bug-benign-gap-whitelist-demotes-clean-approve
---

# Rewrite the unverified-concerns gate to read the `disposition` field and delete the prose regexes

<summary>
- The demotion gate reads each concern's `disposition` field directly: only `not-verified` (or an absent/unrecognised value, treated fail-safe) demotes an `approve`
- All three prose-matching regexes (`benignGapPattern`, `mustTierBlockerPattern`, `unverifiedConcernPattern`) are deleted — no regular expression matches concern prose anywhere in the demotion path
- A legacy `concerns_addressed` entry that is a bare string falls back to a plain `strings.Contains` check on the lowercased text (`not verified` / `unverified`), so task files written before the object shape preserve spec 002's fail-closed contract
- An entry that is neither a string nor an object (number, nested array) is skipped as uninterpretable; the remaining entries are still evaluated
- A `concerns_addressed` value that is not a list at all still returns false (no over-trigger), and a missing/malformed verdict block still returns false unchanged
- The fail-closed reason string and the posting-step gate in `postAndRoute` are unchanged — the fix is confined to the detector itself
</summary>

<objective>
Make the incomplete-review protection read a structured `disposition` field instead of guessing from prose, so an `approve` whose concerns were all examined posts cleanly no matter how the model worded its explanations, while preserving the exact fail-closed contract for legacy and unknown input.
</objective>

<context>
Read `CLAUDE.md` for project conventions (error wrapping, Ginkgo v2 / Gomega tests, doc-comment style).

Read these files fully:
- `pkg/verdict.go` — the file this prompt rewrites. Focus on:
  - `verdictFieldRegexp` (`var verdictFieldRegexp = regexp.MustCompile(...)` near the top) — the ONLY `MustCompile` that may remain in the package after this change; `ParseVerdict` owns it and it is frozen by the spec.
  - `unverifiedConcernPattern`, `mustTierBlockerPattern`, `benignGapPattern` (currently ~lines 260-288) — all three are DELETED by this prompt.
  - `HasUnverifiedConcerns` (currently ~line 299) — the function this prompt rewrites.
  - `ReasonConcernsNotVerified` and `isFailClosedReason` — NOT changed by this prompt.
  - Imports: `encoding/json`, `regexp`, `strings` are all already imported; no import changes.
- `pkg/verdict_unverified_test.go` — the table-driven test file this prompt updates. Note the `fence` helper (wraps a JSON body in a markdown header plus a fenced code block, per the existing rows), the existing rows (legacy bare strings, two benign-string rows currently expecting `false`, the MUST-tier string row expecting `true`, and the no-flag/empty/missing/malformed rows expecting `false`), and the `DescribeTable` pattern to extend.
- `pkg/steps_checkout_execution.go` — `postAndRoute` (READ ONLY). The gate that consumes this detector is:
  ```go
  if verdict.Verdict == VerdictApprove && HasUnverifiedConcerns(reviewBody) {
  	verdict = Result{Verdict: VerdictRequestChanges, Reason: ReasonConcernsNotVerified}
  }
  ```
  It is NOT modified by this prompt; it only relies on `HasUnverifiedConcerns` returning the new semantics. The funnel gate (`ReasonFunnelDidNotRun`) above it and its precedence comment (~line 404) are untouched.
- `docs/architecture.md` — the fail-closed verdict parsing contract, for context.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega table tests
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md` — the rewritten doc comment should start with the function name and describe behavior, not implementation

The contract this gate preserves (from spec 002 DB 4, unchanged): a review that flags any concern as not verified must not emit `approve`. Only the ENCODING changes — from regex-guessed to field-read. The `disposition` field and the three enum values (`addressed` | `not-an-issue` | `not-verified`) are defined in `pkg/prompts/execution_output-format.md`, which the previous prompt in this batch rewrote; you may read it to confirm the vocabulary, but this prompt changes no prompt files.
</context>

<requirements>
1. **Delete the three prose regexes from `pkg/verdict.go`.** Remove `unverifiedConcernPattern`, `mustTierBlockerPattern`, and `benignGapPattern` (the `var ... = regexp.MustCompile(...)` declarations and their doc comments, currently ~lines 260-288). After deletion, the ONLY `regexp.MustCompile` remaining in `pkg/verdict.go` must be `verdictFieldRegexp`. Keep the `regexp` import — `verdictFieldRegexp` still uses it.

2. **Rewrite `HasUnverifiedConcerns` in `pkg/verdict.go`.** Keep the signature `func HasUnverifiedConcerns(reviewText string) bool`. Replace the body and doc comment with this implementation:
   ```go
   // HasUnverifiedConcerns reports whether the review body's verdict JSON marks any
   // ## Plan concern as unexamined. concerns_addressed entries are objects carrying
   // a three-value `disposition` field (`addressed` | `not-an-issue` |
   // `not-verified`); an approve carrying a `not-verified` disposition (or an
   // absent/unrecognised disposition value, treated fail-safe) must be demoted to
   // request-changes (see postAndRoute) so an incomplete review can never
   // green-light a PR. Concern prose is never inspected. A legacy entry that is a
   // bare string falls back to a plain substring check (`not verified` /
   // `unverified`, lowercased) so task files written before the object shape
   // preserve spec 002's contract. Returns false for a missing/malformed verdict
   // block, an empty concerns list, or a concerns_addressed value that is not a
   // list (no over-trigger).
   func HasUnverifiedConcerns(reviewText string) bool {
   	block, _, ok := findVerdictBlock(reviewText)
   	if !ok {
   		return false
   	}
   	var payload struct {
   		ConcernsAddressed []json.RawMessage `json:"concerns_addressed"`
   	}
   	if err := json.Unmarshal([]byte(block), &payload); err != nil {
   		return false
   	}
   	for _, raw := range payload.ConcernsAddressed {
   		// Legacy bare-string entry: today's substring rule, plain strings.Contains.
   		var s string
   		if err := json.Unmarshal(raw, &s); err == nil {
   			lower := strings.ToLower(s)
   			if strings.Contains(lower, "not verified") || strings.Contains(lower, "unverified") {
   				return true
   			}
   			continue
   		}
   		// Object entry: the disposition field is authoritative; prose is inert.
   		var obj struct {
   			Disposition string `json:"disposition"`
   		}
   		if err := json.Unmarshal(raw, &obj); err == nil {
   			switch obj.Disposition {
   			case "addressed", "not-an-issue":
   				continue
   			default: // not-verified, absent, or unrecognised -> fail-safe demote
   				return true
   			}
   		}
   		// Neither a string nor an object (number, nested array): uninterpretable —
   		// skip it and evaluate the remaining entries.
   	}
   	return false
   }
   ```
   Hard constraints on this function:
   - NO new `regexp` use — the legacy path is `strings.Contains` on the lowercased entry, never a regex (spec DB 5 + AC 3).
   - The object path inspects ONLY the `disposition` field; the `concern`/`detail` prose is never read (spec DB 3 + Goal).
   - An object whose `disposition` is absent or unrecognised falls into the `default` case and demotes (fail-safe, spec Constraint). The enum is matched exactly (`addressed`, `not-an-issue`) — no underscore/space normalization.
   - `ReasonConcernsNotVerified` and `isFailClosedReason` are NOT modified (spec DB 6).
   - `ReasonConcernsNotVerified`'s VALUE is unchanged (spec DB 6), but refresh its doc comment (`pkg/verdict.go` ~line 254) to describe the new trigger: a concern carrying a `not-verified` disposition (or an absent/unrecognised one). Do not change the const's string literal — `isFailClosedReason` compares against it.

3. **Convert the two benign-string rows in `pkg/verdict_unverified_test.go`** (the rows currently expecting `false` whose prose matched the old whitelist: the config/changelog-only row and the source-not-in-repo "could not be cross-checked" row). Under the new gate those bare strings now DEMOTE via the legacy substring rule (their prose contains `not verified`), so they would fail as written. Migrate each to the object shape with `disposition: "not-an-issue"`, keeping the full verbatim prose in the `concern` field, expected `false` — this preserves their regression intent (a benign explained gap must not demote) in the new encoding and proves the prose is inert:
   - `{"verdict":"approve","concerns_addressed":[{"concern":"security: not verified (code logic not applicable — this is a config/changelog-only diff with no Go code changes)","disposition":"not-an-issue"}]}` → `false`
   - `{"verdict":"approve","concerns_addressed":[{"concern":"correctness: metric name agent_controller_results_written_total{result=\"not_found\"} — searched entire repo (go.mod, all source, all alerts) and the metric only appears in this new alert and the CHANGELOG. The agent-task-controller source is not in this repository, so the metric name and label value could not be cross-checked against the actual controller code. Not verified.","disposition":"not-an-issue"}]}` → `false`
   Update the row comments to say the benign-gap whitelist is gone and the `disposition` field is now the mechanism (the old comments reference `benignUnverifiedPattern`/blocker-tiering logic that no longer exists). Keep the `fence` helper wrapping. In Go raw string literals, write `result=\"not_found\"` exactly as shown (the backslash-quote is a valid JSON escape, matching the existing row's style).

4. **Keep the legacy bare-string rows as they are** (they must still demote through the new `strings.Contains` path): the lowercase `security: rate-limit not verified` row (this is the AC 8 legacy row), the uppercase `NOT VERIFIED` row, the `unverified` variant row, and the MUST-tier blocker string row (`correctness: expression references ... Must verify metric is exported before deploying.` — expected `true`). Do not rewrite the MUST-tier row in this prompt (the next prompt migrates it to the object shape). Keep the no-flag/empty-list/no-key/prose-without-block/malformed rows unchanged (expected `false`).

5. **Add new gate-behavior rows** to the same `DescribeTable` in `pkg/verdict_unverified_test.go`, all wrapped in the `fence` helper:
   - Object `disposition: "addressed"` → `false` — `{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"addressed"}]}`
   - Object `disposition: "not-an-issue"` → `false` — `{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not-an-issue"}]}`
   - Object `disposition: "not-verified"` → `true` — `{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not-verified"}]}`
   - Object with `disposition` ABSENT → `true` (fail-safe) — `{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit"}]}`
   - Object with an unrecognised `disposition` value → `true` (fail-safe) — `{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"inconclusive"}]}`
   - Mixed list (regression row, spec Constraint): one valid `not-verified` object plus one uninterpretable element → `true` — `{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not-verified"},42]}`
   - All-uninterpretable list (number + nested array) → `false` — `{"verdict":"approve","concerns_addressed":[42,["correctness: nested"]]}`
   - `concerns_addressed` that is NOT a list (a string) → `false` (falls back to today's permissive false) — `{"verdict":"approve","concerns_addressed":"oops"}`
   - Object with the retired space-form value `disposition: "not an issue"` → `true` (fail-safe; the hyphenated enum is matched exactly and the space form is NOT normalized — this row locks that decision, since the space form is what the pre-v0.6.6 schema taught the model) — `{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not an issue"}]}`

   Additionally add one `It` (not a table row) that locks the schema↔gate contract: assert the embedded execution schema the runner receives declares exactly the disposition values the gate accepts. Get it via `prompts.BuildExecutionInstructions(...)` `instructions[1].Content` (same call shape as `pkg/prompts/execution_test.go`) and assert `ContainSubstring` for each of `"addressed"`, `"not-an-issue"`, `"not-verified"`. Place it in `pkg/prompts/execution_test.go` if the import direction is cleaner there. Rationale: a drift between the schema the model reads and the enum the gate switches on demotes every approve fail-safe — this is the only pre-deploy guard.
   Give each row a descriptive Entry description (e.g. "object disposition addressed passes", "object disposition absent demotes (fail-safe)", "mixed list demotes on the valid not-verified entry").

6. **Self-check before finishing:** re-run `<verification>` and confirm it passes; walk AC 3 (the package-wide pattern-variable grep returns 0 and `MustCompile` count is exactly 1) and AC 8 (the legacy bare-string row demotes) against the change. Also confirm every new branch of `HasUnverifiedConcerns` (legacy-hit, legacy-miss, addressed, not-an-issue, not-verified, absent, unrecognised, uninterpretable-skip, all-skipped, non-list, missing block, malformed block) is covered by a row in this file.
</requirements>

<constraints>
- This prompt is confined to `pkg/verdict.go` and `pkg/verdict_unverified_test.go`. Do NOT modify `pkg/steps_checkout_execution.go` (the `postAndRoute` gate code, the funnel gate, and the precedence comment are untouched — spec Constraint), `pkg/githubposter/`, `pkg/prompts/*`, or any docs.
- No regular expression may match concern prose anywhere in the demotion path (spec DB 4 + AC 3): the legacy path is `strings.Contains` on the lowercased entry; `grep -c 'MustCompile' pkg/verdict.go` must be exactly 1 (`verdictFieldRegexp`).
- `ReasonConcernsNotVerified` and `isFailClosedReason` are unchanged (spec DB 6); the fail-closed diagnostic log in `postAndRoute` keeps firing for the same reason string.
- An absent or unrecognised `disposition` demotes (fail-safe); an entry that is neither string nor object is skipped, not a parse failure (spec Constraints).
- `comments[]` and `severity` play no part in the demotion decision and are not touched (spec Constraint).
- Do NOT touch `CHANGELOG.md` — the changelog entry for this spec lands in the third prompt of this batch. `docs/dod.md` (the project validation checklist you self-review against) requires an entry under `## Unreleased`; for this three-prompt batch that criterion is satisfied by prompt 3, which lands on the same branch. Do NOT add the entry here, and do NOT report the missing CHANGELOG entry as a blocker.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass; the two benign rows are migrated (requirement 3), not deleted, and their expectations stay `false`.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- AC 3 evidence: `grep -rnE 'benignGapPattern|mustTierBlockerPattern|unverifiedConcernPattern' pkg/` returns 0 lines; `grep -c 'MustCompile' pkg/verdict.go` returns exactly 1.
- `grep -n 'strings.Contains' pkg/verdict.go` — shows the legacy substring path (no regex).
- `grep -n 'disposition' pkg/verdict.go` — shows the field read in `HasUnverifiedConcerns`.
- `grep -n 'json.RawMessage' pkg/verdict.go` — shows the mixed-type payload.
- `go test -mod=mod ./pkg/... -count=1` — must pass, including the converted benign rows, the kept legacy rows, and the new gate-behavior rows.
- Coverage gate for the changed function (fails on its own, no eyeballing): `go test -mod=mod -coverprofile=/tmp/cover.out ./pkg/ && go tool cover -func=/tmp/cover.out | awk '/HasUnverifiedConcerns/ {gsub(/%/,"",$3); if ($3+0 < 80) {print "FAIL: coverage " $3 "% < 80%"; exit 1} print "OK: " $3 "%"}'` — the new rows in requirement 5 are what get it there.
</verification>

<!-- AUDITOR NOTES
1. The two benign-string rows MUST be migrated in THIS prompt, not the next: under the new legacy substring rule their prose ("not verified" in the text) now demotes, so leaving them as strings flips their expectation and breaks `make test`. They become the first two "prose is inert" object rows; the next prompt completes the paired prose-inert table.
2. AC 8's legacy row is the kept lowercase `security: rate-limit not verified` bare string; the upper/lowercase and `unverified` variant rows add case-insensitivity coverage for the same legacy path.
-->
