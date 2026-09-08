---
status: completed
spec: [005-blocking-verdict]
summary: 'Decoupled blocking from severity in the execution output schema and verdict-translation footer: added the required `blocking`/`blocking_reason` comment fields to the embedded schema, rewrote `verdictTranslationFooter` to a blocking-based roll-up with the six blocking conditions and three non-blocking categories, and updated/extended the Ginkgo assembly tests to prove both changes reach the runner.'
execution_id: github-pr-review-agent-blocking-exec-016-spec-005-blocking-schema-and-prompt
dark-factory-version: dev
created: "2026-09-08T15:43:03Z"
queued: "2026-09-08T15:57:25Z"
started: "2026-09-08T15:57:27Z"
completed: "2026-09-08T16:00:38Z"
branch: dark-factory/blocking-verdict
---

# Decouple blocking from severity — verdict schema and execution prompt

<summary>
- The review output schema declares two new per-comment fields: `blocking` (required bool) and `blocking_reason` (required string when `blocking` is true), alongside the unchanged `file`, `line`, `severity`, `message`
- The schema states that `severity` orders and labels comments only and never decides the verdict — the verdict is decided by `blocking` alone
- The execution prompt's verdict-translation footer replaces the deterministic severity map with a blocking roll-up: severity still maps findings to `critical`/`major`/`nit` labels but no longer contributes to the verdict
- The footer names the six blocking conditions (build/test breakage, production regression, security defect, correctness defect, merge-gate requirement, functional no-op) that justify `blocking: true`
- The footer names the three non-blocking categories (pre-existing debt on untouched lines, stylistic/refactor suggestions, unconfirmable findings) that are surfaced but never block
- Verdict roll-up: any comment with `blocking: true` → `request-changes`; otherwise `approve`
- Tests assert the new schema fields and the new footer wording reach the assembled instructions the runner receives, so the change is wired through `//go:embed`, not a doc-only edit
- No parser, gate, or posting code changes here — this prompt is the contract half of the fix; the Go gate that reads the new fields ships next
</summary>

<objective>
Change the execution output contract so a finding blocks the merge only when its evidence demonstrates a concrete defect, and demote `severity` to a sorting/labeling role that never decides the verdict.
</objective>

<context>
Read `CLAUDE.md` for project conventions (prompt assembly in `pkg/prompts/execution.go`, embedded output format, Ginkgo test style).

Read `docs/architecture.md` — the verdict-rubric / ai_review sections: the verdict schema is `pkg/prompts/execution_output-format.md` (embedded via `//go:embed`), and `pkg/prompts/execution.go`'s `verdictTranslationFooter` is the verdict-rubric source of truth the model reads at run time.

Read these files fully:
- `pkg/prompts/execution_output-format.md` — the JSON example block (top) and the `comments` field rule (currently ~line 34). This is the schema this prompt rewrites.
- `pkg/prompts/execution.go` — the `verdictTranslationFooter` const (currently ~lines 92-110). This is the footer this prompt rewrites.
- `pkg/prompts/execution_test.go` — the `happy path` It (asserts `ContainSubstring("Severity map")` at ~line 79, which MUST be updated — it breaks under the new footer) and the `Describe("output-format schema")` block (~lines 303-340) that asserts the embedded schema content.

Read the coding plugin guide (in-container path):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega assertions

Current load-bearing content you are replacing:

Current `comments` example in `pkg/prompts/execution_output-format.md`:
```json
"comments": [
    {
      "file": "path/to/file.go",
      "line": 42,
      "severity": "critical | major | minor | nit",
      "message": "..."
    }
],
```

Current `comments` field rule (same file):
```
- Each comment requires `file`, `line`, `severity`, `message`
```

