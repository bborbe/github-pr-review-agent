---
status: completed
tags:
    - dark-factory
    - spec
approved: "2026-09-08T20:05:01Z"
generating: "2026-09-08T20:06:51Z"
prompted: "2026-09-08T20:25:33Z"
completed: "2026-09-09T12:14:32Z"
branch: dark-factory/diff-anchor-funnel-findings
---

## Summary

- The mechanical funnel today diff-scopes to the PR's changed FILES but scans their whole content. A 2-line directive-pin change on 2026-07-23 drew 57 MUST findings, none introduced by the PR, and the author was blocked twice in one session via `--admin` overrides — every override teaches the operator the gate is noise.
- This change filters funnel findings down to the lines the PR actually changed, before they reach the review model: pre-existing debt on untouched lines is removed from the injected findings, so it can never gate a merge and costs no model tokens.
- The model's reasoning context is unchanged: it still reads whole files and receives the full PR diff in the verifier preamble. Only which findings are surfaced and can gate is restricted.
- If the changed-line data cannot be obtained or parsed, the run fails closed (funnel reports it did not run) — a broken filter must never silently un-block a diff.
- Two real-runner-verified fixtures anchor the behavior: a 1-line change on a debt-heavy file drops 17 findings to 0; a diff that introduces a log-and-return defect keeps exactly the 2 introduced findings (git lines 54 and 55).

## Problem

The PR-review funnel (`pkg/funnel.go`) diff-scopes to changed files but scans their whole content, so the ast-grep findings are only per-file, not per-line. On 2026-07-23 a 2-line directive-pin change to `metrics.go` / `pr_commenter.go` in agent-task-controller drew 57 MUST findings, none introduced by the PR; the author was blocked twice in one session, each resolved by an `--admin` override. Every override teaches the operator the gate is noise. Post-hoc dismissal in the ai_review step detects debt-noise only after it already blocked the PR, and the blocking-verdict change (v0.7.0) still relies on the model correctly classifying every pre-existing finding as non-blocking — every off-diff finding still costs tokens and still risks a wrong block. The debt-noise must be removed at the source, before injection.

## Goal

The findings injected into the execution prompt — and therefore everything that can gate the merge verdict — are exactly the findings on lines the PR changed. Pre-existing debt on untouched lines never reaches the model, never blocks, and never costs review tokens. A diff that introduces a genuine defect still surfaces it. Any failure to compute the changed lines fails closed: the review proceeds as if the mechanical funnel had not run, never as though an unfiltered scan had succeeded. The findings JSON contract and the execution prompt are byte-identical; only the content of the injected findings is restricted.

## Non-goals

- Do NOT add severity or confidence suppression — that is the separate falsifiability-gate task.
- Do NOT change the blocking-vs-severity verdict semantics shipped in v0.7.0 — this spec only removes findings from the input, never changes how surviving findings roll up.
- Do NOT change `/coding:code-review`'s whole-codebase mode, which is supposed to scan everything.
- Do NOT add baseline-file suppression (`.code-review-baseline.yaml`) — a different, existing mechanism.
- Do NOT modify the shared `ast-grep-runner.sh` (a coding-plugin artifact consumed by other repos) — filtering happens in this consumer.
- Do NOT add any config flag, opt-out knob, or tunable threshold.

## Acceptance Criteria

