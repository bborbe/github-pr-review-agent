---
status: completed
spec: [007-chunked-review-for-oversized-prs]
summary: 'Locked the chunked review loop with five end-to-end fake-runner integration rows (runner call count = n, clone+funnel once, per-chunk prompt scoping, below-threshold unscoped identity, merged body with one verdict block posted once) and added the chunked-review bullet to CHANGELOG.md''s existing ## Unreleased section; make ROOTDIR=/workspace precommit exits 0.'
execution_id: github-pr-review-agent-exec-026-spec-007-regression-lock-changelog
dark-factory-version: dev
created: "2026-09-11T22:35:00Z"
queued: "2026-09-11T20:03:46Z"
started: "2026-09-11T20:27:38Z"
completed: "2026-09-11T20:34:14Z"
branch: dark-factory/chunked-review-for-oversized-prs
---

# Chunked review for oversized PRs — regression lock, precommit sweep, and changelog

<summary>
- The chunked review loop is locked end-to-end with the fake runner: the runner is invoked once per chunk with no extra synthesis invocation.
- The clone and the mechanical funnel are asserted to run exactly once per review, never once per chunk.
- Each chunk's prompt is asserted to name its own files and no file from another chunk, and to carry only that chunk's funnel findings.
- A below-threshold PR is asserted to run exactly once with the same unscoped prompt a single run gets today.
- The merged review is asserted to carry one section per chunk and exactly one verdict block, and to post through the unchanged posting path.
- The whole batch passes `make precommit` (format, generate, test, lint, vet, vuln, osv-scanner, trivy, license).
- The changelog gains an `## Unreleased` entry naming the chunked-review change, placed below the preamble and above the newest release section.
- A failed sweep — including a pre-existing unrelated failure — is reported `status: failed` with the failing target named, never success.
</summary>

<objective>
Close out the batch: lock the chunked review loop with end-to-end fake-runner rows that prove the call counts, prompt scoping, below-threshold identity, and merged body; record the change in the changelog; and run the final `make precommit` sweep over the whole batch.
</objective>

<context>
Read `CLAUDE.md` for project conventions (changelog placement, precommit workflow).

Read these files:
- `CHANGELOG.md` — the preamble ends at the line `...and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).` and goes straight to `## v0.10.1`. There is NO `## Unreleased` section yet. The newest released section is `## v0.10.1`.
- `docs/dod.md` — the changelog placement rule: create `## Unreleased` below the preamble block and above the newest `## vX.Y.Z` section, never between the `# Changelog` title and the preamble.
- `pkg/steps_checkout_execution_test.go` — the existing suite and the fake-runner fixtures added in prompt 2 (temp plugin dir, `repoManager.EnsureWorktreeReturns`, `mocks.ClaudeRunnerMock` `RunStub`, a fake `pkg.FunnelRunner`).
- `pkg/steps_checkout_execution.go` — the chunk loop from prompt 2 and `postAndRoute`.
- `pkg/chunk.go` — `ReviewChunk`, `PartitionReviewChunks`, `MergeChunkReviews`.

Read these coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — entry format and prefix rules
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — completion criteria for the batch
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — what the precommit sweep runs

Verified contracts (do not re-derive):
- Prompts 1 and 2 of this batch land before this prompt executes (this prompt depends on them): `pkg/chunk.go`, the funnel inventory fields in `pkg/funnel.go`, the chunk config plumbing, the chunk loop in `pkg/steps_checkout_execution.go`, and `prompts.BuildChunkExecutionInstructions`. This prompt adds tests + changelog only, plus a full sweep. If any of those is absent when this prompt runs, report `status: failed` — do not re-implement them.
- This repo is `release.autoRelease: true` — the maintainer bot cuts tags post-merge. Do NOT hand-rename `## Unreleased` or tag.
- The repo is mounted at `/workspace`; `.git` is NOT masked (`.dark-factory.yaml` sets `workflow: direct` and `hideGit` defaults to `false`). Use `make ROOTDIR=/workspace precommit` as the primary sweep form anyway — `ROOTDIR` is defaulted in `Makefile.variables` via `$(shell git rev-parse --show-toplevel)`, so pinning it to the mount is harmless when git resolves and safe if it does not.
</context>

