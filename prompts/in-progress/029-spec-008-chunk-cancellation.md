---
status: approved
spec: [008-parallel-chunk-review]
created: "2026-10-23T19:12:02Z"
queued: "2026-10-23T19:27:14Z"
---

# Chunk cancellation guarantees, the pre-chunk seam, and the changelog

<summary>
- The dispatcher exposes exactly one pre-chunk seam, a method that is called once per chunk from the dispatching goroutine before that chunk starts; its placeholder body returns nil and a future mid-run PR-state check can fill it without touching the dispatch loop.
- A chunk that runs out of time ends the whole review with the existing budget-expired result and salvage, even when its siblings report cancellation errors first.
- Salvage is written exactly once, carrying the lowest-index expiring chunk's partial, and no goroutine races the markdown handle.
- A chunk runner failure ends the review as failed, cancels every sibling still running, and starts no further chunk.
- Cancelling the parent context stops every in-flight chunk, starts no new one, and the step returns only after every chunk goroutine has exited.
- Every one of these behaviours is locked by a unit test, including a race-detector-clean run.
- The changelog records the concurrent chunk review feature under a new Unreleased section.
- The whole batch closes with a clean `make precommit` and a clean `go test -race ./pkg/...`.
</summary>

<objective>
Close out the parallel chunk review: add the named `checkBeforeChunk` seam, harden and prove the cancellation semantics (budget expiry, runner failure, parent cancellation, single salvage write, goroutine join), and finish with the changelog entry and the full precommit and race sweeps.
</objective>

<context>
Read `CLAUDE.md` and `docs/dod.md` for project conventions.

Read these files fully before writing code:
- `pkg/steps_checkout_execution.go` — the whole file. Focus on `checkoutExecutionStep` (the struct and `NewCheckoutExecutionStep`), `runChunkedReview` and its dispatch loop / collector decision tree as landed by the previous prompt in this batch, `runOneChunk`, `chunkRunResult`, `isCancellation`, and `postAndRoute`.
- `pkg/steps_checkout_execution_test.go` — the whole file, especially the `checkoutExecutionStep chunked review` Describe and the helpers added by the previous prompt (`newStepWithConfig`, `newStepWithClock`, `fakeClock`, `chunkIndexFromPrompt`, `captureGlog`).
- `pkg/export_test.go` — the existing `*ForTest` seams and their style.
- `pkg/chunk.go` — `WaveChunkDeadline`, `MergeChunkReviews`.
- `pkg/soft_budget.go` — `runWithSoftBudget`, `budgetExpiredResult`.
- `pkg/salvage.go` — `writeSalvage`, `SalvageHeading`.
- `CHANGELOG.md` — the preamble and the newest released section, currently `## v0.11.9` (there is no `## Unreleased` yet).
- `docs/dod.md` — the changelog placement rule (`# Changelog` → preamble → `## Unreleased` → newest `## vX.Y.Z`).

Read these coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — `sync.WaitGroup` joins, no goroutine outliving the step, caller-owned channels
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` — context checks before each dispatch
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, fake runner, race-clean tests
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — entry format and the required `feat:` prefix
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — what the sweep runs and the lint limits
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`

Verified contracts (do not re-derive):
- The previous prompt in this batch landed the bounded fan-out in `runChunkedReview`: `chunkRunResult`, the semaphore, the `sync.WaitGroup` join, the collector decision tree, `isCancellation`, and the three log lines. If `chunkRunResult` or `fan-out start concurrency=` is absent when this prompt runs, report `status: failed` — do not re-implement the fan-out here.
- The first prompt in this batch landed `ReviewChunkConfig.Concurrency` (read from `s.chunkConfig.Concurrency`) and the pure `WaveChunkDeadline(now, outerDeadline, remaining, concurrency)` helper in `pkg/chunk.go`. Both must already exist when this prompt runs — the AC7/AC8 clock programs are computed against them. If either is absent, report `status: failed` rather than adding them here.
- `github.com/bborbe/errors` exports `Is(err, target error) bool`; `runWithSoftBudget` reports expiry only when its own run context's deadline fired (`runCtx.Err() == context.DeadlineExceeded`); a parent cancellation surfaces as `context.Canceled`.
- `libtime.CurrentDateTimeGetter` is `interface { Now() libtime.DateTime }`; `libtime.CurrentDateTimeGetterFunc` adapts a `func() libtime.DateTime`.
- `mocks.ClaudeRunnerMock` has `RunStub func(context.Context, string) (*claudelib.ClaudeResult, error)` and `RunCallCount() int`.
- The step's tests live in the external `pkg_test` package; test-only access to unexported members goes through `pkg/export_test.go` (package `pkg`).
- `.dark-factory.yaml` sets `workflow: direct` and does not set `hideGit`, so `.git` is not masked. Do not run git commands; the verification below is non-git.
</context>

