---
status: completed
spec: [004-bug-benign-gap-whitelist-demotes-clean-approve]
summary: Rewrote concerns_addressed as a structured mutually-exclusive disposition enum in the execution output-format schema, updated timeBudgetFooter and doc comment to the enum vocabulary, and added assembly tests covering the wired output-format instruction
execution_id: github-pr-review-agent-unverified-gate-exec-013-spec-004-schema-and-prompt
dark-factory-version: dev
created: "2026-08-30T19:44:12Z"
queued: "2026-08-30T20:06:13Z"
started: "2026-08-30T20:06:15Z"
completed: "2026-08-30T20:08:58Z"
branch: dark-factory/bug-benign-gap-whitelist-demotes-clean-approve
---

# Make the `concerns_addressed` disposition a structured enum and sharpen the prompt's disposition rules

<summary>
- The review output schema declares each `concerns_addressed` entry as an object with a required `disposition` enum (`addressed` | `not-an-issue` | `not-verified`) instead of a free-text string
- The schema defines `not-verified` narrowly: only a concern the model never examined; a concern that was examined is `not-an-issue`, never `not-verified`
- The time-budget footer in the assembled execution prompt switches to the same enum vocabulary and states the three values are mutually exclusive
- The assembled `<output-format>` instruction the runner receives carries the new object shape, so the schema change is wired through `//go:embed`, not just a doc edit
- No parser, gate, or severity-rubric code changes here — this prompt is the contract half of the fix; the gate that reads the field ships next
</summary>

<objective>
Change the execution output contract so a concern's disposition is a structured, mutually-exclusive enum field the parser can read without guessing, and sharpen the prompt so a model that examined a concern marks it `not-an-issue` and reserves `not-verified` for concerns it never looked at.
</objective>

<context>
Read `CLAUDE.md` for project conventions (prompt assembly in `pkg/prompts/execution.go`, embedded output format, Ginkgo test style).

Read `docs/architecture.md` — "The Verdict Rubric": the verdict schema is `pkg/prompts/execution_output-format.md` (embedded via `//go:embed`), and `pkg/prompts/execution.go`'s `verdictTranslationFooter` is the severity-rubric source of truth (DO NOT touch it).

Read these files fully:
- `pkg/prompts/execution_output-format.md` — the JSON example block (top) and the `concerns_addressed` field rule (currently ~lines 27-32). This is the file this prompt rewrites.
- `pkg/prompts/execution.go` — the `timeBudgetFooter` const (currently the ONLY place the execution prompt defines the dispositions) and its `BuildExecutionInstructions` doc comment (~line 140). The footer is appended to the assembled workflow string, so the model reads it at run time.
- `pkg/prompts/execution_test.go` — the existing `Describe("time budget wrap-up contract")` block (~lines 274-299) that asserts the assembled workflow contains `"not verified"` (space); this assertion must be updated to the enum vocabulary, and a new assertion must cover the `<output-format>` instruction.
- `pkg/prompts/review_output-format.md` — the ai_review phase's OWN schema (`status: addressed | missed`). It is a different phase and is NOT touched; read it only to confirm you are not confusing it with the execution schema.

Read the coding plugin guide (in-container path):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega table + `It` assertions

Current load-bearing content you are replacing:

Current `concerns_addressed` field rule in `pkg/prompts/execution_output-format.md`:
```
- `concerns_addressed`: required, lists each concern from `## Plan` with
  resolution status: `addressed` (a code change or comment resolves it),
  `not an issue` (examined and confirmed), or `not verified` (investigation
  stopped at the time budget before the concern could be examined). A concern
  listed as `not verified` makes the review incomplete: an `approve` verdict
  carrying any `not verified` concern is fail-closed to `request-changes`.
