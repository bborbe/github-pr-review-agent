---
status: approved
spec: [011-pr-visible-failure-marker]
created: "2026-11-18T15:00:00Z"
queued: "2026-11-18T14:49:52Z"
branch: dark-factory/pr-visible-failure-marker
---

# Post the failure marker at every execution-phase terminal routing

<summary>
- A failed post now leaves a comment on the PR naming the failing step and its HTTP status.
- A deliberate no-review skip now leaves a comment on the PR naming that outcome.
- Both execution-phase budget-expiry paths — chunked and single-run — now leave a comment naming the expired budget.
- A successful verdict post leaves no marker.
- A marker whose own POST fails changes nothing: routing, phase, status and the run outcome stay exactly as they were.
- Every call site skips the marker when no poster is configured, so local skip-post runs behave as before.
- The marker is best-effort and its result is discarded; it never adds a diagnostics entry.
- The marker always goes through the same authenticated poster and endpoint the verdict uses.
</summary>

<objective>
Wire `PrPoster.PostFailureMarker` into the three terminal routings that end without a posted verdict in the execution phase — a failed post, a deliberate no-review skip, and an execution-phase soft-budget expiry — from both the chunked and the single-run paths. The marker is best-effort and must never alter the run's routing, phase, status, or `run_outcome`.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Go 1.26, Ginkgo v2/Gomega, Counterfeiter mocks, `github.com/bborbe/errors`, `glog`).

Read these files before changing anything:
- `pkg/steps_checkout_execution.go` — `checkoutExecutionStep`, `postAndRoute`, `noReviewResult`, `chunkOutcome` (its `budgetExpired` branch), `runClaude` (its `budgetExpired` branch), `ExtractPRURL`, `resolvePRInfo`, and `extractRequiredFrontmatter`.
- `pkg/run_outcome.go` — `RunOutcomePostFailed`, `RunOutcomeNoReview`, `RunOutcomeBudgetExpired`, `stampRunOutcome`, `postFailureReason`.
- `pkg/soft_budget.go` — `budgetExpiredResult`. **Do not modify this file.** It is shared by `planningStep` and `reviewStep`, which are out of scope.
- `pkg/export_test.go` — `PostAndRouteForTest` (used to drive `postAndRoute` without Claude).
- `pkg/steps_checkout_execution_test.go` — the existing budget tests, the `buildMD` / `newStep` helpers, and the posting-behaviour `Describe`.
- `pkg/githubposter/poster.go` — `NewPrPoster` and the `HTTPClient` seam the recorder harness will use.
- `pkg/githubposter/poster_test.go` — the `redirectingHTTPClient` + `httptest.NewServer` pattern to mirror.

Reference this coding guide (it lives in the YOLO container, not on the host):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
</context>

<requirements>
1. **Add one unexported helper on `checkoutExecutionStep` in `pkg/steps_checkout_execution.go`.** This is the only place that resolves PR info for the marker and the only place that calls the poster:

   ```go
   func (s *checkoutExecutionStep) postFailureMarker(
       ctx context.Context,
       md *agentlib.Markdown,
       prURLStr, outcome, reason string,
   )
   ```

   Contract:
   - Return immediately when `s.prPoster == nil` (the nil-poster guard every call site relies on).
   - Parse `prURLStr` with `prurl.ParsePRURL(ctx, prURLStr)`. On a parse error, log at `glog.V(2)` and return. Do **not** call `resolvePRInfo` — it appends failure diagnostics and returns an early `agentlib.Result`, and propagating either would change the run's routing.
   - Skip when the parsed `prInfo.Platform != prurl.PlatformGitHub`.
   - Read `ref, _ := md.Frontmatter.String("ref")` and `runID, _ := md.Frontmatter.String("task_identifier")`.
   - Call `s.prPoster.PostFailureMarker(ctx, *prInfo, ref, outcome, reason, runID)` and **discard** the returned `PostResult` (optionally log a non-success at `glog.V(2)`).
   - Never append to `## Diagnostics`; never return an error or a result.

   <!-- Open question for the reviewer: `extractRequiredFrontmatter` does not validate `task_identifier` (it checks clone_url, ref, base_ref only), so `runID` can be empty on a malformed record. This prompt posts the marker anyway with an empty `run:` line. If the marker must always name the run (spec AC 9), decide at audit time whether to skip the marker when `task_identifier` is empty. -->