<requirements>
1. **Add the pre-chunk seam to `pkg/steps_checkout_execution.go`.**
   - Add one unexported field to `checkoutExecutionStep`:
     ```go
     // checkBeforeChunkFn is the pre-chunk seam body. nil in production (the
     // placeholder returns nil); tests install a spy through
     // SetCheckBeforeChunkForTest. The mid-run PR-state check is the intended
     // production filler and needs no change to the dispatch loop.
     checkBeforeChunkFn func(ctx context.Context, md *agentlib.Markdown) *agentlib.Result
     ```
     Do NOT add a constructor parameter and do NOT change `NewCheckoutExecutionStep`'s signature — every existing call site (both entry points, the factory, and the tests) must keep compiling unchanged.
   - Add the method with this exact signature and contract:
     ```go
     // checkBeforeChunk is the single pre-chunk seam. A concurrent fan-out has no
     // "between chunks" moment, so the dispatcher calls this exactly once per
     // chunk, from its own goroutine, immediately before that chunk starts and
     // before its concurrency slot is filled. nil means continue; a non-nil Result
     // aborts the whole review with it: no further chunk starts, the in-flight
     // chunks are cancelled, and the step returns this result after every chunk
     // goroutine has exited. The body is deliberately a placeholder — the mid-run
     // PR-state check is the intended filler and needs no change to the loop.
     func (s *checkoutExecutionStep) checkBeforeChunk(
         ctx context.Context,
         md *agentlib.Markdown,
     ) *agentlib.Result {
         if s.checkBeforeChunkFn == nil {
             return nil
         }
         return s.checkBeforeChunkFn(ctx, md)
     }
     ```
   - In the dispatch loop of `runChunkedReview`, after the `chunkCtx.Err()` check and after the deadline/duration computation, and BEFORE acquiring the semaphore slot, call it exactly once:
     ```go
     if early := s.checkBeforeChunk(chunkCtx, md); early != nil {
         checkResult = early
         trigger()
         break
     }
     ```
     where `checkResult *agentlib.Result` is a dispatcher-local variable and `trigger()` is the `sync.Once`-guarded cancel the chunk goroutines already call — use whatever name the previous prompt landed (if it inlined `cancelOnce.Do(cancel)`, write that). After `wg.Wait()`, return `checkResult, nil` FIRST — before the existing collector decision tree — when it is non-nil. This preserves the contract "aborts with that result" regardless of what the in-flight chunks reported.
   - The call happens outside the concurrent region: it runs on the dispatching goroutine, not in a chunk goroutine.

2. **Add the test seam to `pkg/export_test.go`** (package `pkg`, external `_test` callers):
   ```go
   // SetCheckBeforeChunkForTest installs fn as the step's pre-chunk check so the
   // dispatcher's call count, ordering, and abort behaviour are testable without
   // exposing the field to production callers.
   func SetCheckBeforeChunkForTest(
       step agentlib.Step,
       fn func(ctx context.Context, md *agentlib.Markdown) *agentlib.Result,
   ) {
       if s, ok := step.(*checkoutExecutionStep); ok {
           s.checkBeforeChunkFn = fn
       }
   }
   ```

3. **Harden the cancellation semantics** if the previous prompt's collector differs from this contract (fix only what differs; do not restructure the fan-out):
   - Terminal outcomes cancel the shared chunk context: budget expiry, a non-cancellation runner error, a `stepErr`, and an `earlyResult` — leave the trigger condition the previous prompt landed unchanged; do not narrow it. Cancellation errors never cancel anything and are never the step's result while any other outcome exists.
   - Budget expiry outranks a sibling's cancellation error regardless of collection order: the collector scans `results` by ascending index and applies, in order, `stepErr` → `earlyResult` → `budgetExpired` → non-cancellation `err` → cancellation `err`.
   - Salvage is written exactly once, by the collector, for the LOWEST-index budget-expired chunk, and only when no `stepErr`, no `earlyResult`, and no `checkResult` took precedence. No chunk goroutine writes `md`.
   - The parent-context path returns `Status: agentlib.AgentStatusFailed` with `Message: fmt.Sprintf("execution claude run failed: %v", err)` using the lowest-index cancellation error — for a parent cancel that renders exactly `execution claude run failed: context canceled`.
   - No chunk starts after `chunkCtx.Err() != nil`; the dispatch loop re-checks the context after the semaphore acquire as well as before it.

