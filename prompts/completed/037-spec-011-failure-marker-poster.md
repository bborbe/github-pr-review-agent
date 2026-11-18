---
status: completed
spec: [011-pr-visible-failure-marker]
summary: Added the PostFailureMarker seam to PrPoster with a bounded 2000-char non-verdict COMMENT body, regenerated the Counterfeiter mock, added tests for body/outcome/no-verdict/length/return shapes, and updated CHANGELOG.md.
execution_id: 2567b9bc-4c6b-5543-b964-1c92c2fa426f-exec-037-spec-011-failure-marker-poster
dark-factory-version: dev
created: "2026-11-18T15:00:00Z"
queued: "2026-11-18T14:45:07Z"
started: "2026-11-18T14:45:08Z"
completed: "2026-11-18T14:57:27Z"
branch: dark-factory/pr-visible-failure-marker
---

# Add PostFailureMarker to the PrPoster seam

<summary>
- The review poster gains one new capability: post a non-verdict comment naming a run that ended without posting a verdict.
- The comment is always a `COMMENT` review, never an approve or request-changes, so it cannot satisfy or clear a merge gate.
- Its body is built only from the run outcome, a bounded reason, and the run's identifier.
- The body is hard-capped at 2000 characters and never embeds a response body, a raw error, or the diagnostics block.
- The comment is delivered through the same authenticated endpoint the existing comment-after-dismiss path already uses.
- The generated mock for the poster is regenerated in the same change so every existing fake still compiles.
- Existing posting behaviour (the verdict review, the override approve, the hallucination comment, the deprecated LGTM) is unchanged.
</summary>

<objective>
Add a single, bounded, non-verdict posting seam to `PrPoster` so the execution phase can leave a visible marker on a PR whose run produced no verdict. This prompt adds only the interface method, its implementation through the existing `postReview` plumbing, the mock regeneration, and the body/no-verdict/length tests; the call sites follow in the next prompt.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Go 1.26, Ginkgo v2/Gomega, Counterfeiter mocks, `github.com/bborbe/errors`, `glog`).

Read these files before changing anything:
- `pkg/poster_types.go` — `PrPoster` interface, `PostResult`, and the `//counterfeiter:generate` directive that produces `mocks/pr-poster.go`.
- `pkg/githubposter/poster.go` — `prPoster`, `NewPrPoster`, `postReview`, `PostLGTM`, `postHallucinationComment`, `maxGitHubCommentBody`, and the `postReviewReq` payload shape (`Event`, `CommitID`, `Body`).
- `pkg/githubposter/poster_test.go` — the existing test harness: `makeHTTPResp`, `callSpec`, `seqStub`, the `redirectingHTTPClient` + `httptest.NewServer` pattern used by the `PostLGTM` integration boundary test, and `fakeClient.DoStub` body-capture patterns.
- `pkg/run_outcome.go` — `RunOutcomePostFailed`, `RunOutcomeNoReview`, `RunOutcomeBudgetExpired`.
- `docs/pr-post-back.md` — the posting contract this seam must fit (same endpoint family, same permission, no Issues API).

Reference this coding guide (it lives in the YOLO container, not on the host):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
</context>

<requirements>
1. **Add exactly one method to the `PrPoster` interface in `pkg/poster_types.go`.** The name and signature are frozen — later prompts and the spec's verification grep them:

   ```go
   // PostFailureMarker posts a best-effort COMMENT review naming a run that
   // ended without posting a verdict. It carries no verdict, never changes
   // routing, phase or status, and its result is discarded by the caller.
   // outcome is one of RunOutcomePostFailed / RunOutcomeNoReview /
   // RunOutcomeBudgetExpired. reason is a bounded string naming the failing
   // step (and its HTTP status where applicable). runID is the run's task
   // identifier, which the poster cannot otherwise know. event is always
   // "COMMENT".
   PostFailureMarker(
       ctx context.Context,
       pr prurl.PRInfo,
       headSHA, outcome, reason, runID string,
   ) PostResult
   ```

   Add it to the interface alongside the existing methods. Do not change any existing method or the `PostRequest` / `PostResult` shapes.

2. **Implement `PostFailureMarker` on `*prPoster` in `pkg/githubposter/poster.go`.** It must call the existing `p.postReview(ctx, pr, headSHA, "COMMENT", body)` — do not add a second HTTP path, a second URL builder, or a different auth header. `event` is the literal `"COMMENT"`. Follow the `PostLGTM` / `postHallucinationComment` shape for using `postReview`.

   - On `proceed == false`, return the failed `prpkg.PostResult` unchanged.
   - On success, return a `prpkg.PostResult` with `Outcome: "success"`, `ReviewID` from `postReview`, and `PostedEvent: "COMMENT"`.
   - Do **not** call `verifyAfterPost` — the marker is best-effort and must not add a verify GET.
   - Do **not** append anything to `## Diagnostics`; the caller discards the result.