2. **Failed-post routing in `postAndRoute`.** In the existing failure branch, after `stampRunOutcome(md, RunOutcomePostFailed, postFailureReason(result))`, call:

   ```go
   s.postFailureMarker(ctx, md, prURLStr, RunOutcomePostFailed, postFailureReason(result))
   ```

   then return the existing `&agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "human_review", Message: "posting failed: " + result.ErrorMessage}` unchanged. `prURLStr` is already a parameter of `postAndRoute`.

3. **No-review routing in `postAndRoute`.** The existing block is:

   ```go
   if skipResult, skip := s.noReviewResult(md, verdict, reviewBody, jobRunTime); skip {
       return skipResult, nil
   }
   ```

   Add the marker call before the return:

   ```go
   if skipResult, skip := s.noReviewResult(md, verdict, reviewBody, jobRunTime); skip {
       s.postFailureMarker(ctx, md, prURLStr, RunOutcomeNoReview, "no_review_produced")
       return skipResult, nil
   }
   ```

   Do not change `noReviewResult`'s returned result or its diagnostics. (Its misleading code comment is reconciled in prompt 3.)

4. **Chunked budget-expiry routing in `chunkOutcome`.** In the existing `budgetExpired` branch, after `writeSalvage(...)` and before returning, call:

   ```go
   s.postFailureMarker(
       ctx,
       md,
       ExtractPRURL(md),
       RunOutcomeBudgetExpired,
       "soft_budget_expired:execution",
   )
   ```

   `chunkOutcome` already receives `ctx` and `md`. The reason is the literal `soft_budget_expired:execution` — do **not** read it back from frontmatter (`budgetExpiredResult` stamps its own longer message there).

5. **Single-run budget-expiry routing in `runClaude`.** In the existing `if budgetExpired { ... }` branch, after `writeSalvage(...)` and before returning, call:

   ```go
   s.postFailureMarker(
       ctx,
       md,
       prURLStr,
       RunOutcomeBudgetExpired,
       "soft_budget_expired:execution",
   )
   ```

   `prURLStr` is cached at the top of `runClaude`. Same literal reason.

6. **Do not change `budgetExpiredResult`, `pkg/soft_budget.go`, `planningStep`, `reviewStep`, or the returned routing.** `budgetExpiredResult` is shared by all three phase steps; adding the marker there would post markers for the planning and review expiries the spec declares out of scope. The marker result is discarded, so every routing keeps its exact `Status`, `NextPhase` and `Message`.