- [ ] The funnel gains a hunk-parsing contract covered by a Ginkgo `DescribeTable`/`Entry` set in `pkg/funnel_test.go` — rows cover at least: single and multiple hunks per file; pure addition; deletion-only hunks (`+0,0`); empty hunk output; a malformed hunk header; hunk-boundary inclusion (first and last line of a range kept, one past the end dropped); rename/binary-only entries that produce no hunks without breaking parsing; multiple files — evidence: `go test ./pkg/...` exits 0 with all new rows green AND `grep -c 'Entry(' pkg/funnel_test.go` returns ≥ 1 — a `DescribeTable` whose `Entry` rows run no assertion fails this AC: every enumerated hunk row must call the parser and assert the parsed `[newStart, newStart+newCount)` range (or the malformed-header failure) for its input
- [ ] The filter contract rows are green: a finding survives only when its file's basename matches a changed file AND `line + 1` falls inside a changed range `[newStart, newStart+newCount)`; a finding with a matching basename but an out-of-range line drops; a finding whose basename is absent from the changed set drops; a finding with an empty `file` drops; a finding with `line == 0` drops — evidence: `go test ./pkg/...` exits 0 with the filter `DescribeTable` rows green
- [ ] Fixture 1 (minimal-diff-on-debt): the committed fixture JSON, captured from the real ast-grep runner against a debt-heavy file with a 1-line change, filters 17 findings to `stats.findings_count == 0` with empty `findings_by_owner` — evidence: `grep -cE '"findings_count"[[:space:]]*:[[:space:]]*17' pkg/testdata/funnel_fixture_minimal_diff_debt.json` returns ≥ 1 AND the `minimal-diff-on-debt` row is green under `go test ./pkg/...` (whitespace-tolerant: the runner emits pretty-printed JSON)
- [ ] Fixture 2 (diff-introduces-defect): the committed fixture JSON, captured from the real runner against a diff that adds `Ping()` with a log-and-return double-log, filters 19 findings to exactly 2 survivors — `go-logging/no-log-and-return-error` at `"line":53` and `go-composition/no-package-function-calls-in-business-logic` at `"line":54` (0-based ast-grep lines = git lines 54 and 55), proving the 0-based → 1-based line conversion — evidence: `grep -cE '"findings_count"[[:space:]]*:[[:space:]]*19' pkg/testdata/funnel_fixture_diff_introduces_defect.json` returns ≥ 1 AND the `diff-introduces-defect` row is green under `go test ./pkg/...` (whitespace-tolerant)
- [ ] Fail-closed on hunk failure: when `git diff --unified=0` fails or a hunk header is unparseable, the funnel result is `Ran: false` with a non-empty `FailDetail` and empty `FindingsJSON`; no code path returns `Ran: true` carrying unfiltered findings when hunk data is unavailable — evidence: the two fail-closed rows (git failure, malformed header) are green under `go test ./pkg/...`, asserting `Ran` false, `FindingsJSON` empty, `FailDetail` non-empty
- [ ] The findings JSON contract is unchanged: the filtered output re-parses as valid JSON with top-level keys `stats`, `findings_by_owner`, `errors`; `stats.yamls_run` and `stats.elapsed_ms` equal the input; `stats.findings_count` equals the number of surviving findings; surviving per-finding entries keep `rule_id`, `rule_level`, `file`, `line`, `column`, `matched_text`, `message` verbatim — evidence: the contract row is green under `go test ./pkg/...` AND `git diff pkg/prompts/execution.go pkg/prompts/execution_output-format.md` returns empty after the change
- [ ] `make precommit` exits 0 and the changelog names the change — evidence: `make precommit` exit 0 AND `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'diff-anchor'` returns ≥ 1
- [ ] **Post-Deploy (Rung-2):** after deploying to quant dev, a fresh review on fixture PR `bborbe/pr-review-fixtures#1` (`fix/minimal-diff-on-debt`, head `7f5a15851699da359047a47c2af329474c9d48c8`) posts `APPROVED` at the pinned head from the dev bot, and a fresh review on PR `#2` (`fix/real-defect`, head `03448af503d9cf626bf7d4bb4cae23b7ea4e5db1`) posts `CHANGES_REQUESTED` at the pinned head — evidence: `gh api repos/bborbe/pr-review-fixtures/pulls/1/reviews --jq '[.[] | select(.user.login=="ben-s-pull-request-reviewer-dev[bot]")] | last | {state, commit_id, submitted_at}'` returns `state == "APPROVED"`, `commit_id == "7f5a15851699da359047a47c2af329474c9d48c8"`, `submitted_at` after the dev deploy, AND the same query on `pulls/2` returns `state == "CHANGES_REQUESTED"`, `commit_id == "03448af503d9cf626bf7d4bb4cae23b7ea4e5db1"`, `submitted_at` after the dev deploy.
  - `deploy_check:` `kubectlnukedev -n dev get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git fetch --tags -q; git describe --tags --abbrev=0 origin/master)`
- [ ] **Post-Deploy (Rung-3):** after deploying to quant prod, a fresh review on fixture PR `bborbe/pr-review-fixtures#1` (`fix/minimal-diff-on-debt`, head `7f5a15851699da359047a47c2af329474c9d48c8`) posts `APPROVED` at the pinned head from the prod bot, posted after the prod deploy (the review body may surface zero pinned findings — that is the intended diff-anchored outcome, not a failure) — evidence: `gh api repos/bborbe/pr-review-fixtures/pulls/1/reviews --jq '[.[] | select(.user.login=="ben-s-pull-request-reviewer[bot]")] | last | {state, commit_id, submitted_at}'` returns `state == "APPROVED"`, `commit_id == "7f5a15851699da359047a47c2af329474c9d48c8"`, `submitted_at` after the prod deploy.
  - `deploy_check:` `kubectlnukeprod -n prod get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git fetch --tags -q; git describe --tags --abbrev=0 origin/master)`
