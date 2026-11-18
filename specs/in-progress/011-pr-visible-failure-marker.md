---
status: approved
approved: "2026-11-18T14:02:49Z"
branch: dark-factory/pr-visible-failure-marker
---

## Summary

- A pr-reviewer run that ends without posting a review records that fact only in its own task file, which nobody watching the pull request reads.
- The PR stays silently gated: a reviewer sees "no review yet" and cannot tell that a run already ran and died.
- This change makes the PR carry a COMMENT review naming the failed run, for the three terminal routings that end without a posted verdict.
- The task-file half already exists (`run_outcome` / `failure_reason`); this is the PR-visible half of the same marker.
- Scope is the execution-phase routing only; planning- and review-phase expiries are a separate follow-up.

## Problem

Every terminal routing in this agent returns `Done` + `human_review`: a failed post, a deliberate no-review skip, and an expired soft budget alike. The controller writes `phase: human_review` and clears `current_job`, so the record reads like a review awaiting the operator. That ambiguity was already fixed for the *record* — each routing stamps `run_outcome` (`post_failed` / `no_review` / `budget_expired`) plus a bounded `failure_reason`.

The pull request has no equivalent. A reviewer looking at a gated PR sees no review and no explanation; the only evidence that a run happened at all lives in a task file in another system. Measured on 2026-11-07 (`bborbe/nuke#1104`): a PR sat blocked on a bot verdict that never arrived, with zero reviews and zero comments, and nothing on the artifact said why.

## Goal

A pull request that a pr-reviewer run failed to review carries a visible marker naming that run and why it produced no verdict, so the block is visible where the merge decision is made.

## Non-goals

- Planning-phase and review-phase budget expiries. They need a poster threaded into two step types this change does not touch; filed separately.
- Changing any routing, phase, status or the never-posts guarantee for the *verdict* review. A run that posts no verdict today still posts none after this change, except for the marker.
- Labels, and the GitHub Issues API. **The marker is not an issue comment** — see Assumptions; the App holds no `issues:write` and this design does not need it.
- Retrying or recovering the failed run.
- The prod pin. Deployment is out of scope here.

## Assumptions

- **The delivery mechanism already exists and is proven.** `postHallucinationComment` (`pkg/githubposter/poster.go:421`) posts a non-verdict comment with `p.postReview(ctx, pr, headSHA, "COMMENT", body)` — i.e. `POST /repos/{owner}/{repo}/pulls/{n}/reviews` with `event: COMMENT`. That endpoint is governed by the App's existing `pull_requests:write` permission (`docs/github-app-setup.md:149`). The marker reuses this exact path, so **no GitHub App permission change is required and the Issues API is not touched.**
- A `COMMENT` review does not satisfy branch protection and does not change `reviewDecision` — it leaves the PR `REVIEW_REQUIRED`, which is correct: the marker must make the block visible without clearing it.
- `budgetExpiredResult` (`pkg/soft_budget.go`) is shared by all three phase steps. It must **not** gain the marker: its callers include `steps_planning.go` and `steps_review.go`, so threading a poster there would post markers for the expiries this spec declares out of scope. The marker is posted by `checkoutExecutionStep` at its own call sites instead.
- There are **two** execution-phase budget-expiry call sites — the chunked path and the single-run path — and both must post.
- A marker needs the PR head SHA, which `postReview` requires. At the chunked collector the head SHA is `md.Frontmatter.String("ref")` and the PR URL is `ExtractPRURL(md)` — both already available from the markdown, so no re-derivation is needed. What the marker path must **not** do is propagate `resolvePRInfo`'s `earlyResult`: doing so would turn a budget-terminated run from `done`/`human_review` into `failed`, which is the exact regression AC 6 guards on the POST path.
- **The `reason` argument is not always the stamped `failure_reason`.** At both budget sites the stamped value is `budgetExpiredResult`'s long message, while AC 3 requires the literal `soft_budget_expired:execution`. Those two call sites pass `"soft_budget_expired:" + stepName` as `reason` explicitly; they do not read it back from frontmatter.
- **Alternatives considered and rejected:** delivering the marker as a GitHub *issue comment* (`POST /issues/{n}/comments`). Rejected because the App holds no `issues:write` (`docs/github-app-setup.md:149`) and the project does not use GitHub issues. Recorded so it is not re-proposed.
- `docs/pr-post-back.md` documents the posting contract and `## Diagnostics`; `docs/architecture.md` § "Posting Reviews Back to GitHub" points at it. Both describe the current contract and must be updated to describe this marker.
- `docs/dod.md:20` currently reads "No new code path posts to a PR other than the single structured review (+ the override approve)". That line is **already inaccurate** — `postHallucinationComment` posts a COMMENT review today. The amendment therefore corrects an existing omission as well as covering the marker.

## Acceptance Criteria