4. **Add the tests to `pkg/steps_checkout_execution_test.go`.** Reuse the seven-chunk fixture (seven files, 301 additions each), `newStepWithConfig`, `newStepWithClock`, `fakeClock`, `chunkIndexFromPrompt`, `captureGlog`, and `approveChunkOutput` from the previous prompt. All test-side shared state written from a `RunStub` or from the seam must be mutex- or atomic-guarded; `go test -race ./pkg/...` must be clean.
   - **Seam — called once per dispatched chunk, in chunk order, before that chunk starts.** 5 chunks, `cfg.Concurrency = 2`, budget 25m. Install a seam that, on entry, sets an `inCall` flag and records a re-entry violation if it was already set, increments a `checks` counter (giving the ordinal k), records `startedAtCheck := atomic.LoadInt32(&started)` (the number of `Run` calls that have begun), then clears `inCall` and returns nil. The `RunStub` increments `started` on entry. Assert: `checks == 5`; no re-entry violation (the seam is never called concurrently); and for every check ordinal k, `startedAtCheck <= k-1` (the check for chunk k always precedes chunk k's own run). Assert `RunCallCount() == 5`.
   - **Seam — a non-nil return aborts the remaining chunks.** Same fixture. The seam returns `&agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "human_review", Message: "pr state changed"}` on its 3rd call and nil before. The `RunStub` blocks on `<-ctx.Done()` and returns `nil, ctx.Err()`. Assert the step returns exactly that result (status, next phase, message), `RunCallCount() == 2` (chunks 1 and 2 started; chunk 3 and later never did), and both in-flight stubs observed `ctx.Done()`.
   - **AC7 — budget expiry stops everything and wins over sibling cancellations.** Add a scripted clock helper:
     ```go
     // scriptedClock returns the programmed times in call order, repeating the
     // last one once exhausted. Mutex-guarded for race cleanliness.
     type scriptedClock struct { mu sync.Mutex; times []time.Time; i int }
     func (c *scriptedClock) Now() libtime.DateTime
     ```
     5 chunks, `cfg.Concurrency = 3`, budget 25m. The dispatcher's first five clock reads are: (1) `outerDeadline`, (2) wall-span start, (3) chunk 1's deadline, (4) chunk 2's deadline, (5) chunk 3's deadline. Program `[t0, t0, t0, t0+25m-5ms, t0]`; the scripted clock repeats its last value for any later read (the dispatcher computes chunk 4's deadline before it blocks on the full semaphore, which is harmless — chunk 4 never starts because the context is cancelled first). Chunk 1 and chunk 3 stubs block on `<-ctx.Done()` and return `nil, ctx.Err()`; chunk 2's stub blocks on `<-ctx.Done()` and returns `&claudelib.ClaudeResult{Partial: "partial chunk 2"}, ctx.Err()` (its run context times out after ~5ms). Assert: the step returns `Status: done`, `NextPhase: "human_review"`, a message containing `soft time budget` (NOT a failed status); `RunCallCount() == 3` (chunks 4 and 5 never start); `## Review` is absent; and the captured glog output contains `execution: chunk 2/5 soft time budget exceeded nextPhase=human_review`.
   - **AC8 — salvage is written once.** Same 5-chunk fixture with `cfg.Concurrency = 3` and budget 25m, clock program `[t0, t0, t0+25m-5ms, t0+25m-5ms, t0]` so chunks 1 and 2 both expire; chunk 1 returns partial `"partial one"`, chunk 2 returns `"partial two"`, chunk 3 returns `approveChunkOutput`. Assert: the step returns the budget-expired result; the marshaled markdown contains the string `## Salvage` exactly once; the salvage body contains `Chunk 1/5` and `partial one` and does NOT contain `partial two`; `## Review` is absent. The run must be race-clean.
   - **AC9 — runner failure stops everything.** 7 chunks, `cfg.Concurrency = 3`, budget 25m. Chunk 1's stub waits until chunks 2 and 3 have both entered (count entries on a buffered channel) and then returns `errors.New("runner exploded")` — deterministic, never a bare "immediately"; chunks 2 and 3 block on `<-ctx.Done()` and set an observed flag before returning `nil, ctx.Err()`. Assert: result status `failed`, message contains `execution claude run failed: runner exploded`; `RunCallCount() == 3`; both sibling flags observed; `## Review` and `## Salvage` are absent.
   - **AC10 — external cancellation stops everything and joins.** 7 chunks, `cfg.Concurrency = 3`, budget 25m. Every stub increments `entered`, signals its start, blocks on `<-ctx.Done()`, increments `exited`, and returns `nil, ctx.Err()`. The test cancels the parent context after three chunks have entered, then awaits the step. Assert: every in-flight `Run` observed `ctx.Done()` (`entered == exited == 3` after the step returns); `RunCallCount() == 3` (no new chunk started); the step returns only after all goroutines exited (assert `entered == exited` immediately after the step returns); status `failed` and message contains `execution claude run failed: context canceled`.
   - Do not weaken or delete the tests added by the previous prompt; the AC7 row replaces any earlier row that asserted a different budget-vs-cancellation precedence.

5. **Changelog.** Create `## Unreleased` in `CHANGELOG.md` below the preamble (the line ending `...and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).`) and above `## v0.11.9`, with exactly one bullet:
   ```markdown
   - feat: review a multi-chunk PR's chunks concurrently, up to `REVIEW_CHUNK_CONCURRENCY` (default 3), so a large PR finishes inside `REVIEW_MAX_DURATION` instead of salvaging without a verdict; chunk outputs still merge in chunk order into one posted verdict, the concerns gate receives the wall-clock span, each chunk's prompt states its own budget and warns when the worktree is shared, and a budget expiry or runner failure cancels every in-flight chunk
   ```
   Do not rename, reorder, or edit any existing `## vX.Y.Z` section. Do not add further bullets.

6. **Self-check before finishing.** Re-run `<verification>` and confirm every command passes and every grep prints a line; then walk each requirement 1–5 and each spec AC 7, 8, 9, 10, 14 against the diff, and confirm the seam's doc comment states it runs on the dispatching goroutine outside the concurrent region.
</requirements>

<constraints>
- `checkBeforeChunk` is called exactly once per dispatched chunk, from the dispatching goroutine, outside the concurrent region, before the chunk's slot is filled. Do not call it from a chunk goroutine and do not call it twice for one chunk.
- The mid-run PR-state poller is out of scope: the placeholder body returns nil, and nothing in this prompt polls PR state, adds a timer, or assumes such a poller exists.
- Existing log formats stay byte-identical: `review chunk %d/%d files=%d additions=%d` and `execution: chunk %d/%d soft time budget exceeded nextPhase=human_review`. New lines must not contain `review chunk` or `execution: chunk`.
- A non-budget runner error keeps today's failed/controller-retry path: status failed, no salvage, no review, no retry inside the step.
- `MergeChunkReviews`, `ChunkDeadline`, `WaveChunkDeadline`, `FilterFindingsByBasenames`, `DemotesUnverifiedConcerns`, `postAndRoute`, and the execution tool allowlist are unchanged.
- The outer deadline (`executionStart + REVIEW_MAX_DURATION`) remains the hard stop for the whole review.
- `## Salvage` is written at most once, by the collector; `## Review` only after every chunk goroutine has exited.
- Errors use `github.com/bborbe/errors`; never `fmt.Errorf`, never `context.Background()` in package code.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run from the repo root, in this order:

```bash
make test
go test -race ./pkg/...
make precommit
grep -n 'func (s \*checkoutExecutionStep) checkBeforeChunk' pkg/steps_checkout_execution.go
grep -n 'REVIEW_CHUNK_CONCURRENCY' main.go cmd/run-task/main.go
grep -n 'REVIEW_CHUNK_CONCURRENCY' README.md
grep -n 'fan-out chunk %d/%d finished elapsed=' pkg/steps_checkout_execution.go
grep -n 'fan-out start concurrency=%d chunks=%d' pkg/steps_checkout_execution.go
```

Every command must exit 0. `go test -race ./pkg/...` must report no data race. `make precommit` must exit 0 — if a single target fails (for example `make lint`, `make gosec`, or `make license`), fix it and re-run ONLY that target until it passes, then run the full `make precommit` once more; if the sweep fails on something pre-existing and unrelated, report `status: failed` naming the failing target rather than success. Each grep must print at least one line.
</verification>
