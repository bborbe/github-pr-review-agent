---
status: approved
spec: [005-blocking-verdict]
created: "2026-09-08T15:43:03Z"
queued: "2026-09-08T15:57:25Z"
branch: dark-factory/blocking-verdict
---

# ai_review re-base — verdict consistency keyed to `blocking`, hallucination check keyed to changed files

<summary>
- The ai_review phase's verdict-consistency check is re-keyed from severity to blocking: `approve` with any `blocking: true` comment → inconsistent; `request-changes` with no `blocking: true` comment → inconsistent
- Severity no longer appears in the consistency check, so an `approve` whose critical/major comments are all `blocking: false` (pre-existing debt) is consistent instead of being flagged and routed to `human_review`
- The no-hallucination check is re-keyed from "file + line exist in the inline diff" to "the cited FILE is present in the diff's changed files" — a comment on a line outside a diff hunk but inside a changed file (a surfaced pre-existing-debt finding) is not a hallucination
- The mirroring description in `docs/architecture.md` (the ai_review consistency-check paragraph) follows the same re-key
- One file is embedded prompt text the runner ships; the other is a project doc — either way, the re-base is a doc/prompt change only, with no Go code and no tests changing
</summary>

<objective>
Re-key the ai_review meta-verdict checks so the new approve-with-debt verdicts (all comments `blocking: false`) are no longer re-flagged as inconsistent or hallucinated by the model's own sanity check.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these files fully:
- `pkg/prompts/review_workflow.md` — the ai_review-phase instructions this prompt rewrites. The two checks to change are:
  ```
  2. **No hallucinations.** For each comment in `## Review`, verify the
     cited file + line number actually exist in the inline diff provided
     under `## Environment` (`PR Diff`). A comment citing a file or line
     absent from the inline diff is a hallucination.

  3. **Verdict consistency.** Does the verdict match the comments?
     - `approve` + critical/major comments → inconsistent
     - `request-changes` + only nit/minor comments → inconsistent
  ```
  The file is embedded via `//go:embed` in `pkg/prompts/review.go` (`reviewWorkflow`), so the change reaches the runner automatically — no Go edit needed.
