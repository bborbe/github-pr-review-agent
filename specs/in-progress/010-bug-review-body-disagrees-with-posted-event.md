---
status: prompted
approved: "2026-11-04T14:11:32Z"
generating: "2026-11-04T14:52:08Z"
prompted: "2026-11-04T17:56:12Z"
branch: dark-factory/bug-review-body-disagrees-with-posted-event
---

## Summary

- A posted review's body can advertise `verdict: approve` while GitHub records `CHANGES_REQUESTED`.
- Two independent producers: the verdict JSON block is never stripped from the body, and a demotion never says it happened.
- Readers — human and agent — triage the body, so a correct demotion reads as a verdict inversion.
- Fix: strip every verdict block structurally, and make a demoted body open with the demotion.
- The fail-closed gates themselves are correct and do not change.

## Problem

`StripJSONVerdict` locates the verdict block by scanning for a single line that contains both `"verdict"` and `"reason"`. The block the reviewer actually emits is pretty-printed across ~20 lines and carries `verdict` / `summary` / `comments` / `concerns_addressed` — no `reason` key at all. So nothing matches, nothing is removed, and the raw JSON is posted verbatim inside the review body. When the Go-side funnel gate then demotes `approve → request-changes`, the posted body still says `approve`.

The consequence is not cosmetic. On 2026-09-30 (`bborbe/vault-cli#243`) and 2026-10-01 (`bborbe/vault-cli#251`) a clean approve was demoted and the body kept advertising approve, so the operator merged on the body verdict and reopened the regression. On 2026-11-04 a *correct* `CHANGES_REQUESTED` on `bborbe/agent-task-executor#208` was reported as the same bug, because a chunked review's body carries one block per chunk and the reader saw the first one. A body that disagrees with its own event is unreadable regardless of which side is wrong.

## Goal

A posted review's body and its GitHub event always agree. A clean approve posts `APPROVED` with no verdict JSON in the body. A demoted approve posts `CHANGES_REQUESTED` and its body opens with the gate that demoted it. A genuine request-changes posts `REQUEST_CHANGES` with its prose byte-identical to the stripped body and no verdict JSON.

## Non-goals

- Changing when a demotion fires, or the gates' precedence (funnel → concerns → blocking).
- Touching `PostLGTM` or `postHallucinationComment`, or introducing any new `COMMENT` event.
- Restyling the chunked aggregate's layout — one block per chunk is how `MergeChunkReviews` composes it; this spec removes the blocks, it does not change how they are assembled.
- Suppressing or rewriting a model-authored `request-changes`.
- Fixing the `bborbe/coding` ast-grep argv defect that makes the funnel die in the first place — that is its own spec in that repo.

## Acceptance Criteria

- [ ] AC1: `StripJSONVerdict` removes a pretty-printed multi-line verdict block **whole** — not just the token. Evidence: a new test feeds the verbatim body reproduced below and asserts the result contains none of `"verdict"`, `"summary"`, `"comments"`, `"concerns_addressed"`, and no ` ``` ` fence marker, while the six numbered prose items before the block still appear; `make test` exits 0. The four content negatives are what forbid a line-deletion implementation that leaves orphan JSON in the posted body.
- [ ] AC2: A body carrying more than one verdict block has **every** block removed whole and the prose between them preserved. Evidence: a new test feeds a two-block chunked body (the `bborbe/agent-task-executor#208` shape — a per-chunk block followed by the aggregate) and asserts zero occurrences of each of `"verdict"`, `"summary"`, `"comments"`, `"concerns_addressed"`, ` ```json `, while the `### Chunk N/3` headings still appear in the output.
- [ ] AC3: The compact single-line form still strips, and no pre-existing case regressed — including `ParseVerdict`, which shares the locator. Evidence: `go test ./pkg/... -count=1` exits 0 with every `ParseVerdict` context green (the real blast radius if the locator is touched), and `git diff pkg/verdict_test.go` shows the five existing `StripJSONVerdict` contexts unchanged — in particular the "preserves other JSON in review" context, which a naive fence-truncation would break.
- [ ] AC4: A demoted approve's body **prepends** the demotion to the review and carries no approve JSON — once per gate, all three. Evidence: three table rows, each driving one gate to fire and asserting (a) the captured `PostRequest.Summary` equals `<reason> + "\n\n" + StripJSONVerdict(reviewBody)` exactly — so the first line is the reason and the full review prose survives beneath it (an implementation that sets the summary to the reason alone must fail), and (b) it does not contain `"verdict": "approve"`. Rows:

  | Gate | Reason string asserted as the first line |
  |---|---|
  | funnel did not run | `mechanical funnel did not run` |
  | unverified concerns at budget | `one or more ## Plan concerns not verified` |
  | blocking finding present | `blocking finding present` |
  | chunked fold (merged verdict set before `postAndRoute`) | `chunked review: at least one chunk requested changes` |

