---
status: completed
spec: [006-diff-anchor-funnel-findings]
summary: 'Added the ## Unreleased changelog entry naming the diff-anchoring change (containing the literal diff-anchor) below the preamble and above ## v0.7.0, and ran the final make ROOTDIR=/workspace precommit sweep which passed exit 0 with all acceptance greps and the full container-executable test suite green.'
execution_id: github-pr-review-agent-diff-anchor-exec-022-spec-006-regression-lock-changelog
dark-factory-version: dev
created: "2026-09-08T20:14:50Z"
queued: "2026-09-08T20:34:36Z"
started: "2026-09-08T20:53:07Z"
completed: "2026-09-08T20:54:45Z"
branch: dark-factory/diff-anchor-funnel-findings
---

# Diff-anchor the mechanical funnel findings — regression lock and changelog

<summary>
- The changelog gains an `## Unreleased` entry naming the diff-anchoring change, placed below the preamble and above the newest release section, containing the literal `diff-anchor` (AC 7 evidence)
- A final full `make precommit` sweep runs over the whole batch (filter implementation + tests + fixtures), confirming the tree is release-ready with exit code 0
- The full container-executable acceptance evidence is re-verified in one pass: the hunk/filter/fixture/fail-closed/contract rows green, the fixture count greps, and the untouched prompt-contract files
- The `## Unreleased` placement rule is enforced: section sits between the preamble and `## v0.7.0`, never between the `# Changelog` title and the preamble
- A failed sweep (including a pre-existing unrelated failure) is reported `status: failed` with the failing target named — never success
</summary>

<objective>
Close out the batch: record the diff-anchoring change in the changelog and run the final precommit sweep that proves the accumulated implementation, tests, and fixtures are clean together.
</objective>

<context>
Read `CLAUDE.md` for project conventions (changelog placement, precommit workflow).

Read these files:
- `CHANGELOG.md` — currently ends its preamble at `...and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).` and goes straight to `## v0.7.0`. There is NO `## Unreleased` section yet. The newest released section is `## v0.7.0`.
- `docs/dod.md` — the changelog placement rule: create `## Unreleased` below the preamble block and above the newest `## vX.Y.Z` section, never between the `# Changelog` title and the preamble.

Read these coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — entry format and prefix rules
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — completion criteria for the batch
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — what the precommit sweep runs (fmt, generate, test, lint, vet, vuln, license)

Verified contracts (do not re-derive):
- Prompts 1 and 2 of this batch land before this prompt executes (this prompt depends on them): the filter in `pkg/funnel.go`, the tests in `pkg/funnel_test.go` + `pkg/export_test.go`, and the fixtures created in `pkg/testdata/funnel_fixture_*.{json,diff}`. This prompt adds no code. If any of those is absent when this prompt runs, report `status: failed` — do not regenerate the fixtures or re-implement the filter.
- `pkg/prompts/execution.go` and `pkg/prompts/execution_output-format.md` are untouched by this batch — the findings JSON contract and the execution prompt are byte-identical.
- This repo is `release.autoRelease: true` — dark-factory/`github-releaser-agent` cuts tags post-merge. Do NOT hand-rename `## Unreleased` or tag.
</context>

<requirements>
1. **Add the `## Unreleased` changelog entry to `CHANGELOG.md`.** Create the section below the preamble (the line ending `...and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).`) and above `## v0.7.0`, exactly:

   ```markdown
   ## Unreleased

   - feat: diff-anchor the mechanical funnel findings — the ast-grep findings injected into the execution prompt are now filtered to the exact lines a PR changed (basename match plus 0-based-to-1-based line-in-range against `git diff --unified=0` hunks), so pre-existing debt on untouched lines never reaches the model, never blocks, and costs no review tokens; a diff that introduces a genuine defect still surfaces it; any failure to compute the changed lines fails closed (`Ran: false`), so a broken filter can never silently un-block a diff
   ```

   Final section order must be: `# Changelog` → preamble → `## Unreleased` → `## v0.7.0` (newest first). Do not rename, reorder, or edit any existing `## vX.Y.Z` section. The `feat:` prefix (new feature → minor bump) is required per changelog-guide.md.

