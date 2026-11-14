---
status: completed
summary: 'Rate-limit 403/429 responses now classify as transient, the retry waits for X-RateLimit-Reset, and both rate-limit headers are persisted into ## Diagnostics'
execution_id: github-pr-review-agent-ratelimit-exec-036-fix-rate-limit-403-transient
dark-factory-version: v0.198.22
created: "2026-11-14T13:47:13Z"
queued: "2026-11-14T13:47:13Z"
started: "2026-11-14T13:57:00Z"
completed: "2026-11-14T14:04:37Z"
---

<summary>
- A 403 whose body names a GitHub rate limit, or whose `X-RateLimit-Remaining` is `0`, classifies as `transient` instead of `permanent`.
- A plain 403 — bad token, no repository access — still classifies as `permanent`, so a broken credential is not retried.
- The retry waits until `X-RateLimit-Reset` when that lies further out than the fixed base delay, instead of burning its second attempt inside the exhausted window.
- A reset beyond the step's context deadline ends the step as `transient` with no POST, not as `permanent`.
- Both rate-limit headers are persisted into the `## Diagnostics` block so the next real rate-limited run leaves the captured pair behind.
- The dismiss-list step no longer ends a run with no verdict when it is rate-limited.
</summary>

<objective>
The pr-reviewer drops reviews on a GitHub App installation rate limit during the dismiss-list read. `classifyError` maps every 403 to `ErrorClassPermanent` alongside 401/404/422, so a rate limit — transient by definition, carrying a reset — ends the run without retrying, the PR gets no verdict, and its merge stays gated. Measured over the pr-review records that started in `2026-11-07T00:00:00Z → 2026-11-09T00:00:00Z`, 11 of the 11 that recorded a `failure_step` recorded this exact signature, and all-time it is the largest single recorded failure step (29 of 76).
</objective>

<context>
Read `CLAUDE.md` for project conventions (Go, Ginkgo v2/Gomega, Counterfeiter mocks, `github.com/bborbe/errors`, `glog`).

Read these files before changing anything:
- `pkg/githubposter/retry.go` — `classifyError`, `retryCall`, `CallResult`, `retryBaseDelay`, `retryJitterMs`. This is the defect site.
- `pkg/githubposter/poster.go` — `doRequest` (returns `(int, []byte, error)` and discards response headers), the six `retryCall` call sites (`dismissPriorReviews` → `listBotReviews`, `dismissOne`, `listBotReviewsAtHead`, `executeDismissPUT`, `postReview` — the POST, `verifyAfterPost` — the verify-GET), and `buildFailedResult`.
- `pkg/githubposter/verifier.go` — the seventh `retryCall` call site, `VerifyReview`.
- `pkg/githubposter/export_test.go` — `ClassifyErrorForTest`; `retryBaseDelay` / `retryJitterMs` are zeroed in its `init`.
- `pkg/poster_types.go` — `PostResult`, the struct the `## Diagnostics` block is built from.
- `pkg/steps_checkout_execution.go` — `buildDiagnosticBlock`, which formats the failed-attempt YAML block.
- `docs/pr-post-back.md` § Diagnostic Block Format — the field list requirement 5 extends — and § Dismissal Contract, the invariant requirement 8 preserves.
- `pkg/steps_review.go` — `shouldVerifyPost` / `lastYAMLBlock`, which parse the `## Diagnostics` block back. Requirement 5 changes that serialization, so this reader must keep working.
- `pkg/githubposter/retry_test.go` — the existing `classifyError` table.
- `pkg/githubposter/poster_test.go` — the HTTP client fake (`mocks.HTTPClient`, `redirectingHTTPClient`) and how `retryCall`-driven paths are exercised.
</context>