- [ ] **Post-Deploy (Rung-3):** for 7 consecutive days of prod traffic after the deploy, no override fired on a minimal-diff PR — evidence: `kubectlnukeprod -n prod logs -l agent.benjamin-borbe.de/assignee=pr-reviewer-agent --since=<deploy-time> 2>&1 | grep -c 'override: posting APPROVE'` returns 0 (prod reviewer pods carry the label `agent.benjamin-borbe.de/assignee=pr-reviewer-agent`, verified live 2026-09-08; Job pods are one-shot, so log the window per-pod as each fires) AND the vault task [[Minimal Diffs Are Blocked by Pre-Existing Debt in Touched Files]] gains a Progress entry dated ≥ 7 days after the deploy stating the window passed with zero overrides on minimal-diff PRs.
  - `deploy_check:` `kubectlnukeprod -n prod get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git fetch --tags -q; git describe --tags --abbrev=0 origin/master)`
- [ ] Code-Review-Bench re-run: the [[Code-Review-Bench]] vault note records both the baseline from [[No Baseline Score Exists for the Own PR Reviewer]] and the diff-anchor re-run (agent version + date) — evidence: `grep -cE 'diff-anchor' '/Users/bborbe/Documents/Obsidian/Personal/50 Knowledge Base/Code-Review-Bench.md'` returns ≥ 1 AND the note's results-table diff-anchor row shows precision strictly higher than baseline and recall ≥ baseline − 0.05 (check the two numbers against the recorded baseline row; this AC is gated on the baseline task completing first — if the baseline is still absent when the diff-anchor run ships, record the diff-anchor row with a `baseline-pending` marker and re-check once [[No Baseline Score Exists for the Own PR Reviewer]] lands)
- [ ] The [[GitHub PR Reviewer Agent]] vault note's `## Backlog` no longer lists the diff-scoping item and its `## Review Scope` gotcha section describes the new diff-anchored behavior instead of whole-file scanning — evidence: `grep -c 'Diff-scope the review findings (baseline pre-existing)' '/Users/bborbe/Documents/Obsidian/Personal/50 Knowledge Base/GitHub PR Reviewer Agent.md'` returns 0 AND `grep -c 'Durable fix (not yet built)' <same file>` returns 0 AND `grep -c 'changed lines' <same file>` returns ≥ 1

## Verification

## Container-executable (runs inside the YOLO container at prompt time)

- `go test ./pkg/...` — hunk-parsing table, filter table, both fixture rows, the fail-closed rows, and the contract row green; all pre-existing rows stay green
- `make precommit` — fmt, generate, test, lint, vet, vuln, license clean
- `git diff pkg/prompts/execution.go pkg/prompts/execution_output-format.md` — empty (prompt contract untouched)
- `grep -cE '"findings_count"[[:space:]]*:[[:space:]]*17' pkg/testdata/funnel_fixture_minimal_diff_debt.json` ≥ 1 AND `grep -cE '"findings_count"[[:space:]]*:[[:space:]]*19' pkg/testdata/funnel_fixture_diff_introduces_defect.json` ≥ 1

## Operator-executable (runs on the host after PR merge, verification ladder)