2. **Run the final precommit sweep** — `make precommit` at the repo root MUST exit 0 (fmt, generate, test, lint, vet, vuln, license). Use `make ROOTDIR=/workspace precommit` as the primary form — the container's `.git` is masked, so the bare `make precommit`'s `ROOTDIR ?= $(shell git rev-parse --show-toplevel)` fails deterministically; fall back to bare `make precommit` only if `ROOTDIR` resolves. If a formatting/lint/check target fails (e.g. `make fmt`, `make lint`, `make gosec`, `make errcheck`), fix ONLY that failing target and re-run it; re-run the full sweep once all individual targets pass. If the failing target is a behavior test (`make test`) or any code/tests/fixtures shipped by prompts 1-2, do NOT fix it here — report `status: failed` naming the target (a follow-up fix prompt owns that). If the sweep fails on something pre-existing and unrelated to this batch, do NOT report success — report `status: failed` with the failing target.

3. **Re-verify the full container-executable acceptance evidence in one pass** (the spec's Verification section) and confirm every check passes:
   - `go test -mod=mod ./pkg/... -count=1` exits 0 with all the spec-006 rows green (hunk-parsing tables, filter table, both fixture rows, the fail-closed rows, the contract row) and all pre-existing rows green;
   - `grep -cE '"findings_count"[[:space:]]*:[[:space:]]*17' pkg/testdata/funnel_fixture_minimal_diff_debt.json` returns ≥ 1;
   - `grep -cE '"findings_count"[[:space:]]*:[[:space:]]*19' pkg/testdata/funnel_fixture_diff_introduces_defect.json` returns ≥ 1;
   - `grep -c 'Entry(' pkg/funnel_test.go` returns ≥ 1;
   - `pkg/prompts/execution.go` and `pkg/prompts/execution_output-format.md` are unchanged (this batch never touched them — the host-side `git diff` evidence is an operator/audit step, since the container's `.git` is masked).

4. **Self-check before finishing:** walk AC 7 (the changelog grep and the precommit exit code) and the `## Unreleased` placement rule: the section must sit between the preamble and `## v0.7.0`, never between the title and the preamble.
</requirements>

<constraints>
- This prompt is confined to `CHANGELOG.md` plus the verification sweep. Do NOT modify any Go source, test, fixture, or prompt-contract file — prompts 1-2 of this batch already shipped them and they are in the tree.
- Do NOT rename `## Unreleased` to a version, do NOT hand-tag, and do NOT edit any existing `## vX.Y.Z` section — the maintainer bot cuts tags post-merge (`.maintainer.yaml` is `release.autoRelease: true`).
- The changelog entry must name the observable behavior (diff-anchoring of funnel findings), not file paths or struct names, and must contain the literal `diff-anchor` (AC 7 evidence).
- No config flag, opt-out knob, or tunable threshold is added. The posted review event mapping, allowlist, and override gates are unchanged.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make ROOTDIR=/workspace precommit` — must exit 0 (the container's `.git` is masked, so this form avoids the `ROOTDIR ?= $(shell git rev-parse --show-toplevel)` failure; bare `make precommit` only if `ROOTDIR` resolves). A non-zero exit code means `status: failed` in the completion report, even if the failure looks unrelated.
- AC 7 evidence: `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'diff-anchor'` returns ≥ 1.
- `sed -n '1,12p' CHANGELOG.md` — confirm the section order `# Changelog` → preamble → `## Unreleased` → `## v0.7.0`.
- AC 1-6 re-evidence (from the `go test` + greps in requirement 3): all green.
</verification>

<!-- AUDITOR NOTES
1. AC 7's `make precommit` exit-0 evidence is the load-bearing gate; the `sed` grep proves the changelog names the change. The `## Unreleased` placement rule (below preamble, above `## v0.7.0`) is enforced in requirement 1 and verified in the Verification section.
2. The spec's container-executable `git diff pkg/prompts/execution.go pkg/prompts/execution_output-format.md` evidence cannot run inside the container (the `.git` is masked by hideGit and the daemon does not gate on verification exit codes) — the prompt-contract byte-identity is instead guaranteed by construction (no prompt in this batch touches `pkg/prompts/*`) and by the contract row in prompt 2; the host-side `git diff` remains an operator/audit step.
3. This is the final prompt of a three-prompt batch (implementation → tests+fixtures → changelog+sweep), matching the spec's Suggested Decomposition; the operator ladder (ACs 8-12) is explicitly NOT a prompt per the spec.
-->