- [ ] AC5: A model-authored `request-changes` body is unchanged in substance — and, specifically, gains **no** demotion line. Evidence: a test asserting the captured `PostRequest.Summary` equals `StripJSONVerdict(reviewBody)` exactly, for a body whose verdict the model itself wrote as `request-changes`. This is the negative that separates "a gate demoted" from "the model chose to reject".
- [ ] AC6: No new posting path and no change to the event mapping. Evidence: `git diff pkg/githubposter/poster.go` shows no change at either pre-existing `"COMMENT"` site (`PostLGTM`, `postHallucinationComment`), and `grep -n 'case prpkg.Verdict' pkg/githubposter/poster.go` returns exactly two lines — `approve` and `request-changes`, no `COMMENT`.
- [ ] AC7: The posted-body contract is documented where it is stated. Evidence: `grep -c 'demotion' docs/pr-post-back.md` returns ≥ 1 (the word does not occur today, so this cannot pass without an edit), and the changelog bullet sits under `## Unreleased` — `awk '/^## /{sec=$0} /removes every verdict block/{print sec}' CHANGELOG.md` prints `## Unreleased`. That awk form is the repo's own changelog-fold guard, and the pattern must be a phrase unique to the new bullet: `/verdict block/` alone already matches three released sections (`## v0.11.5`, `## v0.11.4`, `## v0.11.0`). Note the repo has no `## Unreleased` heading today, so the change creates it.
- [ ] AC8: A block the locator cannot delimit is left in place rather than half-removed. Evidence: a new test feeds a body with an unbalanced/unterminated verdict block and asserts the returned text is unchanged and the posted verdict still parses; the over-matching failure mode is the one AC1–AC3 cannot see.
- [ ] AC9: `make precommit` exits 0.
- [ ] **Post-Deploy (Rung-2):** (AC10) on dev, a fresh review of a clean PR posts `APPROVED` with no `"verdict"` in the body, and a review whose funnel is disabled posts `CHANGES_REQUESTED` whose body's **first line** is the demotion reason. Evidence: `gh api repos/bborbe/pr-review-fixtures/pulls/<n>/reviews --jq '[.[] | select(.user.login=="ben-s-pull-request-reviewer-dev[bot]")] | last | {state, commit_id, submitted_at, first_line: (.body | split("\n")[0]), has_verdict: (.body | contains("\"verdict\""))}'` returns `state == "APPROVED"` with `has_verdict == false` and `first_line` not matching `demot`, and the same query on the funnel-disabled run returns `state == "CHANGES_REQUESTED"` with `first_line == "mechanical funnel did not run"` and `has_verdict == false`.
  - `deploy_check:` `kubectlnukedev -n agent get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `vX.Y.Z` — a **literal** tag, replaced with the release tag carrying the fix before this AC is run; it is not knowable at draft time, and a placeholder fails the freshness gate loudly rather than silently. **Never** `git describe --tags --abbrev=0 origin/master`: on this `autoRelease: true` repo the newest tag advances on every merge, including test-only ones, so the precondition re-breaks on any later commit and the gate can never be closed on. That correction is recorded at `specs/completed/004-bug-benign-gap-whitelist-demotes-clean-approve.md`, dated 2026-09-11, later than the spec-006 precedent it supersedes.
- [ ] **Post-Deploy (Rung-3):** (AC11) the same two observations hold on prod. Evidence: `gh api repos/bborbe/pr-review-fixtures/pulls/<n>/reviews --jq '[.[] | select(.user.login=="ben-s-pull-request-reviewer[bot]")] | last | {state, commit_id, submitted_at, first_line: (.body | split("\n")[0]), has_verdict: (.body | contains("\"verdict\""))}'` returns `state == "APPROVED"` with `has_verdict == false` for a clean PR, and `state == "CHANGES_REQUESTED"` with `first_line == "mechanical funnel did not run"` and `has_verdict == false` for the funnel-disabled run.
  - `deploy_check:` `kubectlnukeprod -n agent get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `vX.Y.Z` — a **literal** tag, replaced with the release tag carrying the fix before this AC is run; never the newest tag on `origin/master`, which advances on every merge on this `autoRelease: true` repo

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — fmt, generate, test, lint, vet, vuln, license all clean
- `go test ./pkg/... -count=1` — unit suite passes, including the new contexts
- `grep -c 'containsVerdictJSON(' pkg/verdict.go` — returns 0, i.e. the same-line predicate no longer gates removal
- `git diff pkg/verdict_test.go` — shows added lines only; the five existing contexts are untouched

