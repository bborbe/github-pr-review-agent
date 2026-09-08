---
status: prompted
approved: "2026-09-08T15:36:05Z"
generating: "2026-09-08T15:38:45Z"
prompted: "2026-09-08T15:49:47Z"
branch: dark-factory/blocking-verdict
---

## Summary

- Today the verdict is deterministic and severity-driven: any Must Fix or any Should Fix finding forces `request-changes`. Because the mechanical funnel scans whole touched files, a 2-line PR on a debt-heavy file drew 57 Must-Fix findings — none introduced by the PR — and the author was blocked on debt they did not create.
- The change decouples blocking from severity: every finding carries an explicit `blocking` flag, and a finding blocks only when its evidence shows a concrete defect — a broken build or tests, a production regression, an exploitable security hole, wrong code for a real input, a failed merge-gate requirement, or a mechanism that can never work as written.
- Pre-existing debt on lines the PR did not touch, stylistic or refactor suggestions, and findings the model cannot confirm are surfaced but never block.
- The verdict becomes: any blocking finding → request changes; otherwise approve. Severity still sorts and labels findings but no longer decides the verdict.
- A Go-side safety net catches a model that says approve while marking a finding blocking, and keeps today's severity behavior exactly for models that omit the new field — no regression in either direction.

## Problem

The verdict roll-up is deterministic and total: any Must Fix or any Should Fix produces `request-changes`. Combined with a mechanical funnel that scans whole touched files, a 2-line diff on a debt-heavy file drew 57 MUST findings — none introduced by the PR — and the author was blocked on debt they did not create. Every `--admin` override erodes the merge gate further. Blocking must be decoupled from severity: severity keeps driving sorting and display; blocking drives the merge verdict.

## Goal

A finding blocks a PR only when its evidence demonstrates a concrete defect the change introduces or must fix. Pre-existing debt, style suggestions, and unconfirmable findings are reported without blocking. The posted verdict is `approve` unless a blocking finding exists, independent of how many findings the funnel surfaced or how severe they look. A model that contradicts itself — approve alongside a blocking finding — is still fail-closed to `request-changes` by the Go side, and the four existing unparseable-verdict fail-closed paths are byte-identical.

## Non-goals

- Do NOT change what the mechanical funnel flags or how it scans. Only the model's verdict assignment changes.
- Do NOT change `severity` semantics, the posting contract, or any other override gate.
- Do NOT add any config flag, opt-out knob, or tunable threshold to this feature — the blocking rules are fixed invariants.

## Acceptance Criteria