- `docs/architecture.md` — the "ai_review's Consistency Check" section (~lines 51-60). The two mirroring lines to re-key are:
  ```
  2. **No hallucinations** — every comment in `## Review` cites a file + line that actually exists in the inline PR diff supplied in the verifier preamble (host-fetched via `gh pr diff` and embedded under `## Environment`).
  3. **Verdict consistency** — does the executor's verdict match the severity of its comments? `approve` with critical comments = inconsistent; `request-changes` with only nits = inconsistent.
  ```

Read only to confirm you are not touching it:
- `pkg/prompts/review_output-format.md` — the ai_review phase's OWN schema (`verdict: pass | fail`, `hallucinations`, `verdict_consistency`). It does not mirror the execution comment shape and is NOT touched.

The `blocking` vocabulary the consistency check reads is declared in `pkg/prompts/execution_output-format.md`, which prompt 1 of this batch rewrote; you may read it to confirm the field names, but this prompt changes no execution prompt files.
</context>

<requirements>
1. **Rewrite check 2 (No hallucinations) in `pkg/prompts/review_workflow.md`.** Replace the check quoted in `<context>` with:
   ```markdown
   2. **No hallucinations.** For each comment in `## Review`, verify the
      cited file is present in the diff's changed files (the files touched by
      `PR Diff` under `## Environment`). A comment citing a file absent from
      the changed files is a hallucination. A comment whose line sits outside
      a diff hunk but inside a changed file is NOT a hallucination — the
      executor pinned it from the checked-out worktree (a surfaced
      pre-existing-debt finding).
   ```
   This removes the phrases `file + line number` and `actually exist in the inline diff` from the check (AC 4 evidence) and introduces the `changed file` wording.

2. **Rewrite check 3 (Verdict consistency) in `pkg/prompts/review_workflow.md`.** Replace the check quoted in `<context>` with:
   ```markdown
   3. **Verdict consistency.** Does the verdict match the comments?
      - `approve` + any comment with `blocking: true` → inconsistent
      - `request-changes` + no comment with `blocking: true` → inconsistent
      - `severity` plays no part in this check: an `approve` whose comments
        are all `blocking: false` is consistent regardless of severity
   ```
   CRITICAL text constraints: the file must NOT contain the string `critical/major` anywhere (AC 3 evidence) — the third bullet above deliberately says `regardless of severity`, not `critical/major`. The check must not reference `nit/minor` either (the old second bullet is gone). The file must contain `blocking` on at least two lines (AC 3 evidence).

3. **Re-key the two mirroring lines in `docs/architecture.md`** ("ai_review's Consistency Check", ~lines 56-57). Replace them with:
   ```markdown
   2. **No hallucinations** — every comment in `## Review` cites a file present in the diff's changed files (supplied in the verifier preamble, host-fetched via `gh pr diff` and embedded under `## Environment`); a comment on a line outside a diff hunk but inside a changed file is not a hallucination.
   3. **Verdict consistency** — does the executor's verdict match the `blocking` flags of its comments? `approve` with any `blocking: true` comment = inconsistent; `request-changes` with no `blocking: true` comment = inconsistent. Severity plays no part in the check.
   ```
   The document must no longer contain the phrase `match the severity` (AC 3 evidence) and must contain `blocking` on at least one line (AC 3 evidence).

4. **Self-check before finishing:** re-run `<verification>` and confirm it passes; walk AC 3 and AC 4 against the change. Confirm the rest of `review_workflow.md` (check 1 Concerns addressed, check 4, the Rules section) and the rest of the architecture-doc section are byte-identical.
</requirements>

<constraints>
- This prompt is confined to `pkg/prompts/review_workflow.md` and `docs/architecture.md`. Do NOT touch `pkg/prompts/review_output-format.md`, `pkg/prompts/execution_*`, `pkg/verdict.go`, `pkg/steps_review.go`, or `pkg/steps_review_test.go` — the execution schema/footer (prompt 1), the Go gate (prompt 2), and the ai_review step code are out of scope (spec Constraints + DB 5/6 are prompt-level re-keys).
- `severity` keeps its vocabulary and its sorting/display role everywhere else; it is only removed from the ai_review consistency check (spec DB 5).
- The rubric section of `docs/architecture.md` keeps its "read the prompt" source-of-truth stance (derivative by design, spec Constraint). `docs/pr-post-back.md` needs no change (spec Constraint).
- This prompt and the others in this batch land on the same branch before any release — the re-keyed checks read `blocking`, which the execution schema (prompt 1) declares; do not cut a release from an intermediate state where the checks reference a field the schema does not yet teach.
- Do NOT touch `CHANGELOG.md` — the changelog entry for this spec lands in the final prompt of this batch.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass (this prompt changes no Go code; `go test` is a no-op confirmation).
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- AC 3 evidence on `pkg/prompts/review_workflow.md`: `grep -c 'critical/major'` returns 0 AND `grep -c 'blocking'` returns ≥ 2.
- AC 4 evidence on `pkg/prompts/review_workflow.md`: `grep -c 'actually exist in the inline diff'` returns 0 AND `grep -c 'changed file'` returns ≥ 1.
- AC 3 evidence on `docs/architecture.md`: `grep -c 'match the severity'` returns 0 AND `grep -c 'blocking'` returns ≥ 1.
- `grep -n 'critical/major\|nit/minor' pkg/prompts/review_workflow.md docs/architecture.md` — returns 0 lines (no stale severity-keyed consistency vocabulary).
- `go test -mod=mod ./pkg/prompts/... ./pkg/... -count=1` — must pass unchanged (confirms no Go/test regression from the prompt re-key).
</verification>

<!-- AUDITOR NOTES
1. This prompt is prompt text + docs only; the AC 3/4 evidence is grep-based because the ai_review checks are executed by the model reading the embedded prompt, not by Go code. There is deliberately no new test here — `pkg/steps_review_test.go` tests the ai_review step's Go behavior (verifier, hallucination dismissal), which does not parse the embedded workflow text.
2. The check-2 re-key deliberately drops "file + line" in favor of "cited file ... changed files" — spec DB 6: a comment on a line outside a diff hunk but inside a changed file is a surfaced pre-existing-debt finding, not a hallucination.
3. The third consistency bullet says "regardless of severity" instead of naming critical/major specifically, because AC 3's evidence greps for `critical/major` returning 0.
-->