```

Current JSON example `concerns_addressed` (in the example block at the top of the same file):
```json
"concerns_addressed": [
  "security: rate-limit added in handler.go:45",
  "correctness: context propagation fixed in query.go:88"
]
```

Current `timeBudgetFooter` dispositions (in `pkg/prompts/execution.go`):
```go
"Disposition EVERY concern in `## Plan` inside `concerns_addressed` with one of:\n" +
"- `addressed` — a code change or a comment resolves it\n" +
"- `not an issue` — you examined it and confirmed it is a non-issue\n" +
"- `not verified` — the time budget stopped you before you could examine it\n\n" +
```

Verified contracts (do not re-derive):
- `pkg/prompts/execution.go` imports `_ "embed"` and embeds `execution_output-format.md` into the `executionOutputFormat` string; `BuildExecutionInstructions` returns `claudelib.Instructions{{Name: "workflow", Content: assembled}, {Name: "output-format", Content: executionOutputFormat}}` — `instructions[1].Content` is the embedded schema the runner receives.
- `libtime "github.com/bborbe/time"` `Duration` has a `String()` (e.g. `25m`); the footer keeps its single `%s` budget slot.
- The existing "time budget wrap-up contract" test asserts `ContainSubstring("25m")`, `ContainSubstring("not verified")`, `ContainSubstring("STOP")`, `ContainSubstring("time budget")` on `instructions[0].Content`. Only the `not verified` assertion breaks under the new vocabulary — update it.
</context>

<requirements>
1. **Rewrite the `concerns_addressed` entry shape in `pkg/prompts/execution_output-format.md`.** In the JSON example block at the top, replace the two string entries under `"concerns_addressed"` with object entries:
   ```json
   "concerns_addressed": [
     {
       "concern": "security: rate-limit on new endpoint",
       "disposition": "addressed",
       "detail": "rate limit added in handler.go:45"
     },
     {
       "concern": "correctness: context propagation",
       "disposition": "not-an-issue",
       "detail": "examined query.go:88 — already propagated"
     }
   ]
   ```
   Leave every other field in the example (`verdict`, `summary`, `comments`) unchanged.

2. **Rewrite the `concerns_addressed` field rule** (the block quoted in `<context>`) in the same file to declare the object shape and the mutually-exclusive enum:
   ```markdown
   - `concerns_addressed`: required, one object per concern from `## Plan`. Each
     object has:
     - `concern` (required): the concern text from `## Plan`
     - `disposition` (required): exactly one of `addressed`, `not-an-issue`,
       `not-verified`
     - `detail` (optional): free-text explanation of the disposition
   - The three dispositions are mutually exclusive:
     - `addressed` — a code change or comment resolves the concern
     - `not-an-issue` — you examined the concern and confirmed it is not an issue
     - `not-verified` — the time budget stopped you before you could examine it;
       it means ONLY that you never looked at the concern. A concern you examined
       is `not-an-issue`, never `not-verified`.
   - A concern listed as `not-verified` makes the review incomplete: an `approve`
     verdict carrying any `not-verified` concern is fail-closed to
     `request-changes`.
   ```
   Do NOT touch the `verdict` / `summary` / `comments` / `severity` field rules or the closing fence instructions in this file.

3. **Rewrite the `timeBudgetFooter` const in `pkg/prompts/execution.go`** to use the enum vocabulary and state mutual exclusivity. Replace the dispositions portion (quoted in `<context>`) so the const becomes:
   ```go
   const timeBudgetFooter = "---\n\n" +
   	"## Time budget\n\n" +
   	"This run has a soft time budget of %s. When the budget is reached, STOP " +
   	"investigating immediately and write the verdict from what you already know — " +
   	"do not keep going until the job kills the run.\n\n" +
   	"Disposition EVERY concern in `## Plan` inside `concerns_addressed` with one of " +
   	"these mutually exclusive values:\n" +
   	"- `addressed` — a code change or a comment resolves it\n" +
   	"- `not-an-issue` — you examined it and confirmed it is a non-issue\n" +
   	"- `not-verified` — the time budget stopped you before you could examine it\n\n" +
   	"The values are mutually exclusive: a concern you examined is `not-an-issue`, " +
   	"never `not-verified` — `not-verified` means only that you never looked at it.\n\n" +
   	"A concern listed as `not-verified` means the review is incomplete: the verdict " +
   	"will be fail-closed to request-changes and the partial output is salvaged for a " +
   	"human. Never silently drop a concern because investigation ended.\n\n"
   ```
   CRITICAL string constraints: exactly ONE `%s` (the budget) and no other `%` character; it must not contain `description:` or `allowed-tools:` (the frontmatter-stripping test asserts those are absent); it must contain the literals `not-an-issue`, `not-verified`, and `mutually exclusive` (AC 2 evidence) as well as `STOP` and `time budget` (kept from the existing test).

4. **Update the one doc comment in `pkg/prompts/execution.go` that still says `not verified`** — the `BuildExecutionInstructions` doc comment's "flags any ## Plan concern it could not examine as `not verified`" (~line 140) — to say `not-verified`, so the source-of-truth comment matches the enum vocabulary. (The `timeBudgetFooter` doc comment above the const does not contain the phrase; leave it unchanged unless `grep -n 'not verified' pkg/prompts/execution.go` still reports it after your edits.)

5. **Update the existing wrap-up-contract test in `pkg/prompts/execution_test.go`.** In `Describe("time budget wrap-up contract")`, update the inline Go comment above the assertion (currently `// ## Plan concern as `not verified`.` at ~line 294) to say `not-verified`, then replace the assertion `Expect(workflow).To(ContainSubstring("not verified"))` with:
   ```go
   Expect(workflow).To(ContainSubstring("not-an-issue"))
   Expect(workflow).To(ContainSubstring("not-verified"))
   Expect(workflow).To(ContainSubstring("mutually exclusive"))
   ```
   Keep the existing `"25m"`, `"STOP"`, and `"time budget"` assertions and the surrounding call shape (same `prompts.BuildExecutionInstructions(ctx, claudelib.ClaudeConfigDir(tmpDir), "standard", "main", true, sampleFindings, "", libtime.Duration(25*time.Minute))` invocation).