### Operator-executable (runs on the host after PR merge and deploy)

- Replay the original reproduction against the fixed build: write the embedded body into a scratch task file's `## Review` section, then `go run ./cmd/run-task --task-file=/tmp/repro.md --phase=execution --skip-post` — the task file's `## Review` section afterwards contains none of `"verdict"`, `"summary"`, `"comments"`, `"concerns_addressed"` (this is the repo build, not the deployed binary; the deployed artifact is covered by the Rung-2/Rung-3 ACs)
- `gh -R bborbe/pr-review-fixtures pr view <n> --json reviews,reviewDecision --jq '{state: .reviews[-1].state, body: .reviews[-1].body, decision: .reviewDecision}'` — for a clean approve, `decision` is `APPROVED` and `body` carries no `"verdict"`

## Desired Behavior

1. `StripJSONVerdict` locates verdict blocks the same way `ParseVerdict` already does — fence-first, brace-walk fallback — and removes **every** block in the body, not only the last one. It iterates `findVerdictBlock` until the locator reports no block remains, leaving no dangling fence markers and no more than one consecutive blank line at each seam (the existing tests already assert `NotTo(HaveSuffix("\n\n\n"))`).
2. When any gate demotes an approve to request-changes, the demotion reason is **prepended** to the stripped body: the first line names the gate in the same vocabulary the code logs (`mechanical funnel did not run`, `one or more ## Plan concerns not verified`, `blocking finding present`), and the full review prose follows unchanged beneath it. A demoted body never becomes the reason line alone. This includes the **chunked fold**: `MergeChunkReviews` sets the merged verdict to request-changes with its own reason before `postAndRoute` sees it, so that path must prepend a first line too — otherwise a chunked review posts `CHANGES_REQUESTED` with no statement of why, and stripping the blocks removes the only explanation it had.
3. A demoted body carries no `verdict` JSON of any kind — neither the original approve nor a rewritten request-changes block.
4. A model-authored `request-changes` body equals `StripJSONVerdict(reviewBody)` exactly: it keeps its prose and findings verbatim, gains no demotion line, and loses only its JSON block.
5. A block the locator cannot delimit — neither fenced nor brace-matchable — is left in place. Stripping fails open; the posted event stays correct either way.
6. The posted event continues to be derived from the verdict alone, and the gates keep their current precedence and behavior; nothing about when a demotion fires changes.

## Reproduction

Run against `bborbe/github-pr-review-agent` at `v0.15.5` — the tag deployed on **both** stages (`nuke/agent/values-dev.yaml`, `values-prod.yaml`, `jobNamespace: development-sandbox`). `origin/master` is `v0.16.1`; the deployed tag is the baseline because the bug is what the deployed binary does.

1. Fetch the body of `bborbe/vault-cli#243` review `PRR_kwDORaXkPs8AAAABP62efg` (id `5363310206`, `CHANGES_REQUESTED`, 3809 characters):
   `gh api "repos/bborbe/vault-cli/pulls/243/reviews/5363310206" --jq '.body'`
2. Feed that body to `pkg.StripJSONVerdict`.
3. Observed: the returned string is byte-identical to the input — the verdict block survives.

The body is reproduced verbatim below so the container needs no GitHub access:

````text
The mock has 9 parameters matching the interface (actor as arg8, force as arg9). Let me now compile the findings and issue the verdict.

Based on my review:

1. **`--by` enforcement in cli.go:2537** — Uses `Changed("by")` (not the string value), so the "unknown" default doesn't trigger false positives on non-flag keys. ✅ Solid.

2. **Atomicity of flag+provenance write** — `writeFlagProvenance` runs before any mutation (line 131), and if it returns error the whole function returns without calling `WriteTask`. The single `WriteTask` at line 164 persists all three keys together. ✅ Correct.

3. **`flag_set_at` as bare `time.Time`** — Stored via `o.currentDateTime.Now().Time()` (line 234), which is a `time.Time` value. The test at line 718-732 verifies yaml.v3 renders it unquoted and round-trips correctly. ✅ Good.

4. **`ClearFlag` removes all three keys** — `ClearFlag()` at task_frontmatter.go:194-198 deletes flag, flag_set_by, and flag_set_at. `ClearField` also handles this at line 213-216. ✅ Correct.

5. **`flag_set_by`/`flag_set_at` direct write refusal** — `flagProvenanceSetRefusal` runs before any mutation, rejects the keys absolutely (--force doesn't bypass), writes nothing on refusal. ✅ Solid.

6. **`NewFrontmatterSetOperation` now requires `currentDateTime`** — All callers updated (cli.go, tests, mocks). The mock was regenerated with the new signature. ✅ Correct.

```json
{
  "verdict": "approve",
  "summary": "Flag provenance feature is well-implemented: --by enforcement uses Changed() so defaults don't break non-flag keys; flag+provenance are written atomically in a single WriteTask with no read window; blank actor is refused before mutation; direct provenance writes are refused absolutely; ClearFlag/ClearField remove all three keys together; the mock is regenerated with the new 9-param signature. All concerns from the plan are addressed.",
  "comments": [],
  "concerns_addressed": [
    {
      "concern": "security: new --by flag accepted only for flag key; blank actor refused - verify enforcement is solid",
      "disposition": "addressed",
      "detail": "cli.go:2537 uses cmd.Flags().Changed('by') (not the value), so --by default 'unknown' only applies when explicitly passed. Blank actor refusal is in writeFlagProvenance at frontmatter.go:226-231, before any mutation."
    },
    {
      "concern": "correctness: flag_set_at uses bare time.Time - timezone handling and YAML serialization need verification",
      "disposition": "addressed",
      "detail": "Stored as time.Time (frontmatter.go:234), test at line 718-732 verifies yaml.v3 renders it unquoted as '2026-09-30T08:00:00Z' and round-trips correctly."
    },
    {
      "concern": "correctness: WriteTaskCallCount must be exactly 1 on success, 0 on refusal - verify atomicity of flag+provenance write",
      "disposition": "addressed",
      "detail": "writeFlagProvenance runs before any mutation (line 131) and returns error without WriteTask on refusal. The single WriteTask at line 164 persists all three keys atomically. Tests verify call count 1 on success, 0 on refusal."
    },
    {
      "concern": "correctness: ClearField/ClearFlag removing all three keys - ensure no orphan provenance on partial clears",
      "disposition": "addressed",
      "detail": "ClearFlag (task_frontmatter.go:194-198) and ClearField (line 213-216) both delete flag, flag_set_by, and flag_set_at together. Tests verify all three are nil after clear."
    },
    {
      "concern": "security: direct task set flag_set_by rejected - prevents provenance injection attacks",
      "disposition": "addressed",
      "detail": "flagProvenanceSetRefusal (frontmatter.go:252-258) runs before any mutation and refuses flag_set_by and flag_set_at keys with nothing written. --force does not bypass."
    }
  ]
}
```
````

4. Second half, the demotion: a run whose funnel fails posts this body with `state: CHANGES_REQUESTED` and no sentence anywhere saying the verdict was demoted. `postAndRoute` computes `summary := StripJSONVerdict(reviewBody)` *before* the three demotion gates run, so the body never learns a gate fired.

## Expected vs Actual

**Expected** (`pkg/verdict.go` documents `StripJSONVerdict` as "removes the JSON verdict line (and surrounding code fence if present) from the review text. Returns the cleaned review text for posting as a PR comment", `docs/pr-post-back.md` states the posted summary is that stripped body, and spec 060 fixes the event as verdict-only):

- the posted body carries no verdict JSON
- a demoted body opens with the demotion

**Actual:**

- the posted body carries the full verdict JSON, so a demoted review reads as an approve
- a demoted body says nothing about the demotion

## Workaround

Operators read the GitHub event, not the body, when deciding to merge — and treat a `CHANGES_REQUESTED` with an `approve` body as ambiguous rather than as approval.

## Do-Nothing Option

Leave it. The cost is already measured: two incidents (2026-09-30, 2026-10-01) where a correct demotion was read as an approve and merged on the body verdict, and a third (2026-11-04) where a correct rejection was reported as this bug — each one an operator interrupt plus a reopened regression. The defect also degrades the reviewer's own signal: once a body can say `approve` while the event says `CHANGES_REQUESTED`, no reader can trust either half, and the operator becomes the reviewer again. The fix is two focused changes in one file pair; not doing it keeps the review body permanently unreliable and keeps generating recurrence reports that cost more than the fix.

## Why this is a bug

The function's own contract says it removes the verdict block, and every `StripJSONVerdict` test asserts that it does — but all of them use the compact single-line form, so the contract is untested against the only form the reviewer emits. The gate's behavior is correct; the body's honesty is not. Two prior rounds "fixed" this defect on other surfaces because the body lied about which verdict was posted.

## Constraints

- The fail-closed gates keep their exact precedence: funnel, then concerns, then blocking. This spec makes the body honest; it does not loosen a gate.
- `mapVerdictAndSummary` keeps mapping verdict → event only. No new posting path, no new `COMMENT` event (spec 060). The two pre-existing `"COMMENT"` call sites — `PostLGTM` and `postHallucinationComment` — are out of scope and must not be touched.
- `ParseVerdict`'s block locator is the reference implementation — reuse `findVerdictBlock` rather than forking a second locator that can drift. It may be **extended** to search the whole body (its bare-JSON fallback is anchored to a trailing 50-line window, which can miss an earlier unfenced block in a long chunked body), provided `ParseVerdict`'s own parsing behaviour is unchanged and every existing `ParseVerdict` context stays green.
- Existing behaviour for a body with no verdict block stays: returned unchanged.
- The 65,536-character truncation path (`pkg/githubposter/poster.go`) keeps working after the block is removed.
- `docs/pr-post-back.md` must state that a gate-demoted body opens with the demotion reason; `CHANGELOG.md` carries the change under `## Unreleased`.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Body carries two or more verdict blocks (chunked review) | Every block is removed; the prose between them survives | AC2 fails in CI — the locator stopped iterating |
| A block the locator cannot delimit | Left in place; event still correct (DB5) | None needed — stripping fails open by design |
| Empty body after removal | `mapVerdictAndSummary` substitutes `automated review — no summary produced` and records `soft-warning: empty summary substituted with default` in `PostResult.Warnings` (it is not logged or written to the task file — no non-test reader exists) | The `poster_test.go` "empty summary → soft-warning" context stays green; the warning is asserted there, not grepped from a task file |
| A gate demotes and the body is otherwise empty | The demotion line is prepended first, so the summary is non-empty and the empty-summary default does **not** fire | The demotion-line assertion in AC4's table covers this shape; the fixture body is the demotion line alone |
| Removal deletes real review prose | AC5's exact-equality test fails | Revert the locator change; it is over-matching |
| Removal leaves a dangling fence marker | The existing `NotTo(HaveSuffix("\n\n\n"))` context fails | Fix the seam handling in the same prompt |

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Structural verdict-block removal in `StripJSONVerdict` (all blocks, fail-open on an undelimitable one) + docs/CHANGELOG | 1, 5, 6 | 1, 2, 3, 7, 8, 9 | — |
| 2 | Demotion line prepended in `postAndRoute`, per gate, model-authored path untouched | 2, 3, 4, 6 | 4, 5, 6, 9 | prompt 1 |

Rationale: prompt 2's demotion line is prepended to a body prompt 1 has already cleaned, so the removal must land first. AC9 (`make precommit`) rides on both. AC10 and AC11 are the post-deploy observations and run after the release and the dev/prod deploys respectively — no prompt covers them.