- [ ] **A failed post leaves a PR-visible marker.** Evidence: an httptest recorder captures `POST /repos/{owner}/{repo}/pulls/{n}/reviews` with `event: COMMENT` and a body containing `run_outcome: post_failed`, the **injected** failing step name, its HTTP status, and **neither** `no_review` nor `budget_expired`, on the failed-post path.
- [ ] **A deliberate no-review skip leaves a PR-visible marker.** Evidence: the same recorder captures a `COMMENT` review body containing `run_outcome: no_review` and **neither** `post_failed` nor `budget_expired`.
- [ ] **BOTH execution-phase budget-expiry call sites post a marker.** Evidence: two tests, one driving the chunked path and one driving the single-run path, each capture a `COMMENT` review body containing `run_outcome: budget_expired` and `soft_budget_expired:execution`, and **neither** `post_failed` nor `no_review`.
- [ ] **A successful post leaves NO marker.** Evidence: a negative-control test runs the success path and asserts no additional review request carried a marker body.
- [ ] **The marker never carries a verdict.** Evidence: every captured marker request has `event: COMMENT` and never `APPROVE` or `REQUEST_CHANGES`; a test asserts `reviewDecision` semantics are untouched by checking the marker path never returns a `PostedEvent` of APPROVE/REQUEST_CHANGES.
- [ ] **A marker that fails to post does not change the run's outcome.** Evidence: a test where the marker POST returns 500 asserts the returned `agentlib.Result` is byte-identical to the same routing without the marker (`Status: done`, `NextPhase: human_review`, same `Message`), and that `run_outcome` is still stamped.
- [ ] **The marker body is bounded and carries no response body.** Evidence: a test asserts the posted body is ≤ 2000 characters and contains neither the raw error text nor any `## Diagnostics` content.
- [ ] **The marker is suppressed when no poster is configured.** Evidence: a test on the nil-poster path asserts no request is made and the run routes unchanged.
- [ ] **The marker body names the run.** Evidence: a test asserts the posted body contains the run's task identifier.
- [ ] **`docs/dod.md` § Review-Posting Safety is amended** to permit this path: a `COMMENT` review carrying no verdict, best-effort, never changing routing, phase or status — naming both pre-existing COMMENT paths it already omitted (`postHallucinationComment` and the deprecated `PostLGTM`), and stating explicitly that the marker is **not** a demotion of the verdict path and does not touch `mapVerdictAndSummary`. Evidence: `grep -n 'failure marker' docs/dod.md` returns ≥1 line, AND `grep -n 'No new code path posts' docs/dod.md` returns 0 lines — the prohibition line is replaced, not merely followed by an exception.
- [ ] **The two now-false statements are reconciled in the same change.** `docs/pr-post-back.md:127` ("Nothing is posted to the PR — deliberately, since the repo's own Definition of Done forbids a new posting path") and the code comment at `pkg/steps_checkout_execution.go:1145-1149` say the opposite of this change. Evidence: `grep -c 'Nothing is posted to the PR' docs/pr-post-back.md` returns 0, and `grep -c 'Nothing is posted rather than a COMMENT' pkg/steps_checkout_execution.go` returns 0 — that second probe returns **1 today**, so it fails before the fix and cannot pass vacuously.
- [ ] **`docs/pr-post-back.md` and `docs/architecture.md` describe the marker.** Evidence: `grep -c 'failure marker' docs/pr-post-back.md docs/architecture.md` returns ≥1 for each.
- [ ] **A `## Unreleased` CHANGELOG entry exists.** The file's top section is currently `## v0.18.2`, so the entry must create the heading in the documented position. Evidence: `grep -c '^## Unreleased$' CHANGELOG.md` returns 1.
- [ ] **`make precommit` exits 0**, including `make test`, on the change.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — lint, vet, format, generate and `make test` clean
- `make test` — the suite passes
- `grep -n 'PostFailureMarker' pkg/poster_types.go pkg/githubposter/poster.go` — the seam exists on the interface and has one implementation
- `grep -n 'failure marker' docs/dod.md docs/pr-post-back.md docs/architecture.md` — the three doc updates landed
- `grep -c 'issues/' pkg/githubposter/poster.go` — returns 0; the marker never touches the Issues API

### Operator-executable (runs on the host after PR merge)

- On a dev PR whose run expired its budget, `gh api repos/<owner>/<repo>/pulls/<n>/reviews --jq '.[] | select(.body | test("run_outcome: budget_expired")) | .state'` prints `COMMENTED`

## Desired Behavior

1. When a run produces a review but the GitHub write fails, a `COMMENT` review is posted on the PR naming the failing step and its HTTP status.
2. When a run deliberately skips posting because it produced no review, a `COMMENT` review is posted naming `no_review`.
3. When an execution-phase run exhausts its soft time budget, a `COMMENT` review is posted naming `budget_expired` and the phase that expired — from **both** the chunked and the single-run execution paths.
4. A run that posts its verdict successfully leaves no marker.
5. A marker whose own POST fails leaves the run's routing, phase, status and `run_outcome` exactly as they would have been without it.
6. The marker goes through the same authenticated client, endpoint family and permission the existing comment-after-dismiss path uses, and is skipped entirely when no poster is configured.
7. `docs/dod.md` § Review-Posting Safety records this path as a named, bounded exception — and names the comment-after-dismiss path it already omitted.
8. The marker body is composed only from `run_outcome`, the bounded `failure_reason` and the run's task identifier — never a response body, an error string or file content.