- [ ] The verdict schema declares per-comment `blocking` (bool, required) and `blocking_reason` (string, required when `blocking` is true), keeps `file`, `line`, `severity`, `message`, and states that `severity` orders and labels comments only and never decides the verdict — evidence: `grep -c 'blocking' pkg/prompts/execution_output-format.md` returns ≥ 3 AND `grep -c 'blocking_reason' pkg/prompts/execution_output-format.md` returns ≥ 1 AND `grep -c '"severity"' pkg/prompts/execution_output-format.md` returns ≥ 1
- [ ] The execution prompt replaces the deterministic severity map with a blocking roll-up that names the blocking conditions and the non-blocking categories — evidence: `grep -c 'Severity map (deterministic)' pkg/prompts/execution.go` returns 0 AND `grep -c 'blocking' pkg/prompts/execution.go` returns ≥ 1 AND `grep -c 'request-changes' pkg/prompts/execution.go` returns ≥ 1 AND `grep -E -c 'never (work|fire)' pkg/prompts/execution.go` returns ≥ 1
- [ ] ai_review's verdict-consistency check is re-keyed from severity to blocking, and the mirroring line in the architecture doc follows — evidence: `grep -c 'critical/major' pkg/prompts/review_workflow.md` returns 0 AND `grep -c 'blocking' pkg/prompts/review_workflow.md` returns ≥ 2 AND `grep -c 'match the severity' docs/architecture.md` returns 0 AND `grep -c 'blocking' docs/architecture.md` returns ≥ 1
- [ ] ai_review's no-hallucination check no longer flags a comment whose line sits outside a diff hunk but inside a changed file — evidence: `grep -c 'actually exist in the inline diff' pkg/prompts/review_workflow.md` returns 0 AND `grep -c 'changed file' pkg/prompts/review_workflow.md` returns ≥ 1
- [ ] The `pkg/verdict_test.go` table (extending the existing DescribeTable) covers all four cases green: (a) an approve verdict with a critical/major-severity comment marked `blocking: false` (pre-existing debt) yields `approve`; (b) a comment marked `blocking: true` while the model verdict is `approve` yields `request-changes` with reason `ReasonBlockingFindingPresent`; (c) a comment with the `blocking` field absent yields `request-changes` at `critical`/`major` severity and `approve` at `nit`/`minor`; (d) the four fail-closed paths (empty text / no verdict block / malformed JSON / unknown verdict) still yield `request-changes` — evidence: `go test ./pkg/...` exits 0 with all rows green, and reverting the blocking gate flips rows (b) and the (c)-critical row while rows (d) stay green
- [ ] The override chain preserves the two existing gates and adds the blocking gate after them, funnel first; the new reason is registered for fail-closed diagnostics — evidence: `go test ./pkg/...` exits 0 with a chain-precedence row (funnel-did-not-run + a blocking finding + approve → reason `ReasonFunnelDidNotRun`) green AND `grep -c 'ReasonFunnelDidNotRun' pkg/verdict.go` returns ≥ 1 AND `grep -c 'ReasonConcernsNotVerified' pkg/verdict.go` returns ≥ 1 AND `grep -c 'ReasonBlockingFindingPresent' pkg/verdict.go` returns ≥ 1 AND `grep -A 40 'func isFailClosedReason' pkg/verdict.go | grep -c 'ReasonBlockingFindingPresent'` returns ≥ 1
- [ ] `make precommit` exits 0 and the changelog names the change — evidence: `make precommit` exit 0 AND `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'blocking'` returns ≥ 1
- [ ] **Post-Deploy (Rung-2):** on fixture PR #1 (`bborbe/pr-review-fixtures#1`, `fix/minimal-diff-on-debt`, head `7f5a1585`) — a one-line port bump in a debt-heavy file — the newest review posted by the dev bot is `APPROVED` (verdict NOT `request-changes`) at the pinned head, and its body surfaces at least one pinned finding — evidence: `gh api repos/bborbe/pr-review-fixtures/pulls/1/reviews --jq '[.[] | select(.user.login=="ben-s-pull-request-reviewer-dev[bot]")] | last | {state, commit_id, body}'` returns `state == "APPROVED"`, `commit_id == "7f5a15851699da359047a47c2af329474c9d48c8"`, and `body` matches `main\.go:[0-9]+` at least once.
  - `deploy_check:` `kubectlnukedev -n dev get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git fetch --tags -q; git describe --tags --abbrev=0 origin/master)`
- [ ] **Post-Deploy (Rung-2):** on fixture PR #2 (`bborbe/pr-review-fixtures#2`, `fix/real-defect`, head `03448af5`) — an inverted `isHealthy()` — the newest review posted by the dev bot is `CHANGES_REQUESTED` at the pinned head — evidence: `gh api repos/bborbe/pr-review-fixtures/pulls/2/reviews --jq '[.[] | select(.user.login=="ben-s-pull-request-reviewer-dev[bot]")] | last | {state, commit_id}'` returns `state == "CHANGES_REQUESTED"` and `commit_id == "03448af503d9cf626bf7d4bb4cae23b7ea4e5db1"`.
  - `deploy_check:` `kubectlnukedev -n dev get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git fetch --tags -q; git describe --tags --abbrev=0 origin/master)`

## Verification

## Container-executable (runs inside the YOLO container at prompt time)

