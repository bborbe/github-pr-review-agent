---
status: completed
spec: [011-pr-visible-failure-marker]
summary: Documented the PR-visible failure marker across the DoD, posting contract, architecture and no-review code comment, removed the stale no-post statements, and added an Unreleased changelog entry.
execution_id: 2567b9bc-4c6b-5543-b964-1c92c2fa426f-exec-039-spec-011-failure-marker-docs
dark-factory-version: dev
created: "2026-11-18T15:00:00Z"
queued: "2026-11-18T14:53:48Z"
started: "2026-11-18T15:02:25Z"
completed: "2026-11-18T15:05:14Z"
branch: dark-factory/pr-visible-failure-marker
---

# Document the failure marker and reconcile the now-false posting statements

<summary>
- The project's Definition of Done no longer forbids every new posting path; it names the failure marker as a bounded exception.
- The Definition of Done also names the two pre-existing comment-only paths it already omitted.
- The posting-contract document describes the marker: when it is posted, what it contains, and that it is best-effort.
- The architecture document points at the marker documentation.
- The posting-contract document no longer claims that a no-review run posts nothing to the PR.
- The stale code comment that says the no-review path posts nothing is corrected.
- The changelog records the new PR-visible marker under an Unreleased heading.
</summary>

<objective>
Make the repository's documentation describe the PR-visible failure marker accurately, and remove the three statements that now say the opposite of the shipped behaviour. The code is already implemented by prompts 1 and 2; this prompt reconciles the contract documents and the changelog.
</objective>

<context>
Read `CLAUDE.md` for project conventions.

Read these files before changing anything:
- `docs/dod.md` — § "Review-Posting Safety". The line `- No new code path posts to a PR other than the single structured review (+ the override approve)` is now false and must be replaced, not merely followed by an exception.
- `docs/pr-post-back.md` — § "Always-Post Review Invariant" contains the false line `Nothing is posted to the PR — deliberately, since the repo's own Definition of Done forbids a new posting path ...`; § "The Posting Flow" and § "Failure Routing" describe the posting contract.
- `docs/architecture.md` — § "Posting Reviews Back to GitHub" points at `docs/pr-post-back.md`.
- `pkg/steps_checkout_execution.go` — the `noReviewResult` doc comment contains the false paragraph beginning `Nothing is posted rather than a COMMENT — this repo's Definition of Done forbids ...`.
- `CHANGELOG.md` — the top section after the preamble is currently `## v0.18.2`.
- `docs/github-app-setup.md` — the App's existing `pull_requests:write` permission; the marker reuses it and does not need `issues:write`.

Reference this coding guide (it lives in the YOLO container, not on the host):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md`
</context>

<requirements>
1. **Amend `docs/dod.md` § "Review-Posting Safety".** Replace the prohibition line `- No new code path posts to a PR other than the single structured review (+ the override approve)` with a statement that:
   - permits a `COMMENT` review carrying no verdict for exactly the existing comment-only paths and the failure marker;
   - names both pre-existing `COMMENT` paths the old line omitted: `postHallucinationComment` (the comment-after-dismiss path) and the deprecated `PostLGTM`;
   - names the failure marker explicitly (use the exact lowercase phrase `failure marker` at least once);
   - states the marker is best-effort, never changes routing, phase or status, never carries a verdict, and is **not** a demotion of the verdict path — it does not touch `mapVerdictAndSummary`;
   - keeps the verdict path's `APPROVE` / `REQUEST_CHANGES` contract intact.

   The exact phrase `No new code path posts` must not appear anywhere in `docs/dod.md` after the change.

2. **Add a failure-marker section to `docs/pr-post-back.md`.** Use the exact lowercase phrase `failure marker`. Describe:
   - the three triggers, all execution-phase terminal routings: `run_outcome: post_failed`, `run_outcome: no_review`, and `run_outcome: budget_expired` (both the chunked and single-run budget paths);
   - the delivery: `PrPoster.PostFailureMarker` calls the existing `postReview(ctx, pr, headSHA, "COMMENT", body)`, i.e. `POST /repos/{owner}/{repo}/pulls/{n}/reviews` with `event: COMMENT`; it uses the existing `pull_requests:write` permission and is not an issue comment — the Issues API is not touched;
   - the body: composed only from `run_outcome`, a bounded `failure_reason`, and the run's task identifier; hard-capped at 2000 characters; it never embeds a response body, a raw error, or `## Diagnostics` content;
   - the semantics: best-effort, the `PostResult` is discarded, no `## Diagnostics` entry is added, routing/phase/status and `run_outcome` are unchanged; a nil poster skips it entirely;
   - the merge-gate effect: a `COMMENTED` review does not satisfy branch protection, so the PR stays `REVIEW_REQUIRED` — the marker makes the block visible without clearing it;
   - the out-of-scope note: planning-phase and review-phase budget expiries are not covered.