<requirements>
1. **Return the response headers from `doRequest`.** Change the signature in `pkg/githubposter/poster.go` to return `(int, http.Header, []byte, error)`, returning `resp.Header` on success and a nil header on the error paths. Update every call site — `poster.go` has six (`listBotReviews`, `dismissOne`, `listBotReviewsAtHead`, `executeDismissPUT`, `postReview`, `verifyAfterPost`) and `verifier.go` has one (`VerifyReview`), seven in total — and keep each one's existing error handling and body truncation exactly as-is.

2. **Thread the headers through `retryCall`.** Change the closure type to `func(ctx context.Context) (T, int, http.Header, string, error)` and add `ResponseHeader http.Header` to `CallResult`. Populate it on all four return paths (success, non-transient failure, context-cancelled, second attempt).

3. **Classify a rate limit as transient.** Change `classifyError` to take the response header and body: `classifyError(httpStatus int, header http.Header, body string, err error) prpkg.ErrorClass`. Keep the existing order — the phantom-POST sentinel, context errors, `net.Error`, `httpStatus == 0`, `>= 500` / `429` — then, before the `401 || 403 || 404 || 422` branch, add a rate-limit test:

   - a `403` or `429` whose `X-RateLimit-Remaining` header is `0` is a rate limit;
   - a `403` or `429` whose body contains `rate limit` (case-insensitive) is a rate limit — this covers the primary limit's `API rate limit exceeded for installation ID …` and the secondary limit's `You have exceeded a secondary rate limit`;
   - anything else keeps its current class. **A plain 403 with neither signal must still return `ErrorClassPermanent`** — a bad token or a missing repository permission is not retried.

   Add a small unexported helper for the test (e.g. `isRateLimited(status int, header http.Header, body string) bool`) rather than inlining the condition twice.

4. **Honour the reset before the retry.** Parse `X-RateLimit-Reset` (epoch seconds) into a `time.Time`. Compute the wait as the existing base delay plus jitter, extended to the reset instant when that is further out. Replace the current fixed `time.After(retryBaseDelay + jitter)` with that computed wait; keep the existing `ctx.Done()` arm unchanged so a reset beyond the step's deadline returns the transient result with `Attempts: 1` rather than sleeping. An absent, empty, or unparsable `X-RateLimit-Reset` leaves the wait at base + jitter — no error, no special case. A direct clock read here is deliberate (this is a free generic function with no constructor to inject through, matching `pkg/funnel.go`); do not add a clock parameter. Keep the `// #nosec G404` comment and its `//nolint:gosec` marker on the jitter line — the lint config requires both.

5. **Persist both headers into `## Diagnostics`.** Add `RateLimitRemaining string` and `RateLimitReset string` to `prpkg.PostResult` in `pkg/poster_types.go`, populate them in `buildFailedResult` from `cr.ResponseHeader`, and emit them in `buildDiagnosticBlock`'s failed-attempt block. Emit each only when non-empty, and keep the existing fields and their order intact — an ordinary failure with no rate-limit headers must produce a byte-identical block to today's. Do not add the fields to the success or `skipped` branches.

6. **Add the fixture.** Create `pkg/githubposter/testdata/ratelimit-403.body` containing the real response body copied verbatim from a live record — `{"message":"API rate limit exceeded for installation ID 134414316.","documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting","status":"403"}` — and `pkg/githubposter/testdata/ratelimit-403.headers` containing the two documented rate-limit fields, one per line, `x-ratelimit-remaining: 0` and `x-ratelimit-reset: 1763000000`. Add a small loader in the test file (plain `os.ReadFile` on the path is fine) rather than `go:embed`, so the fixture stays readable as data. Build the `http.Header` with `Header.Set`, never a lowercase map literal — `Header.Get` canonicalises the lookup key, so `http.Header{"x-ratelimit-remaining": …}` would never be found and would not model what `resp.Header` actually produces.