- `go test ./pkg/...` — table rows (a)-(d), the chain-precedence row, and all existing rows green
- `make precommit` — fmt, generate, test, lint, vet, vuln, license clean
- `grep -c 'blocking' pkg/prompts/execution_output-format.md` ≥ 3 AND `grep -c 'blocking_reason' pkg/prompts/execution_output-format.md` ≥ 1
- `grep -c 'Severity map (deterministic)' pkg/prompts/execution.go` returns 0 AND `grep -c 'blocking' pkg/prompts/execution.go` ≥ 1 AND `grep -E -c 'never (work|fire)' pkg/prompts/execution.go` ≥ 1
- `grep -c 'critical/major' pkg/prompts/review_workflow.md` returns 0 AND `grep -c 'blocking' pkg/prompts/review_workflow.md` ≥ 2
- `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'blocking'` ≥ 1

## Operator-executable (runs on the host after PR merge, verification ladder)

- Merge to master; the maintainer bot (`github-releaser-agent`) cuts the tag (`.maintainer.yaml` is `release.autoRelease: true`, `.dark-factory.yaml` is `autoRelease: false` — do NOT hand-tag). Read the tag with `git fetch --tags && git describe --tags --abbrev=0`.
- Deploy per `docs/releasing-github-pr-review-agent.md`: bump `agent.tag` in `values-dev.yaml` and `values-prod.yaml` AND `AGENT_TAG_dev` / `AGENT_TAG_prod` in `~/Documents/workspaces/nuke/github-pr-reviewer/Makefile`, then mirror + apply dev, then prod.
- **Allowlist prerequisite (required, not optional):** the fixture repo is NOT on the dev allowlist — `values-dev.yaml` currently sets `REPO_ALLOWLIST: github.com/bborbe/go-skeleton` for both the `watcher` and the `agent`. Add `github.com/bborbe/pr-review-fixtures` to BOTH entries, mirror + apply dev, run the ladder, then revert both entries. Without this, the reviews are refused at the allowlist gate and no review posts.
- Run both `deploy_check` commands and confirm each equals its `deploy_target` before collecting AC evidence.
- Confirm fixture PR #1 (`fix/minimal-diff-on-debt`, head `7f5a1585`) and PR #2 (`fix/real-defect`, head `03448af5`) are OPEN in `bborbe/pr-review-fixtures`, trigger a fresh review on each (watcher poll or the review trigger), wait for the new review to post, then run the two Rung-2 queries.

## Desired Behavior

1. The verdict schema gives every comment two new required fields — `blocking` (bool) and `blocking_reason` (string, required when `blocking` is true) — while `file`, `line`, `severity`, `message` are unchanged. The schema states that `severity` orders and labels comments only and never decides the verdict.
2. The execution prompt's verdict-translation footer replaces the deterministic severity map with a blocking roll-up. A finding is `blocking: true` only when its evidence demonstrates at least one blocking condition: build/test breakage (breaks compilation, tests, or a CI gate); production regression (data loss, crash, outage, broken prod path); security defect (exploitable vulnerability or credential leak); correctness defect (the changed code is wrong for a real input); merge-gate requirement (a MUST-tier requirement the repo's merge demands that the change fails); functional no-op (the change's stated mechanism will never work or never fire as written). A finding does NOT block when it is pre-existing debt on lines the PR did not touch (surface, don't block), a stylistic/naming/refactor suggestion, or a finding the model cannot confirm (dropped per the existing funnel-inject adjudication contract). Verdict roll-up: any comment with `blocking: true` → `request-changes`; otherwise `approve`.
3. After `ParseVerdict` — whose four fail-closed paths (empty text / no verdict block / malformed JSON / unknown verdict → `request-changes`) are byte-identical — the posting path inspects the verdict block's `comments[]`: an `approve` carrying any blocking comment is overridden to `request-changes` with the new fail-closed reason `ReasonBlockingFindingPresent`. A comment's `blocking` field, when present, is authoritative regardless of severity. When absent, severity falls back: `critical`/`major` → blocking, `nit`/`minor` → not blocking — reproducing today's severity roll-up exactly for a model that omits the new field, so no regression in either direction.
4. The new gate composes with the two existing post-`ParseVerdict` override gates in the posting path — funnel-did-not-run first (coarsest), then concerns-not-verified, then blocking — and every gate only ever converts `approve` → `request-changes`, never the reverse. `ReasonBlockingFindingPresent` is registered in `isFailClosedReason` so the existing fail-closed diagnostic logging fires for it.
5. ai_review's verdict-consistency check is re-keyed from severity to blocking: `approve` with any `blocking: true` comment → inconsistent; `request-changes` with no `blocking: true` comment → inconsistent. Severity no longer appears in the check, so an `approve` with critical/major comments that are all `blocking: false` is consistent. The mirroring description in the architecture doc is updated the same way.
6. ai_review's no-hallucination check treats a comment as a hallucination only when its cited FILE is absent from the diff's changed files. A comment on a line outside a diff hunk but inside a changed file — a surfaced pre-existing-debt finding — is not a hallucination, because the executor pinned it from the checked-out worktree.
7. Comment parsing is per-entry resilient: a comment that is unparseable, or carries neither `blocking` nor `severity`, is skipped and does not block; a missing or malformed `comments` list never over-triggers. A `blocking: true` with an empty `blocking_reason` still blocks — the reason is a model-quality requirement, not a gate condition.