3. **Remove the false no-post sentence from `docs/pr-post-back.md`.** In § "Always-Post Review Invariant", the sentence beginning `Nothing is posted to the PR — deliberately, since the repo's own Definition of Done forbids a new posting path ...` must be replaced with an accurate statement: the no-review skip posts no verdict, but it does post a best-effort failure-marker `COMMENT` naming the run, and the skip remains visible in `## Diagnostics` (`outcome: skipped` / `failure_reason: no_review_produced`). The exact phrase `Nothing is posted to the PR` must not appear in `docs/pr-post-back.md` after the change.

4. **Update `docs/architecture.md` § "Posting Reviews Back to GitHub".** Add a short paragraph stating that an execution-phase run that ends without a posted verdict (failed post, deliberate no-review skip, or soft-budget expiry) posts a best-effort `COMMENT` failure marker naming the run, that the marker carries no verdict and never changes routing, and that the full contract lives in `docs/pr-post-back.md`. Use the exact lowercase phrase `failure marker`.

5. **Correct the `noReviewResult` doc comment in `pkg/steps_checkout_execution.go`.** Replace the paragraph that begins `Nothing is posted rather than a COMMENT — this repo's Definition of Done forbids ...` with an accurate comment: the no-review skip posts no verdict review, but it does post a best-effort failure-marker `COMMENT` naming the run; the marker carries no verdict and never changes the routing, and the skip remains visible in `## Diagnostics` and `run_outcome`. Keep the rest of the method's doc comment (the rationale for `IsNoReviewReason`) intact. The exact phrase `Nothing is posted rather than a COMMENT` must not appear in `pkg/steps_checkout_execution.go` after the change.

6. **Add a `## Unreleased` entry to `CHANGELOG.md`.** The heading must be exactly `## Unreleased`, placed below the file preamble and above the newest `## v0.18.2` section (final order: `# Changelog` → preamble → `## Unreleased` → `## v0.18.2`). Add one bullet describing the change in the file's existing style, for example:
   - a run that ends without posting a verdict (failed post, deliberate no-review skip, or execution-phase soft-budget expiry) now leaves a best-effort `COMMENT` failure marker on the PR naming the run;
   - the marker carries no verdict, never changes routing/phase/status, and is skipped when no poster is configured;
   - planning- and review-phase budget expiries remain out of scope.

   Do not rename or move the existing version sections.
</requirements>

<constraints>
- The Definition of Done amendment replaces the prohibition line; it does not merely append an exception after it. `No new code path posts` must be gone.
- The amendment names both pre-existing `COMMENT` paths (`postHallucinationComment` and the deprecated `PostLGTM`) and states the marker is not a demotion and does not touch `mapVerdictAndSummary`.
- `docs/pr-post-back.md` and `docs/architecture.md` both use the exact lowercase phrase `failure marker`.
- The false statements are removed, not softened: `Nothing is posted to the PR` in `docs/pr-post-back.md` and `Nothing is posted rather than a COMMENT` in `pkg/steps_checkout_execution.go` must both be absent.
- The changelog entry creates exactly one `## Unreleased` heading in the documented position.
- The marker never touches the Issues API and needs no new GitHub App permission.
- Planning-phase and review-phase budget expiries are documented as out of scope.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).
- `grep -q 'failure marker' docs/dod.md && grep -q 'failure marker' docs/pr-post-back.md && grep -q 'failure marker' docs/architecture.md` — each of the three docs carries the phrase.
- `! grep -q 'No new code path posts' docs/dod.md` — the prohibition line is replaced.
- `! grep -q 'Nothing is posted to the PR' docs/pr-post-back.md` — the false no-post claim is removed.
- `! grep -q 'Nothing is posted rather than a COMMENT' pkg/steps_checkout_execution.go` — the stale code comment is corrected.
- `grep -c '^## Unreleased$' CHANGELOG.md` — must print `1`.
- `grep -n 'postHallucinationComment' docs/dod.md` — the amendment names the pre-existing comment path.
- `grep -n 'PostLGTM' docs/dod.md` — the amendment names the deprecated pre-existing comment path.
- Before finishing, re-run `<verification>` and confirm it passes; walk each acceptance criterion against the change.
</verification>