6. **Add an assembly test for the `<output-format>` instruction** (the embedded schema the runner receives — this is what proves the schema change is wired, not a doc edit). In `pkg/prompts/execution_test.go`, add a new `It` in the same Describe (or a new `Describe("output-format schema")` block) that calls `writePlugin(fakePlugin)` + `prompts.BuildExecutionInstructions(...)` with the same arguments as the happy-path test, then asserts on `instructions[1].Content`:
   - `ContainSubstring("\"disposition\"")`
   - `ContainSubstring("not-an-issue")`
   - `ContainSubstring("not-verified")`
   - `ContainSubstring("\"disposition\": \"addressed\"")` — the bare substring `addressed` is vacuous, `concerns_addressed` already contains it

7. **Self-check before finishing:** re-run `<verification>` and confirm it passes; walk AC 1 (the four greps on `execution_output-format.md`) and AC 2 (the two greps on `execution.go`) against the change.
</requirements>

<constraints>
- This prompt is confined to `pkg/prompts/execution_output-format.md`, `pkg/prompts/execution.go`, and `pkg/prompts/execution_test.go`. Do NOT touch `pkg/verdict.go`, `pkg/steps_checkout_execution.go`, `pkg/prompts/review_output-format.md`, `pkg/prompts/review_workflow.md`, or `pkg/prompts/planning_*` — the parser/gate and the ai_review/planning prompts are out of scope (spec Constraints).
- Do NOT change the verdict severity rubric: `verdictTranslationFooter` in `pkg/prompts/execution.go` is untouched, including the `minor`-does-not-contribute note (spec Constraint).
- The enum values are the hyphenated JSON keys `addressed | not-an-issue | not-verified` — do not use the old space-separated `not an issue` / `not verified` forms in the new prompt text (they are only the legacy string path the parser handles, which is a later prompt).
- Do NOT touch `CHANGELOG.md` — the changelog entry for this spec lands in the third prompt of this batch. `docs/dod.md` (the project validation checklist you self-review against) requires an entry under `## Unreleased`; for this three-prompt batch that criterion is satisfied by prompt 3, which lands on the same branch. Do NOT add the entry here, and do NOT report the missing CHANGELOG entry as a blocker.
- Between this prompt and prompt 2, `HasUnverifiedConcerns` still unmarshals `concerns_addressed` as `[]string`, so an object-shaped list silently returns `false` (fail-open). This is acceptable only because all three prompts of this batch land on the same branch before any release — do not cut a release from an intermediate state.
- No reference-doc edits are required for this prompt: `docs/architecture.md` mentions `concerns_addressed` only as derivative ai_review prose that stays accurate, and `docs/pr-post-back.md` and `README.md` do not describe the schema. Stay inside the file fence.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass after the footer/test updates.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- AC 1 evidence on `pkg/prompts/execution_output-format.md`: `grep -c 'disposition'` returns ≥ 1, `grep -c 'not-an-issue'` returns ≥ 1, `grep -c 'not-verified'` returns ≥ 1, `grep -c 'addressed'` returns ≥ 1.
- AC 2 evidence on `pkg/prompts/execution.go`: `grep -c 'not-an-issue'` returns ≥ 1 AND `grep -c 'mutually exclusive'` returns ≥ 1.
- `grep -n 'not verified' pkg/prompts/execution.go pkg/prompts/execution_test.go pkg/prompts/execution_output-format.md` — returns 0 lines (no stale space-form vocabulary remains in the execution prompt, its schema, or its tests).
- `grep -n 'instructions\[1\].Content' pkg/prompts/execution_test.go` — shows the new output-format assembly assertion.
- `go test -mod=mod ./pkg/prompts/... -count=1` — must pass, including the updated wrap-up-contract test and the new output-format assembly test.
</verification>

<!-- AUDITOR NOTES
1. AC 2's "wired, not a comment" requirement is enforced by requirement 6 (assert the assembled instructions[1].Content), mirroring the spec-002 precedent where the wrap-up wording was asserted on the assembled prompt string.
2. The old space-form vocabulary (`not an issue`, `not verified`) is intentionally retired from the prompt text; the legacy PARSER path (a later prompt) handles pre-change data with a substring check, which is a separate concern from the prompt vocabulary.
-->