Current `verdictTranslationFooter` in `pkg/prompts/execution.go`:
```go
const verdictTranslationFooter = "---\n\n" +
	"## Final step — emit verdict JSON\n\n" +
	"After Step 7 (Manual Review) completes and the consolidated report is\n" +
	"produced, ALSO emit a JSON verdict matching the agent's frozen schema (see\n" +
	"`<output-format>`).\n\n" +
	"Severity map (deterministic):\n" +
	"- Must Fix finding → comment severity \"critical\", contributes to verdict \"request-changes\"\n" +
	"- Should Fix finding → comment severity \"major\", contributes to verdict \"request-changes\"\n" +
	"- Nice to Have finding → comment severity \"nit\"\n" +
	"- The severity \"minor\" is reserved for LLM judgment on findings that\n" +
	"  genuinely don't fit a plugin bucket; the deterministic map never emits it.\n\n" +
	"Verdict roll-up (binary — exactly one of two values):\n" +
	"- Any Must Fix present → verdict \"request-changes\"\n" +
	"- Any Should Fix present → verdict \"request-changes\"\n" +
	"- Only Nice to Have, or nothing flagged → verdict \"approve\"\n\n" +
	"Each comment must pin to a real `file` and `line` from the report. If a\n" +
	"finding has no coordinates, fold it into `summary` instead of emitting an\n" +
	"un-pinned comment. Preserve the plugin's bucket label verbatim in the\n" +
	"comment `message` for traceability.\n"
```

Verified contracts (do not re-derive):
- `pkg/prompts/execution.go` imports `_ "embed"` and embeds `execution_output-format.md` into the `executionOutputFormat` string; `BuildExecutionInstructions` returns `claudelib.Instructions{{Name: "workflow", Content: assembled}, {Name: "output-format", Content: executionOutputFormat}}` — `instructions[1].Content` is the embedded schema the runner receives.
- The `concerns_addressed` disposition rules in the schema (lines ~35-58) are UNCHANGED by this prompt — the concerns gate is a separate mechanism that keeps its exact vocabulary.
- The `timeBudgetFooter` const is UNCHANGED by this prompt.
- The `funnelFailedSteerTemplate` const already contains the substring "blocking gap" (line ~87); it is NOT touched by this prompt.
</context>

<requirements>
1. **Update the `comments` example in `pkg/prompts/execution_output-format.md`** (the JSON block at the top of the file). Replace the current comment object (quoted in `<context>`) with one that carries the two new fields, using the file's existing union-annotation style (the same style as `"severity": "critical | major | minor | nit"`):
   ```json
   "comments": [
       {
         "file": "path/to/file.go",
         "line": 42,
         "severity": "critical | major | minor | nit",
         "blocking": true | false,
         "blocking_reason": "required when blocking is true; empty string otherwise",
         "message": "..."
       }
   ],
   ```
   Leave every other field in the example (`verdict`, `summary`, `concerns_addressed`) unchanged.

2. **Rewrite the `comments` field rule** in the same file. Replace the single line `- Each comment requires `file`, `line`, `severity`, `message`` with:
   ```markdown
   - Each comment requires `file`, `line`, `severity`, `blocking`, `message`;
     `blocking_reason` is required when `blocking` is true
   - `blocking`: required bool — whether the finding blocks the merge verdict.
     Mark it `true` ONLY when the evidence demonstrates a concrete defect the
     change introduces or must fix (see the blocking conditions in the
     execution workflow footer); mark it `false` for pre-existing debt on lines
     this PR did not touch, stylistic/naming/refactor suggestions, and findings
     you could not confirm (dropped per the funnel-inject adjudication contract)
   - `blocking_reason`: required string when `blocking` is true — the concrete
     defect that justifies blocking; an empty value never un-blocks
   - `severity` orders and labels comments only — it never decides the verdict.
     The verdict is decided by `blocking` alone
   ```
   Do NOT touch the `verdict` / `summary` / `concerns_addressed` field rules or the closing fence instructions in this file.