- Merge to master; the maintainer bot (`github-releaser-agent`) cuts the tag (`.maintainer.yaml` is `release.autoRelease: true`, `.dark-factory.yaml` is `autoRelease: false` — do NOT hand-tag). Read the tag with `git fetch --tags && git describe --tags --abbrev=0`.
- Deploy per `docs/releasing-github-pr-review-agent.md`: bump `agent.tag` in `values-dev.yaml` and `values-prod.yaml` AND `AGENT_TAG_dev` / `AGENT_TAG_prod` in `~/Documents/workspaces/nuke/github-pr-reviewer/Makefile`, then mirror + apply dev, then prod (`BRANCH=master` for prod, never `BRANCH=prod`).
- **005-ordering prerequisite (required, not optional):** spec 005 (`specs/in-progress/005-blocking-verdict.md`) still has pending Rung-2 verification on the same fixture PRs, and 005's PR-#1 AC expects the review body to surface ≥ 1 pinned finding — diff-anchoring removes that expectation (the body may carry zero pinned findings). Complete 005's dev Rung-2 verification BEFORE deploying this change to dev, or re-baseline 005's PR-#1 AC first.
- **Allowlist prerequisite (required, not optional):** the dev `REPO_ALLOWLIST` is currently `github.com/bborbe/go-skeleton` only — the fixture repo is refused at the dev allowlist gate. Add `github.com/bborbe/pr-review-fixtures` to the dev entry in `values-dev.yaml`, mirror + apply dev, run the ladder, then revert. Prod's allowlist (`github.com/bborbe/*`) already covers the fixture repo.
- Fixture sources for the implementation (already built + verified with the real runner on 2026-09-08): the four captured fixture files are pre-staged in the worktree at `testdata-fixtures/` (`funnel_fixture_minimal_diff_debt.json`/`.diff` = 17 findings + 1-line diff; `funnel_fixture_diff_introduces_defect.json`/`.diff` = 19 findings + Ping diff) so the container sees them at `/workspace/testdata-fixtures/`. Prompt 2 copies them into `pkg/testdata/` so the unit rows replay from committed data.
- Trigger a fresh review on fixture PRs #1 and #2 after each deploy (watcher poll or review trigger), wait for the new review, then run the two Rung-2 and the Rung-3 queries. Run the two `deploy_check` commands and confirm each equals its `deploy_target` before collecting evidence.
- One-week window: record the daily observation in the vault task; at the end run the prod-log override grep.
- Benchmark: complete the baseline run from [[No Baseline Score Exists for the Own PR Reviewer]], then re-run with the released agent and record the delta in [[Code-Review-Bench]].
- Vault doc: remove the diff-scoping backlog item and rewrite the `## Review Scope` gotcha section per AC 12.

## Desired Behavior

1. **Post-run pre-injection filtering.** After the funnel runner returns its findings JSON (`Ran: true`), the funnel computes per-file changed-line ranges from `git diff --unified=0` against the same base-ref resolution the changed-files scan already uses (three-dot `origin/<base>...HEAD` with the existing bare-ref fallback), then rewrites the findings JSON before it is injected into the execution prompt. A finding survives only when its file's basename matches a changed file AND `line + 1` (ast-grep's 0-based line converted to git's 1-based) falls inside a changed range `[newStart, newStart+newCount)` from `@@ -a,b +c,d @@`. Findings with an empty `file` or `line == 0` drop. `stats.findings_count` is recomputed to the surviving count; every other field and the top-level shape are untouched.
2. **Fixture-driven outcomes.** The two real-runner-verified fixtures are the observable contract: a 1-line change on a debt-heavy file (17 findings) filters to `findings_count == 0`; a diff that adds `Ping()` with a log-and-return double-log (19 findings) filters to exactly 2 — `go-logging/no-log-and-return-error` at git line 54 and `go-composition/no-package-function-calls-in-business-logic` at git line 55 — so an introduced defect still gates while pre-existing debt does not.
3. **Fail-closed on hunk failure.** A failure to obtain or parse the hunk data — `git diff --unified=0` erroring, or a hunk header that does not match the `@@ -a,b +c,d @@` shape — produces the fail-closed funnel result (`Ran: false` with a `FailDetail` naming the failure), never `Ran: true` carrying unfiltered findings. A legitimately empty hunk result (mode-only, binary-only, or pure-rename change) is not a failure: findings for files with no changed ranges drop, which is the correct outcome for such a diff.
4. **Contract preservation.** The execution prompt's funnel-inject steer template, the verdict-translation footer, and the findings JSON schema are byte-identical; the model's whole-file read access and the full PR diff in the verifier preamble are unchanged. The verdict chain and every existing gate (funnel-did-not-run, concerns-not-verified, blocking) behave exactly as before on the surviving findings. Only the content of the injected findings is restricted.
5. **Implementation hygiene.** Hunk parsing and filtering are pure functions. Errors are wrapped via `github.com/bborbe/errors` with context — no `fmt.Errorf`, no bare `return err`. The changed-files base resolution is shared between the file scan and the hunk computation so the two can never disagree on the base.
6. **Test discipline.** Ginkgo v2 / Gomega coverage in `pkg/funnel_test.go` via `DescribeTable`/`Entry`: hunk-parsing edge cases, the two coordinate traps (0-based → 1-based lines, absolute-vs-relative paths matched on basename), both real-data fixtures, and the fail-closed paths. LLM-dependent steps use the fake runner — no live Claude calls in tests. The fixtures are committed as real-runner-captured artifacts so the scenarios replay from committed data.