<requirements>
1. **Add end-to-end fake-runner rows to `pkg/steps_checkout_execution_test.go`** (extend the suite from prompt 2; reuse its fixtures). These lock the loop behavior through the real `checkoutExecutionStep.Run`:
   - **Runner call count.** With a `FunnelResult` whose `ChangedFiles` partition into `n` chunks (e.g. 30 files × 20 added lines with the default config → 2 chunks), the fake `ClaudeRunnerMock.RunCallCount()` equals `n` — no extra synthesis invocation.
   - **Clone and funnel run once.** The fake `RepoManager.EnsureWorktreeCallCount()` equals 1 and the fake `FunnelRunner.RunCallCount()` equals 1 across a multi-chunk review.
   - **Prompt scoping.** Capture each prompt via the `RunStub`'s `prompt` argument; assert chunk `i`'s prompt contains every path of chunk `i` and NO path that belongs only to another chunk; assert each prompt carries the chunk-scoped findings (the filtered `findings_count`) rather than the whole-review findings.
   - **Below-threshold identity.** With a `FunnelResult` whose total added lines are at or below `REVIEW_CHUNK_ENGAGE_ADDITIONS` (500), the runner is called exactly once, the single prompt carries NO chunk-scope preamble (it is the same unscoped prompt a single run gets today), and exactly one `review chunk 1/1` line is emitted (assert via the runner call count and the prompt content; the log line itself is asserted in prompt 2's rows).
   - **Merged body.** With two chunks both emitting a valid `approve` verdict block, the posted summary (`PrPoster.PostCallCount() == 1` and the captured `PostRequest.Summary`) carries exactly one verdict block and `pkg.ParseVerdict` on the `## Review` body returns `approve`; with one chunk emitting `request-changes`, the merged body parses to `request-changes` and the posted verdict is `request-changes`; the `## Review` body contains exactly `n` lines matching `^### Chunk `.

   These rows assert real integration behavior of the loop; do NOT duplicate the pure-function rows already in `pkg/chunk_test.go` (partition composition, merge unit rows, `ChunkDeadline` math, `FilterFindingsByBasenames`) — reference them instead if a shared helper is useful.

2. **Add the `## Unreleased` changelog entry to `CHANGELOG.md`.** Create the section below the preamble (the line ending `...and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).`) and above `## v0.10.1`, exactly:

   ```markdown
   ## Unreleased

   - feat: review oversized PRs in bounded chunks — a PR whose reviewable added lines exceed `REVIEW_CHUNK_ENGAGE_ADDITIONS` (default 500) is partitioned path-sorted into chunks of at most `REVIEW_CHUNK_MAX_ADDITIONS` (300) added lines and `REVIEW_CHUNK_MAX_FILES` (15) files, a file is never split and `X.go` always shares a chunk with its `X_test.go` sibling, each chunk runs one review scoped to its own files with the funnel findings for those files only, and the chunk outputs merge worst-wins into exactly one posted review; below the threshold the review path is unchanged, and each chunk run logs `review chunk <i>/<n> files=<F> additions=<A>`
   ```

   Final section order must be: `# Changelog` → preamble → `## Unreleased` → `## v0.10.1`. Do not rename, reorder, or edit any existing `## vX.Y.Z` section. The `feat:` prefix (new feature → minor bump) is required per `changelog-guide.md`. The entry must contain the literal `chunk` (the AC evidence greps for it).

   Also add three rows to `README.md`'s configuration table (the table documenting `REVIEW_MODE` / `REVIEW_MAX_DURATION`): `REVIEW_CHUNK_ENGAGE_ADDITIONS` (added-line threshold above which a review is partitioned into chunks, default `500`), `REVIEW_CHUNK_MAX_ADDITIONS` (maximum added lines per chunk, default `300`), and `REVIEW_CHUNK_MAX_FILES` (maximum changed files per chunk, default `15`) — `docs/dod.md` requires README to be updated when configuration changes.

3. **Run the final precommit sweep** — `make ROOTDIR=/workspace precommit` at the repo root MUST exit 0 (format, generate, test, lint, vet, vuln, osv-scanner, trivy, license). If a formatting/lint/check target fails (e.g. `make format`, `make lint`, `make vulncheck`, `make osv-scanner`, `make trivy`), fix ONLY that failing target and re-run it; re-run the full sweep once all individual targets pass. If `make test` fails because of code or tests shipped by prompts 1-2, do NOT fix that code here — report `status: failed` naming the failing row (a follow-up fix prompt owns that); fix your own new rows from requirement 1 if their expectations are wrong. If the sweep fails on something pre-existing and unrelated to this batch, do NOT report success — report `status: failed` with the failing target.

4. **Re-verify the container-executable acceptance evidence in one pass** and confirm every check passes:
   - `make test` exits 0 with all the spec-007 rows green (the partition table, the merge table, the config-validation rows, the inventory rows, the `ChunkDeadline` and `FilterFindingsByBasenames` tables, the fake-runner loop rows) and all pre-existing rows green;
   - `grep -n 'review chunk' pkg/*.go` returns ≥ 1 (the chunk-line emission site is present);
   - `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'chunk'` returns ≥ 1.

5. **Self-check before finishing.** Walk spec AC 3 (the call-count and identity rows) and AC 6 (the precommit exit code and the changelog grep) against the change: the runner is invoked exactly `n` times, the clone and funnel run once, the below-threshold review runs once with the unscoped prompt, and the changelog section sits between the preamble and `## v0.10.1`.
</requirements>

<constraints>
- This prompt is confined to `pkg/steps_checkout_execution_test.go`, `CHANGELOG.md`, `README.md`, and the verification sweep. Do NOT modify any production Go source shipped by prompts 1-2 — they are in the tree. If a production defect is found, report `status: failed` naming it; do not silently fix it here.
- The changelog entry must name the observable behavior (bounded chunked reviews), not file paths or struct names, and must contain the literal `chunk`.
- Do NOT rename `## Unreleased` to a version, do NOT hand-tag, and do NOT edit any existing `## vX.Y.Z` section.
- No config flag, opt-out knob, or tunable threshold is added here. The posted review event mapping, allowlist, and override gates are unchanged.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make ROOTDIR=/workspace precommit` — must exit 0 (the pinned `ROOTDIR` avoids depending on `git rev-parse`; bare `make precommit` only if `ROOTDIR` resolves). A non-zero exit code means `status: failed` in the completion report, even if the failure looks unrelated.
- AC 6 evidence: `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'chunk'` returns ≥ 1.
- AC 3 evidence: `grep -n 'review chunk' pkg/*.go` returns ≥ 1.
- `sed -n '1,12p' CHANGELOG.md` — confirm the section order `# Changelog` → preamble → `## Unreleased` → `## v0.10.1`.
- `make test` — all spec-007 rows and all pre-existing rows green.
</verification>

<!-- AUDITOR NOTES
1. This is prompt 3 of a three-prompt batch, matching the spec's Suggested Decomposition. It depends on prompts 1-2 and covers spec ACs 3 (call-count and identity rows) and 6 (precommit + changelog).
2. These rows are end-to-end through `checkoutExecutionStep.Run` (call counts, prompt scoping, below-threshold identity, merged body posted) and deliberately do NOT duplicate the pure-function rows in `pkg/chunk_test.go` or the routing rows in prompt 2 — the split matches the spec's decomposition (prompt 2 = loop implementation + routing rows; prompt 3 = integration call-count/identity rows + sweep).
3. The spec's Post-Deploy Rung-2/Rung-3 acceptance criteria (dev/prod chunk-line observation) are operator-only and explicitly NOT a prompt per the spec's Suggested Decomposition — they live on the spec's Verification ladder.
4. No scenario prompt is emitted: the spec states "No scenario is added" — the behavior is reachable by the unit and fake-runner integration rows, and the deployed observation is the live post-deploy check.
-->