3. **Rewrite the `verdictTranslationFooter` const in `pkg/prompts/execution.go`** (replace the whole const quoted in `<context>`) with this:
   ```go
   const verdictTranslationFooter = "---\n\n" +
   	"## Final step — emit verdict JSON\n\n" +
   	"After Step 7 (Manual Review) completes and the consolidated report is\n" +
   	"produced, ALSO emit a JSON verdict matching the agent's frozen schema (see\n" +
   	"`<output-format>`).\n\n" +
   	"Severity orders and labels comments only — it never decides the verdict:\n" +
   	"- Must Fix finding → comment severity \"critical\"\n" +
   	"- Should Fix finding → comment severity \"major\"\n" +
   	"- Nice to Have finding → comment severity \"nit\"\n" +
   	"- The severity \"minor\" is reserved for LLM judgment on findings that\n" +
   	"  genuinely don't fit a plugin bucket; the deterministic map never emits it.\n\n" +
   	"Every comment carries a `blocking` bool, and `blocking_reason` when\n" +
   	"`blocking` is true. Mark a finding `blocking: true` ONLY when its evidence\n" +
   	"demonstrates a concrete defect the change introduces or must fix:\n" +
   	"- Build/test breakage — breaks compilation, tests, or a CI gate\n" +
   	"- Production regression — data loss, crash, outage, or a broken production path\n" +
   	"- Security defect — an exploitable vulnerability or a credential leak\n" +
   	"- Correctness defect — the changed code is wrong for a real input\n" +
   	"- Merge-gate requirement — a MUST-tier requirement the repo's merge demands\n" +
   	"  that this change fails\n" +
   	"- Functional no-op — the change's stated mechanism will never work or never\n" +
   	"  fire as written\n\n" +
   	"Do NOT mark a finding `blocking: true` when it is:\n" +
   	"- Pre-existing debt on lines this PR did not touch — surface it, don't block\n" +
   	"- A stylistic, naming, or refactor suggestion\n" +
   	"- A finding you cannot confirm — drop it per the funnel-inject adjudication\n" +
   	"  contract (report only findings you verified against the worktree)\n\n" +
   	"Verdict roll-up (binary — exactly one of two values):\n" +
   	"- Any comment with `blocking: true` → verdict \"request-changes\"\n" +
   	"- Otherwise → verdict \"approve\"\n\n" +
   	"Each comment must pin to a real `file` and `line` from the report. If a\n" +
   	"finding has no coordinates, fold it into `summary` instead of emitting an\n" +
   	"un-pinned comment. Preserve the plugin's bucket label verbatim in the\n" +
   	"comment `message` for traceability.\n"
   ```
   CRITICAL string constraints: the const must contain exactly the six blocking conditions and three non-blocking categories in the vocabulary shown; it must contain the literal `blocking_reason`, at least one `request-changes`, and the exact phrase `will never work or never` (AC 2 evidence `never (work|fire)`); it must NOT contain the phrase `Severity map (deterministic)` (AC 2 evidence); it must not contain `description:` or `allowed-tools:` (the frontmatter-stripping test asserts those are absent).

4. **Update the `happy path` test in `pkg/prompts/execution_test.go`** — the `Severity map` and `Verdict roll-up` assertions (lines 79-80) break under the new footer (the severity-map phrase is gone). Replace both assertions (lines 79-80) with:
   ```go
   Expect(workflow).To(ContainSubstring("blocking"))
   Expect(workflow).To(ContainSubstring("blocking_reason"))
   Expect(workflow).To(ContainSubstring("Verdict roll-up"))
   ```
   Keep the surrounding assertions (`Final step — emit verdict JSON`, `TARGET_BRANCH**: main`, `mode**: standard`, `Procedure body line 1.`, `NotTo(ContainSubstring("description: Test plugin"))`) unchanged.

5. **Add assembly assertions for the new schema fields** in the existing `Describe("output-format schema")` block in `pkg/prompts/execution_test.go`. In the first `It` of that block (the one asserting the embedded schema content, ~line 304), add these assertions on `outputFormat := instructions[1].Content`:
   ```go
   Expect(outputFormat).To(ContainSubstring("\"blocking\""))
   Expect(outputFormat).To(ContainSubstring("blocking_reason"))
   Expect(outputFormat).To(ContainSubstring("never decides the verdict"))
   ```
   Keep the existing `"disposition"`, `not-an-issue`, `not-verified`, `"disposition": "addressed"` assertions unchanged (the disposition vocabulary is untouched).

6. **Add an assembly test for the new footer wording** (this proves the footer rewrite is wired into the prompt the runner receives, not a comment edit). Add a new `It` in the `Describe("output-format schema")` block (or a new `Describe("blocking roll-up footer")`) that calls `writePlugin(fakePlugin)` + `prompts.BuildExecutionInstructions(...)` with the same call shape as the happy-path test, then asserts on `workflow := instructions[0].Content`:
   - `ContainSubstring("blocking: true")`
   - `ContainSubstring("never work or never")`
   - `ContainSubstring("surface it, don't block")`
   - `NotTo(ContainSubstring("Severity map (deterministic)"))`