7. **Add the recorder-based tests in `pkg/steps_checkout_execution_test.go`.** Mirror the `redirectingHTTPClient` + `httptest.NewServer` pattern from `pkg/githubposter/poster_test.go` (add the needed `net/http`, `net/http/httptest`, `net/url` imports and import `githubposter "github.com/bborbe/github-pr-review-agent/pkg/githubposter"`). The handler records every `POST` body whose path contains `/reviews` and returns `201 {"id":1}`. Add a test-local wrapper so a test can inject the verdict-post failure while the real poster posts the marker:

   ```go
   type markerRecordingPoster struct {
       pkg.PrPoster // the real githubposter provides PostFailureMarker
       postResult   pkg.PostResult
   }

   func (p *markerRecordingPoster) Post(context.Context, pkg.PostRequest) pkg.PostResult {
       return p.postResult
   }
   ```

   Tests required:
   - **Failed post (AC 1).** Drive `PostAndRouteForTest` with `markerRecordingPoster{PrPoster: realPoster, postResult: pkg.PostResult{Outcome: "failed", FailureStep: "post", HTTPStatus: 500, Class: pkg.ErrorClassTransient}}`. Assert the captured marker body contains `"event":"COMMENT"`, `run_outcome: post_failed`, `post (HTTP 500)` (the injected failing step name and status), and the task identifier from frontmatter, and contains neither `no_review` nor `budget_expired`.
   - **No-review skip (AC 2).** Drive `PostAndRouteForTest` with the real poster and a body that triggers `IsNoReviewReason` (e.g. `"(tool 'WebFetch' unavailable)"`). Assert the captured body contains `run_outcome: no_review` and neither `post_failed` nor `budget_expired`, and names the task identifier.
   - **Chunked budget expiry (AC 3).** Mirror the existing "salvages the cut-off chunk" test (two-chunk funnel, fake runner that blocks until the chunk deadline fires), but pass the real recorder poster. Assert the captured body contains `run_outcome: budget_expired` and `soft_budget_expired:execution`, and neither `post_failed` nor `no_review`.
   - **Single-run budget expiry (AC 3).** Mirror the existing single-run budget test (fake runner that blocks until the soft budget fires), but pass the real recorder poster. Same body assertions.
   - **Success leaves no marker (AC 4).** Use `mocks.PrPoster` with `PostReturns(pkg.PostResult{Outcome: "success", ReviewID: 42})`; drive the success path and assert `PostFailureMarkerCallCount() == 0`.
   - **A failed marker changes nothing (AC 6).** Use `mocks.PrPoster` twice: both `Post` return the same failed result; the test run's `PostFailureMarker` returns a failed result (e.g. `Outcome: "failed", HTTPStatus: 500`), the control run's returns success. Compare the whole returned `agentlib.Result` (at minimum `Status`, `NextPhase` and `Message` must be equal), and assert both markdowns still have `run_outcome == post_failed`.
   - **Nil poster is safe (AC 8).** Drive the no-review path and the single-run budget path with `nil` poster; assert the run routes unchanged (`done` / `human_review`) and does not panic.
   - **The marker never carries a verdict (AC 5).** Every recorder assertion also checks `"event":"COMMENT"` and never `"APPROVE"` / `"REQUEST_CHANGES"`.

   Keep the existing tests passing. The existing budget tests pass `nil` or fake posters and assert `PostCallCount() == 0`; those assertions remain true.
</requirements>

<constraints>
- The marker is posted only by `checkoutExecutionStep` at the three execution-phase routings. `budgetExpiredResult` is not modified; `planningStep` and `reviewStep` are untouched.
- Every call site guards on a nil poster. Both budget-expiry sites return before reaching `postAndRoute`, so they need their own `s.prPoster == nil` guard (satisfied by the shared helper).
- The marker is best-effort: no error from it may propagate into the run's `agentlib.Result`; the returned `PostResult` is discarded; no `## Diagnostics` entry is appended for the marker.
- The marker result must not change routing, phase, status or `run_outcome`. A failed marker leaves the run's routing byte-identical to the same routing without the marker.
- Both budget-expiry sites pass the literal `"soft_budget_expired:execution"` as `reason`; they do not read the stamped `failure_reason` back from frontmatter.
- The marker path must not propagate `resolvePRInfo`'s `earlyResult`: doing so would turn a budget-terminated run from `done` / `human_review` into `failed`.
- The marker never posts `APPROVE` or `REQUEST_CHANGES`; it cannot satisfy or clear a merge gate.
- The marker body must not embed the GitHub response body, the raw error, or the `## Diagnostics` block.
- Every marker failure mode is swallowed without changing the run: a 4xx/5xx marker POST, a rate limit, a closed or deleted PR, an unresolvable PR URL, and an empty `ref` (which `postReview` rejects with 422) all leave the routing exactly as it would have been. The helper never panics and never propagates an error.
- Existing tests that construct `PrPoster` fakes must keep compiling.
- Go 1.26, Ginkgo v2/Gomega, Counterfeiter mocks, errors via `github.com/bborbe/errors`, logging via `glog`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).
- `go test ./pkg/... -count=1` — must pass, including the existing budget tests and the new recorder tests.
- `grep -c 's.postFailureMarker(' pkg/steps_checkout_execution.go` — must print `4` (the failed-post, no-review, chunked-budget and single-run-budget call sites).
- `grep -c 'soft_budget_expired:execution' pkg/steps_checkout_execution.go` — must print `2` (both execution budget sites).
- `grep -n 'PostFailureMarker' pkg/steps_checkout_execution.go` — prints the helper's call to the poster.
- Before finishing, re-run `<verification>` and confirm it passes; walk each acceptance criterion against the change.
</verification>