7. **Tests.** Extend `pkg/githubposter/retry_test.go` and update `ClassifyErrorForTest` in `export_test.go` to the new signature. Cover:
   - the fixture body + `x-ratelimit-remaining: 0` at 403 → `ErrorClassTransient`;
   - the fixture body with no headers at 403 → `ErrorClassTransient` (body-only detection);
   - a plain 403 (`{"message":"Bad credentials"}`) with no rate-limit headers → `ErrorClassPermanent`;
   - 429 with neither signal → `ErrorClassTransient` (unchanged);
   - a retry whose response carries `x-ratelimit-reset` set 2 seconds ahead waits at least until it: `X-RateLimit-Reset` is epoch **seconds**, so `now.Add(2*time.Second).Unix()` truncates to 1–2 s ahead — assert against the parsed reset instant (`resetTime.Sub(start)`), never a hard 2 s literal. Read a monotonic clock around the two calls and assert the elapsed time is at least that delta, not the zeroed base delay;
   - a retry whose reset lies beyond the context deadline returns `Class: ErrorClassTransient` with `Attempts: 1` and the call closure invoked exactly once;
   - `buildDiagnosticBlock` emits `rate_limit_remaining` and `rate_limit_reset` lines when the `PostResult` carries them, and produces output byte-identical to the current block when both are empty. Expose `buildDiagnosticBlock` through a `pkg/export_test.go` `...ForTest` hook (follow the existing `ShouldVerifyPostForTest` pattern), and add a `class: permanent` block carrying the two new lines to the `## Diagnostics` round-trip tests in `pkg/steps_review_test.go`, so `shouldVerifyPost` / `lastYAMLBlock` are proven to still parse the block and still skip verification.
   Expose whatever `retryCall` wrapper the timing tests need from `export_test.go`; do not weaken `retryBaseDelay = 0` / `retryJitterMs = 0` in `init`, since the reset test must prove the wait came from the header rather than the base delay.

8. **Do not change** the event mapping, the dismissal contract (`reviews at the current head SHA are NEVER dismissed`), the phantom-POST sentinel's behaviour, `maxGitHubCommentBody` truncation, or any step name. `step` strings are load-bearing — the regression metric greps `GET /pulls/N/reviews (dismiss-list)` verbatim.

9. **Add the CHANGELOG entry.** `docs/dod.md` requires one under `## Unreleased`, and that section does not exist — `CHANGELOG.md` currently runs from the preamble straight to `## v0.17.1`. Create `## Unreleased` below the preamble and above `## v0.17.1` (never between the `# Changelog` title and the preamble), with one `fix:` bullet covering the rate-limit 403 reclassification, the reset-aware retry wait, and the two new `## Diagnostics` fields.

10. **Add the two optional rate-limit lines to `docs/pr-post-back.md` § Diagnostic Block Format**, and leave its existing sample block intact — that sample models the no-headers case, which stays byte-identical. Do not change the README: this change alters retry classification, not a run mode, a configuration field, or verdict behaviour.
</requirements>

<constraints>
- A rate limit is `transient`; a credential or permission 403 is `permanent`. Do not collapse them.
- The retry budget stays at two attempts. This prompt changes *when* the second attempt fires, not how many there are.
- The `## Diagnostics` block for a failure carrying no rate-limit headers must be byte-identical to the current output.
- Errors via `github.com/bborbe/errors` (no `fmt.Errorf`, no bare `return err`); logging via `glog` with `V(n)` gating.
- Ginkgo v2 / Gomega for tests.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).
- `go test ./pkg/githubposter/... -count=1` — must pass, including the new rate-limit contexts.
- `grep -n 'ErrorClassPermanent' pkg/githubposter/retry.go` — must still show the 401/403/404/422 branch (the plain-403 path is retained, not deleted).
- `grep -n 'X-RateLimit-Reset' pkg/githubposter/retry.go` — must print at least one line.
- `ls pkg/githubposter/testdata/ratelimit-403.body pkg/githubposter/testdata/ratelimit-403.headers` — both exist.
</verification>