7. **Self-check before finishing:** re-run `<verification>` and confirm it passes; walk AC 1 (the three greps on `execution_output-format.md`) and AC 2 (the four greps on `execution.go`) against the change. Confirm `grep -n "Severity map" pkg/prompts/execution.go` returns 0 lines (no stale severity-map vocabulary remains in the prompt or its schema; the only remaining "Severity map" occurrence in the tests is the `NotTo(ContainSubstring("Severity map (deterministic)"))` negative assertion added in requirement 6, which is intentional).
</requirements>

<constraints>
- This prompt is confined to `pkg/prompts/execution_output-format.md`, `pkg/prompts/execution.go`, and `pkg/prompts/execution_test.go`. Do NOT touch `pkg/verdict.go`, `pkg/steps_checkout_execution.go`, `pkg/prompts/review_workflow.md`, `pkg/prompts/review_output-format.md`, `pkg/prompts/planning_*`, or `pkg/prompts/review.go` — the parser/gate and the ai_review/planning prompts are out of scope (spec Constraints).
- Do NOT change the `concerns_addressed` disposition vocabulary, the `timeBudgetFooter`, or the funnel steer templates — they are separate, unchanged mechanisms.
- Do NOT touch `CHANGELOG.md` — the changelog entry for this spec lands in the final prompt of this batch. `docs/dod.md` (the project validation checklist you self-review against) requires an entry under `## Unreleased`; for this four-prompt batch that criterion is satisfied by prompt 4, which lands on the same branch. Do NOT add the entry here, and do NOT report the missing CHANGELOG entry as a blocker.
- Between this prompt and prompt 2, the Go parser does not yet read the `blocking` field — but the severity fallback (which ships in prompt 2) reproduces today's roll-up for a model that omits the field, so no intermediate state can over-block or under-block beyond today's behavior. All four prompts of this batch land on the same branch before any release — do not cut a release from an intermediate state.
- `severity` keeps its vocabulary and its sorting/display role; it must not decide any verdict anywhere except the documented Go fallback for a comment missing `blocking` (which ships in prompt 2).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass after the footer/test updates.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- AC 1 evidence on `pkg/prompts/execution_output-format.md`: `grep -c 'blocking'` returns ≥ 3, `grep -c 'blocking_reason'` returns ≥ 1, `grep -c '"severity"'` returns ≥ 1.
- AC 2 evidence on `pkg/prompts/execution.go`: `grep -c 'Severity map (deterministic)'` returns 0, `grep -c 'blocking'` returns ≥ 1, `grep -c 'request-changes'` returns ≥ 1, `grep -E -c 'never (work|fire)'` returns ≥ 1.
- `grep -n 'Severity map' pkg/prompts/execution.go` — returns 0 lines (no stale severity-map vocabulary remains in the prompt or its schema; the only remaining "Severity map" occurrence in the tests is the `NotTo(ContainSubstring("Severity map (deterministic)"))` negative assertion added in requirement 6, which is intentional).
- `grep -n 'blocking_reason' pkg/prompts/execution_test.go` — shows the new schema + footer assembly assertions.
- `go test -mod=mod ./pkg/prompts/... -count=1` — must pass, including the updated happy-path test and the new schema/footer assembly tests.
</verification>

<!-- AUDITOR NOTES
1. AC 2's "wired, not a doc edit" requirement is enforced by requirements 5-6 (assert the assembled instructions[1].Content and the assembled workflow), mirroring the spec-002/spec-004 precedent where the wrap-up wording was asserted on the assembled prompt string.
2. The happy-path assertion `ContainSubstring("Severity map")` (execution_test.go ~line 79) is the ONE pre-existing test that breaks under the new footer; it is updated in requirement 4. Do not skip it — `make test` fails otherwise.
3. The six blocking conditions and three non-blocking categories are frozen vocabulary from spec Desired Behavior 2 — the Go gate (next prompt) does not parse them, only the model reads them, so they live in the footer/schema exactly as the spec words them.
-->
