---
status: completed
summary: 'Pinned the coding plugin marketplace to bborbe/coding@v0.51.0 in the Dockerfile runtime stage and added a fix: bullet under a new ## Unreleased CHANGELOG section'
execution_id: github-pr-review-agent-selector-pin-exec-016-pin-coding-marketplace-version
dark-factory-version: dev
created: "2026-09-08T17:20:00Z"
queued: "2026-09-08T15:33:40Z"
started: "2026-09-08T15:34:44Z"
completed: "2026-09-08T15:36:54Z"
---

# Pin the coding plugin marketplace to bborbe/coding@v0.51.0 in the Dockerfile

<summary>
- The Dockerfile's runtime stage pulls the coding plugin marketplace from the fixed ref `bborbe/coding@v0.51.0` instead of the floating `bborbe/coding`
- Image rebuilds become deterministic — a rebuild can no longer silently resolve the marketplace to a newer coding plugin version
- The 2026-07-23 missing-guide incident (selector-mode reviews failing with a Must-Fix CRITICAL because the resolved plugin lacked `docs/selector-mode-guide.md`) cannot recur on a rebuild
- A `fix:` CHANGELOG entry under a newly created `## Unreleased` section documents the pin and the incident it prevents
- The chained `RUN set -eux` structure of the runtime stage stays intact — only the one marketplace-add line changes
- No Go source, test, or build files are touched; `make precommit` passes unchanged
</summary>

<objective>
Pin the coding plugin marketplace in the Dockerfile's alpine runtime stage to `bborbe/coding@v0.51.0` so image rebuilds resolve a fixed plugin version and can no longer silently pull one missing `docs/selector-mode-guide.md` (the cause of the 2026-07-23 Must-Fix CRITICAL on selector-mode reviews, previously only fixed transiently by a rebuild), and record the change under `## Unreleased`.
</objective>

<context>
Read `CLAUDE.md` for project conventions (dark-factory prompt workflow, `make precommit` as the gate, release via the sibling github-releaser-agent).

Read these files:
- `Dockerfile` — the final `FROM alpine` stage contains the `RUN set -eux \` chain (~lines 24-28) with the three chained plugin lines `claude plugin marketplace add bborbe/coding \`, `claude plugin install coding \`, and `claude plugin list | grep -q coding`. The first of those three is the line this prompt edits.
- `CHANGELOG.md` — head only: the newest section is `## v0.6.11`; there is currently NO `## Unreleased` section.
- `docs/dod.md` — § Documentation: the `## Unreleased` placement rule (below the preamble, above the newest `## vX.Y.Z`; final order `# Changelog` → preamble → `## Unreleased` → `## vX.Y.Z`).

Read the coding plugin guide (in-container path):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — conventional prefixes (`fix:` → patch bump) and the frozen-preamble rule.

Note: no Dockerfile in this repo or its sibling agent repos (github-releaser-agent, security-review-agent, github-pr-review-agent, github-pr-review-agent-blocking, github-dark-factory-agent*) pins the plugin marketplace version — this is the first pin, so there is no in-tree exemplar to copy from. Implement the exact literal in requirement 1; do not invent a different pin syntax.
</context>

<requirements>
1. **Pin the marketplace in `Dockerfile`.** In the final `FROM alpine` stage, inside the `RUN set -eux \` chain, change exactly one line. Preserve the leading `&&`, the `timeout 300`, and the trailing ` \` exactly:

   ```
   OLD:  && timeout 300 claude plugin marketplace add bborbe/coding \
   NEW:  && timeout 300 claude plugin marketplace add bborbe/coding@v0.51.0 \
   ```

   Do not modify any other Dockerfile line — in particular the following `&& timeout 300 claude plugin install coding \` and `&& claude plugin list | grep -q coding` lines stay untouched.

2. **Add the `## Unreleased` entry in `CHANGELOG.md`.** There is currently no `## Unreleased` section. Create it directly above `## v0.6.11` (below the preamble, never between `# Changelog` and the preamble), final order `# Changelog` → preamble → `## Unreleased` → `## v0.6.11`. Add exactly one bullet, verbatim:

   ```markdown
   - fix: pin the coding plugin marketplace to `bborbe/coding@v0.51.0` in the Dockerfile runtime stage — the unpinned `claude plugin marketplace add bborbe/coding` silently resolved to a coding plugin version missing `docs/selector-mode-guide.md`, which made selector-mode reviews fail with a Must-Fix CRITICAL (observed 2026-07-23, fixed transiently by rebuild); the pin makes image rebuilds deterministic
   ```

   The bullet must start with `fix:` (patch bump per the changelog guide). Do not touch the preamble or any released `## vX.Y.Z` section.

3. **Self-check before finishing.** Re-run `<verification>` and confirm it passes. Walk each acceptance criterion against the change: the pinned literal is the only `claude plugin marketplace add bborbe/coding` line in the Dockerfile (no unpinned line remains), and the changelog bullet sits under `## Unreleased` directly above `## v0.6.11` with the preamble intact.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Change exactly two files: `Dockerfile` and `CHANGELOG.md`. No other file.
- Do NOT run `docker build`, `make build`, or `make buca` — the container has no Docker socket and the image rebuild is operator-side, outside this prompt's scope. This change is a pure text edit; correctness is established by the pinned literal, the changelog bullet, and `make precommit`.
- Keep the chained `RUN set -eux \` structure intact: the edited line keeps its leading `&&`, `timeout 300`, and trailing ` \`.
- Existing tests must still pass — no Go source is touched, so the suite is unaffected; `make precommit` is the gate.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- Pin present: `grep -c 'claude plugin marketplace add bborbe/coding@v0.51.0' Dockerfile` returns 1.
- No unpinned line remains: `grep -c 'claude plugin marketplace add bborbe/coding' Dockerfile` returns 1 — the pinned line is the only line containing `claude plugin marketplace add bborbe/coding` (a leftover unpinned line would raise the count to 2).
- Changelog bullet: `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -c 'bborbe/coding@v0.51.0'` returns 1.
- Section placement: `awk '/^## /{print NR": "$0}' CHANGELOG.md | head -3` — the first `##` heading must be `## Unreleased`, immediately followed by `## v0.6.11` (preamble untouched, no `##` inserted above it).
</verification>