3. **Add an unexported body builder and its hard cap in the same file.**

   ```go
   const maxFailureMarkerBody = 2000

   func buildFailureMarkerBody(outcome, reason, runID string) string
   ```

   - Compose a body that contains, in this order and with these exact keys:
     - a one-line human-readable opening stating that the run did not post a verdict;
     - `run_outcome: <outcome>`;
     - `run: <runID>`;
     - `failure_reason: <reason>`;
     - a closing line stating that this is a failure marker, carries no verdict, and does not change the merge gate.
   - Hard-cap the final body at `maxFailureMarkerBody` characters. The `run_outcome:` and `run:` lines must survive truncation; truncate the reason/tail rather than those keys.
   - The body must never contain a GitHub response body, a raw error string, or any `## Diagnostics` content. The builder's only inputs are the three strings.

4. **Regenerate the Counterfeiter mock.** Run `make generate` (it runs `go generate -mod=mod ./...`, which reads the `//counterfeiter:generate` directive in `pkg/poster_types.go`). Do not hand-edit `mocks/pr-poster.go`. Confirm the regenerated file contains `PostFailureMarker`, `PostFailureMarkerCallCount`, `PostFailureMarkerArgsForCall`, and `PostFailureMarkerReturns`.

5. **Add the body/no-verdict/bounded-length tests in `pkg/githubposter/poster_test.go`.** Follow the existing external-test-package style and the `fakeClient.DoStub` body-capture pattern. Add a `Describe("PostFailureMarker", ...)` block that covers:
   - **Each outcome produces the right body.** For `post_failed` with reason `"post (HTTP 500)"` and runID `"task-123"`: the captured POST body carries `"event":"COMMENT"`, contains `run_outcome: post_failed`, contains `post (HTTP 500)`, contains `task-123`, and contains neither `no_review` nor `budget_expired`. For `no_review` with reason `"no_review_produced"`: contains `run_outcome: no_review` and neither `post_failed` nor `budget_expired`. For `budget_expired` with reason `"soft_budget_expired:execution"`: contains both `run_outcome: budget_expired` and `soft_budget_expired:execution`, and neither `post_failed` nor `no_review`.
   - **The marker never carries a verdict.** Every captured request has `"event":"COMMENT"` and never `"APPROVE"` or `"REQUEST_CHANGES"`; the returned `PostResult.PostedEvent` is `"COMMENT"`, not `APPROVE` / `REQUEST_CHANGES`.
   - **The body is bounded and carries no diagnostics.** With a 10,000-character `reason`, the captured body is `<= 2000` characters and contains neither `## Diagnostics`, `response_body:`, nor `error_message:`.
   - **Success and failure return shapes.** A 201 response returns `Outcome == "success"` and `PostedEvent == "COMMENT"`; a permanent 403 returns `Outcome == "failed"`. The success path makes exactly one HTTP call (the POST) — no verify GET.

   Use the existing `makeHTTPResp` / `seqStub` / `fakeClient.DoStub` helpers where they fit. If a body-capturing stub is clearer, read `req.Body` inside the stub (as the existing `DismissCurrentReview` tests do) before returning the canned response.

6. **Do not change `Post`, `PostOverrideApprove`, `PostLGTM`, `postHallucinationComment`, `mapVerdictAndSummary`, or `maxGitHubCommentBody`.** The verdict path and the existing comment paths keep their exact behaviour.
</requirements>

<constraints>
- `PrPoster` gains exactly one method, named `PostFailureMarker` with the signature above. The name is frozen: the spec's verification greps it.
- The implementation calls `postReview(ctx, pr, headSHA, "COMMENT", body)`; it does not add a second HTTP path. `event` is always `COMMENT`.
- The marker body is composed only from `run_outcome`, the bounded `reason`, and `runID` — never a response body, an error string, or file content.
- The marker body is hard-capped at 2000 characters.
- The marker is best-effort: the returned `PostResult` is discarded by callers; no `## Diagnostics` entry is appended for the marker.
- The marker never posts `APPROVE` or `REQUEST_CHANGES` and cannot satisfy or clear a merge gate.
- The marker never touches the GitHub Issues API (`issues:` must not appear in `pkg/githubposter/poster.go`).
- `mocks/pr-poster.go` is generated — regenerate it with `make generate`, never hand-edit it.
- Existing tests that construct `PrPoster` fakes must keep compiling.
- Go 1.26, Ginkgo v2/Gomega, Counterfeiter mocks, errors via `github.com/bborbe/errors`, logging via `glog`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).
- `go test ./pkg/githubposter/... -count=1` — must pass, including the new `PostFailureMarker` block.
- `grep -n 'PostFailureMarker' pkg/poster_types.go pkg/githubposter/poster.go mocks/pr-poster.go` — prints the interface method, the implementation, and the regenerated mock methods.
- `! grep -q 'issues/' pkg/githubposter/poster.go` — the marker never touches the Issues API.
- Before finishing, re-run `<verification>` and confirm it passes; walk each acceptance criterion against the change.
</verification>