## Constraints

- `PrPoster` gains exactly one method, named `PostFailureMarker(ctx context.Context, pr prurl.PRInfo, headSHA, outcome, reason, runID string) PostResult`. The name is frozen: the verification greps it. `runID` is the run's task identifier, which the poster cannot otherwise know — `prPoster` holds only `httpClient`, `ghToken`, `botLogin` and `currentDateTime`, and `prurl.PRInfo` carries only owner/repo/number. The identifier is available at each call site from the markdown frontmatter.
- The implementation calls the existing `postReview(ctx, pr, headSHA, "COMMENT", body)` rather than adding a second HTTP path. `event` is always `COMMENT`.
- Its counterfeiter mocks must be regenerated in the same change — `mocks/` is generated from the `//counterfeiter:generate` directive in `pkg/poster_types.go`, and `make precommit` runs `generate`.
- The marker is best-effort: no error from it may propagate into the run's `agentlib.Result`. `PostFailureMarker` returns a `PostResult` that is **discarded** (optionally logged at `V(2)`); no `## Diagnostics` entry is appended for the marker, so a failed marker leaves no trace by design — the run's `run_outcome` is the record's only evidence.
- **Every call site guards on a nil poster.** `postAndRoute` already returns early when `prPoster` is nil, but both budget-expiry sites return before reaching it, so each needs its own `if s.prPoster != nil` guard or the marker path panics on a nil interface.
- `budgetExpiredResult` is not modified. Only `checkoutExecutionStep` gains call sites; `planningStep` and `reviewStep` are untouched.
- The comment body must not embed the GitHub response body, the raw error, or the `## Diagnostics` block — those already live in the record.
- Existing tests that construct `PrPoster` fakes must keep compiling.
- The never-posts guarantee for the *verdict* is unchanged: this change adds a `COMMENT` review, never an `APPROVE` or `REQUEST_CHANGES`.

## Failure Modes

| Trigger | Expected behavior | Detection | Reversibility | Recovery |
|---|---|---|---|---|
| Marker POST returns 4xx/5xx | Run routes exactly as before; marker silently absent | Operator reads `run_outcome` in the record, which is unaffected | Reversible — the marker review was never created | Re-run the review; the marker posts on the next failure |
| GitHub rate limit on the marker | Same as above; no retry storm | Same | Reversible | Existing rate-limit classification governs the verdict path, not the marker |
| PR closed or deleted mid-run | Marker POST fails; run routes as before | Run record shows the routing | Irreversible but moot — the PR is gone | None needed |
| Process dies after the marker posts, before the diagnostic is written | Marker exists; record lacks the stamp | Marker present, `run_outcome` absent | **Append-only** — a `COMMENTED` review cannot be dismissed (the dismiss endpoint 422s on anything but APPROVED/CHANGES_REQUESTED, per `docs/pr-post-back.md:170`), so the marker is left in place | None — the body names the run, so a reader can tell it apart |
| Marker posted twice for one run | Two markers naming the same run | Both carry the same task identifier | **Append-only** — no cleanup API exists | None — the run identifier distinguishes them |
| A `COMMENT` review is mistaken for a verdict | PR stays `REVIEW_REQUIRED`; merge gate unaffected | `reviewDecision` unchanged after the marker | Reversible | None needed — this is the intended behaviour |
| PR URL or head SHA unresolvable at the marker site | Marker skipped entirely; the budget routing is unchanged (`done` / `human_review`) | `run_outcome` still reads `budget_expired` | Not attempted — nothing was posted | None needed; the record already says why the run died |
| Empty `ref` reaches the POST | `postReview` returns 422 and the marker is skipped; routing unchanged | Nothing observable — this is the silent-by-design case | Reversible | None; the run record carries the outcome |

## Security / Abuse Cases

The marker body is composed from `run_outcome`, a bounded `failure_reason` and the run's task identifier — never from a response body, an error string, or file content. The POST goes to the same endpoint family the verdict review already uses, with the same token, so no new credential, scope or API surface is introduced. The marker carries no verdict and cannot satisfy or clear a merge gate.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | `PostFailureMarker` on the interface, its bounded body builder, its implementation via `postReview` with `event: COMMENT`, mock regeneration, and the body/no-verdict/bounded-length tests | 8 | 5, 7, 9 | — |
| 2 | Call sites at both execution budget-expiry paths and both `postAndRoute` routings, plus the negative control, best-effort and nil-poster tests | 1, 2, 3, 4, 5, 6 | 1, 2, 3, 4, 6, 8 | prompt 1 |
| 3 | Doc updates: `docs/dod.md` amendment, `docs/pr-post-back.md` (including the now-false "Nothing is posted" line), `docs/architecture.md`, the `steps_checkout_execution.go` comment, CHANGELOG | 7 | 10, 11, 12, 13, 14 | prompts 1, 2 |

## Do-Nothing Option

A gated PR stays indistinguishable from a PR nobody has reviewed yet. The record already says why the run died, but the person deciding whether to merge reads the PR, not the task file — so the block stays invisible at the point of decision, which is the defect this row was filed on.