## Constraints

- `FunnelResult` semantics are unchanged: `Ran` / `FindingsJSON` / `FailDetail`, and the no-changed-files short-circuit (`Ran: true` with an empty findings object) stay exactly as today.
- Findings JSON shape is frozen: top-level `stats` (`yamls_run`, `findings_count`, `elapsed_ms`), `findings_by_owner`, `errors`; per-finding fields `rule_id`, `rule_level`, `file`, `line`, `column`, `matched_text`, `message`.
- The execution prompt files (`funnelInjectSteerTemplate`, the verdict-translation footer, `execution_output-format.md`) are byte-identical; the verifier preamble's full-PR-diff injection is unchanged.
- Line conversion is a fixed invariant: ast-grep `range.start.line` is 0-based, git hunks are 1-based, the filter adds +1. A finding with `line == 0` (file-level, or a first-line finding) drops by decision — the model's judgment pass, which reads the whole file, still covers a genuine first-line defect.
- Path matching is a fixed invariant: the runner emits absolute paths, `git diff --name-only` emits repo-relative paths, the filter matches on basename.
- Changed lines are the new-file `+` lines only in `git diff --unified=0`; context lines never count; a hunk range is `[newStart, newStart+newCount)`.
- The hunk computation uses the same base-ref resolution order as the existing changed-files scan (`origin/<base>` three-dot first, bare-ref fallback), so the changed-file set and the hunk ranges always refer to the same base; any failure in either path fail-closes.
- The shared `ast-grep-runner.sh` script is unchanged; filtering happens in this consumer after the runner returns.
- DoD rules apply: errors via `github.com/bborbe/errors` (no `fmt.Errorf`), no debug output, `context.Context` threaded through IO, doc comments on exported symbols, `make precommit` clean, no `exclude`/`replace` in `go.mod`.
- No config flag, opt-out knob, or tunable threshold is added to this feature.
- The posted review event mapping, the allowlist, and all override gates are unchanged.
- CHANGELOG: `## Unreleased` is created below the preamble and above the newest `## vX.Y.Z` (currently `## v0.7.0`) — never between the title and the preamble.

## Failure Modes

| Trigger | Expected behavior | Detection | Recovery |
|---|---|---|---|
| `git diff --unified=0` fails (base ref unresolvable, git unavailable) | Fail-closed: `Ran: false` with `FailDetail`; the review surfaces the mechanical pass as unavailable and cannot silently approve | The execution steer shows the funnel COULD NOT RUN variant; a unit row asserts `Ran` false | Re-trigger the review; never retry with unfiltered findings |
| Malformed hunk header (unexpected git output shape) | Fail-closed, never unfiltered `Ran: true` — a broken parser must never silently un-block a diff | The malformed-header unit row fails before merge (`go test ./pkg/...`) | Fix the parser before shipping; the row is part of the regression lock |
| Base-resolution divergence between the file scan and the hunk computation | Ranges computed against a different base than the changed-file set → wrong survivors | Shared base-resolution constraint; if violated, the fail-closed path fires | Unit rows cover both resolution orders; a divergence cannot ship silently |
| Legitimate zero-hunk diff (mode-only, binary-only, pure rename) | All findings for that file drop; the PR can approve clean | None — correct outcome, covered by unit rows | None needed |
| Genuine first-line finding (ast-grep line 0) on a changed first line | The finding drops by decision | A real first-line defect may pass the funnel gate | The model's whole-file judgment pass still catches it; accepted limitation, documented in the vault gotcha update |
| Spec 005's Rung-2 on fixture PR #1 still pending when this ships | 005's PR-#1 AC expects ≥ 1 pinned finding in the body; diff-anchoring can make the body carry zero | 005's dev-bot query on `pulls/1` returns a body with no `main.go:N` match | Complete 005's dev Rung-2 before deploying to dev, or re-baseline 005's PR-#1 AC first (see Verification ordering prerequisite) |
| Fixture repo not on the dev allowlist | Dev-bot reviews refused at the allowlist gate; Rung-2 inconclusive | No new dev-bot review on the fixture PRs | Add `github.com/bborbe/pr-review-fixtures` to the dev `REPO_ALLOWLIST`, mirror + apply dev, re-trigger, revert |
| Deployed image ≠ released tag (stale deploy) | spec-verifier Phase 0.5 refuses before AC evidence | `deploy_check` ≠ `deploy_target` | Deploy per `docs/releasing-github-pr-review-agent.md`; re-run `deploy_check` |
| GitHub API rate limiting during ladder queries | `gh` returns 403/rate-limited; Rung-2/3 evidence uncollectable | The API error mentions rate limit | Re-run after the reset window; the poster's own posting retries are unchanged |
| Override fired on a minimal-diff PR during the one-week window | AC 10 fails — the window did not pass | Prod log grep returns ≥ 1 `override: posting APPROVE` line or the vault Progress records an override | Investigate whether the override covered a genuine changed-line defect (correct gate behavior) or a false positive on a changed line (re-file as a new spec); record the outcome in the vault task |
| Benchmark baseline not yet recorded (baseline task still backlog) | Diff-anchor delta cannot be computed; AC 11 inconclusive | [[Code-Review-Bench]] has no baseline row | Complete the baseline run from [[No Baseline Score Exists for the Own PR Reviewer]] first; AC 11 is gated on it |
| Re-trigger races on the same PR at the same head | Same-SHA reviews stack (never dismissed); the newest is the one posted last and `last` in the query | Two same-head review entries for the same bot | None needed — the `last`-entry query and vault-first idempotency cover it |