## Constraints

- `ParseVerdict`'s four fail-closed paths are byte-identical: empty text / no verdict block / malformed JSON / unknown verdict → `request-changes` with their exact existing reasons.
- The funnel-did-not-run gate (`ReasonFunnelDidNotRun`) and the concerns-not-verified gate (`ReasonConcernsNotVerified`) are unchanged, applied after `ParseVerdict` before posting, and remain ordered funnel-first; the blocking gate is added after them. Each gate only overrides `approve` → `request-changes`.
- `severity` keeps its vocabulary and its sorting/display role; it must not decide any verdict anywhere except the documented Go fallback for a comment missing `blocking`.
- The comment fields `file`, `line`, `severity`, `message` and the posting contract (vault-first write, prior-review dismissal, verdict→event mapping, body truncation) are unchanged.
- An explicit `blocking` field wins over severity; the severity fallback applies only when the field is absent; a comment carrying neither is skipped.
- No gate ever converts `request-changes` → `approve`.
- The mechanical funnel itself (Go-side, ast-grep) is unchanged; only the model's verdict assignment changes. Funnel findings on untouched lines are surfaced as non-blocking, never dropped silently and never escalated by severity.
- Prompt and parser co-ship in one binary (`//go:embed execution_output-format.md`), so there is no schema/prompt/parser version skew.
- Tests use Ginkgo v2 / Gomega with the existing table-driven pattern in `pkg/verdict_test.go`.
- Docs: `docs/architecture.md`'s ai_review consistency-check line is re-keyed to blocking; the rubric section keeps its "read the prompt" source-of-truth stance (derivative by design). `docs/pr-post-back.md` needs no change.

## Failure Modes

| Trigger | Expected behavior | Detection | Recovery |
|---|---|---|---|
| Model emits a real defect as `blocking: false` | Approve posts for a broken change — the explicit field is authoritative by design and the Go gate cannot catch it | Review body shows a `blocking: false` on a genuine defect; the PR #2 fixture guards the intended direction | Operator forces a re-review; never re-add a severity-keyed counter-gate (it would recreate the debt-blocking incident) |
| Model omits the `blocking` field on comments (old-style output) | Severity fallback reproduces the old severity map exactly: `critical`/`major` block, `nit`/`minor` do not — no regression either direction | Table rows (c) cover both directions | None needed |
| `blocking: true` with an empty `blocking_reason` | Still `request-changes` (fail-safe) | Table row (b) asserts the override; the reason is not a gate condition | None needed |
| Fixture repo not added to the dev allowlist | Reviews refused at the allowlist gate; no review posts; Rung-2 ACs inconclusive | Task routes to `human_review`; no new review on the fixture PR | Add `github.com/bborbe/pr-review-fixtures` to both `REPO_ALLOWLIST` entries in `values-dev.yaml`, mirror + apply dev, re-trigger |
| Deployed dev image ≠ released tag (stale deploy) | spec-verifier Phase 0.5 refuses before AC evidence | `deploy_check` ≠ `deploy_target` | Deploy per `docs/releasing-github-pr-review-agent.md`; re-run `deploy_check` |
| ai_review re-base incomplete | The legitimate approve-with-debt verdict is flagged inconsistent; review routes to `human_review` | Fixture review posts `APPROVED` but the task lands in `human_review` / ai_review verdict is fail | Verify the `review_workflow.md` re-base greps (AC 3/4) landed; re-run |
| `gh` or the nuke dev cluster unreachable during the ladder | Verification is inconclusive, not failed | The probe errors or returns empty rather than a value | Re-run when reachable; never mark the spec completed on an unreachable probe (spec 003 lesson) |
| GitHub API rate limiting during ladder queries | `gh` returns 403/rate-limited; Rung-2 evidence uncollectable | The API error mentions rate limit | Re-run after the reset window; the poster's own posting retries are unchanged |
| Two runs race on the same PR at the same head | Same-SHA reviews stack (never dismissed); the newest is the one posted last and `last` in the query | The reviews list shows two same-head entries | None needed — the `last`-entry query and vault-first idempotency cover it |
| Malformed `comments` list or an unparseable comment | Per-entry skip; no over-trigger; a genuinely malformed block hits `ParseVerdict`'s malformed-JSON fail-close | Table rows (a)-(d) and the existing fail-closed rows stay green | None needed |

## Security / Abuse Cases

- The blocking decision is entirely model-controlled: a crafted diff can steer the model into `blocking: false` on a genuine defect, yielding an approve the Go gate cannot catch. This is not a regression — today's severity map is equally model-controlled, and the two model-independent defenses, the mechanical funnel gate (`ReasonFunnelDidNotRun`) and the concerns-not-verified gate, are untouched. The severity fallback closes the lazy bypass: a model that omits the field still reproduces old behavior (critical/major block). A deliberate mislabel remains an auditable assertion visible in the review body.
- No new trust boundary: the change adds no HTTP, files, or user-input handling; the gate only reads the verdict block the model already produced, and the four fail-closed paths keep unparseable output → `request-changes`.
- The per-entry skip means a malformed comment can neither force nor suppress a block; nothing in the new gate hangs, retries, or races (it is pure in-process parsing on already-produced text).

## Suggested Decomposition

Prompts are generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Verdict schema + execution prompt: `blocking`/`blocking_reason` in `execution_output-format.md`; blocking roll-up replacing the deterministic severity map in `execution.go` (six conditions, three non-blocking categories) | 1, 2 | 1, 2 | — |
| 2 | Go blocking gate: comment parsing + `ReasonBlockingFindingPresent` in `verdict.go`, registered in `isFailClosedReason`; gate composed after funnel + concerns in the posting path; severity fallback; per-entry skip | 3, 4, 7 | 5, 6 | prompt 1 (parses the schema's fields; field names are frozen, so parallel-safe) |
| 3 | ai_review re-base: verdict-consistency check re-keyed from severity to blocking; no-hallucination check keyed on the file's presence in the diff's changed files; `docs/architecture.md` consistency line | 5, 6 | 3, 4 | — |
| 4 | Regression-lock + changelog: `pkg/verdict_test.go` table rows (a)-(d) + chain-precedence row; CHANGELOG `## Unreleased` entry; `make precommit` sweep | — | 5, 6, 7 | prompts 1-3 |
| — | Operator ladder (no prompt — runs on the host after merge): deploy + dev-allowlist change + fixture queries | — | 8, 9 | prompt 4 merged + deployed |

Rationale: the schema defines the contract (prompt 1); the Go gate enforces it independently of the model (prompt 2); the ai_review re-base is the necessary consequence so the new approve-with-debt verdicts are not re-flagged (prompt 3, parallel to prompt 2); the regression-lock locks all of it (prompt 4); the fixture ladder proves the deployed behavior end-to-end.

## Do-Nothing Option

Keep the deterministic severity map: any Must Fix or any Should Fix blocks. On a debt-heavy repo, a small PR draws dozens of findings on lines the PR did not touch and is blocked; operators resort to `--admin` overrides, each of which erodes the gate further. The 57-MUST incident named in the Problem recurs on any large-file PR. The current approach is not acceptable.