## Security / Abuse Cases

- The filter consumes ast-grep-emitted coordinates and git-derived hunks only — not PR-author-controlled text. A crafted PR cannot inject a finding at an arbitrary changed line: finding coordinates come from the local scan of the checked-out worktree, not from the PR. A finding can survive only by ast-grep genuinely matching on a line the diff changed, which is exactly the surface the gate blocks.
- The existing defense-in-depth on the runner output (valid-JSON check, code-fence neutralization) remains in force and is unchanged; the filter operates on already-validated JSON.
- No new trust boundary: no HTTP, no new files, no user input; the filter is a pure in-process transformation of already-produced JSON. Nothing hangs, retries, or races.
- The fail-closed direction is the safe one: a broken parser blocks the mechanical pass (`Ran: false`), never silently un-gates a diff.

## Suggested Decomposition

Prompts are generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Filter implementation in `pkg/funnel.go`: changed-line range computation from `git diff --unified=0` (shared base resolution), basename + line+1 filter, `stats.findings_count` recompute, fail-closed on hunk failure, `errors.Wrapf` hygiene | 1, 3, 5 | 5, 6 (partial) | — |
| 2 | Tests + committed fixtures: hunk-parsing `DescribeTable`, filter `DescribeTable` (both traps), fixture files from pre-staged `testdata-fixtures/` committed into `pkg/testdata/`, fixture rows (17→0, 19→2 at lines 53/54), fail-closed rows, contract row | 2, 6 | 1, 2, 3, 4, 5, 6 | prompt 1 (tests the filter) |
| 3 | Regression-lock + changelog: `make precommit` sweep, `## Unreleased` CHANGELOG entry naming diff-anchoring | — | 7 | prompts 1-2 |
| — | Operator ladder (no prompt — runs on the host after merge): 005-ordering + allowlist prerequisites, dev/prod deploy, fixture-PR verification queries, one-week observation, benchmark re-run, vault note update | — | 8, 9, 10, 11, 12 | prompt 3 merged + deployed |

Rationale: prompt 1 establishes the filter against the frozen JSON and prompt contracts; prompt 2 proves it, replaying the two real-runner fixtures the vault task already validated — this is where the line-base and path traps get locked; prompt 3 sweeps the build and documents the change; the operator ladder proves the deployed behavior end-to-end and closes the vault DoD items. No scenario is added: the behavior is fully reachable by unit/integration tests (the tests build a real git worktree), and the fixture PRs serve as the live post-deploy verification.

## Do-Nothing Option

Keep the whole-file funnel scan. On any debt-heavy repo, a small PR re-draws the full finding set: v0.7.0's blocking decoupling keeps the debt non-blocking only while the model correctly classifies each finding, every off-diff finding still costs tokens and still risks a wrong block, and the 2026-07-23 57-MUST incident recurs on the next large-file PR. The `--admin` override treadmill continues, and the Code-Review-Bench precision stays depressed because every emitted finding counts against the denominator. The current approach is not acceptable.
