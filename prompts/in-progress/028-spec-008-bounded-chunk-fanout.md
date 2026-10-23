---
status: approved
spec: [008-parallel-chunk-review]
created: "2026-10-23T19:12:01Z"
queued: "2026-10-23T19:27:14Z"
---

# Bounded concurrent chunk fan-out, ordered merge, wall-span elapsed

<summary>
- A chunked review now runs up to N chunk reviews at once instead of one after another, starting chunks in chunk order and starting the next unstarted chunk whenever a slot frees.
- At N = 1 the review is sequential again, with the same order and the same results as today.
- Chunk outputs are collected by chunk index, so the merged review lists chunk sections in chunk order regardless of which chunk finished first.
- The merged review is still one body with one verdict block, posted through the unchanged posting path.
- The elapsed time handed to the concerns gate is the wall-clock span from the first chunk start to the last chunk finish, not the sum of the chunk durations — so a fast concurrent review no longer demotes an approve.
- Every chunk still logs its unchanged start line, plus one finished line carrying its elapsed time, and one fan-out start line precedes the first chunk.
- A chunk that expires or fails cancels the shared chunk context so its siblings stop and no new chunk starts; the step waits for every chunk goroutine before returning.
- The existing tests that assumed sequential chunks are updated for concurrent dispatch, including the test that asserted the summed elapsed.
</summary>

<objective>
Replace the sequential chunk loop in `runChunkedReview` with a bounded concurrent fan-out: dispatch chunks in order under a concurrency semaphore, collect outcomes by chunk index, merge in chunk order, and hand the concerns gate the wall-clock span — while keeping every existing log format and the posting path unchanged.
</objective>

<context>
Read `CLAUDE.md` and `docs/dod.md` for project conventions.

Read these files fully before writing code:
- `pkg/steps_checkout_execution.go` — the whole file. Focus on `runChunkedReview` (the sequential loop, `outerDeadline`, `totalElapsed`, `outputs`, `MergeChunkReviews`, `md.ReplaceSection`, `postAndRoute`), `runOneChunk` (deadline, start log line, findings filter, `neutralizeCodeFences`, `BuildChunkExecutionInstructions`, `runWithSoftBudget`, the budget-expiry salvage + `budgetExpiredResult` branch, the failed branch), `buildRunner`, and `postAndRoute`'s `runElapsed` parameter.
- `pkg/steps_checkout_execution_test.go` — the whole file, especially the `checkoutExecutionStep chunked review` Describe: `twoChunkFunnel`, `thirtyFileFunnel`, `chunkApproveWithUnverifiedConcern`, `approveChunkOutput`, `requestChangesChunkOutput`, `chunkHeadingCount`, `newStep`, and every multi-chunk `It`.
- `pkg/chunk.go` — `WaveChunkDeadline`, `ChunkDeadline`, `MergeChunkReviews`, `ReviewChunkConfig.Concurrency`.
- `pkg/soft_budget.go` — `runWithSoftBudget` returns `(result, err, expired, elapsed)` and detects expiry from its own run context; `budgetExpiredResult`.
- `pkg/salvage.go` — `writeSalvage(md, partial)`.
- `pkg/claude_partial.go` — `ExtractBudgetPartial(result, err)`.
- `mocks/claude-runner.go` — `ClaudeRunnerMock` with `RunStub` and `RunCallCount()`.
- `pkg/verdict.go` — `DemotesUnverifiedConcerns(reviewText string, elapsed, budget time.Duration) bool` and the 0.8 threshold.
- `pkg/prompts/execution.go` — `BuildChunkExecutionInstructions(..., chunkBudget libtime.Duration, concurrency int)` as changed by the previous prompt in this batch.

Read these coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — bounded producers, caller-owned channels, no bare `go func()` where the repo's `run` helpers fit; here the fan-out is bounded by a semaphore and joined by a `sync.WaitGroup`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` — non-blocking context checks in the dispatch loop
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md` — `V(2)` for per-item lines, exact formats preserved
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, counterfeiter mocks, `go test -race` cleanliness
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`, never `fmt.Errorf`

Verified contracts (do not re-derive):
- `claudelib.ClaudeRunner` is `interface { Run(ctx context.Context, prompt string) (*ClaudeResult, error) }`.
- **One shared runner is safe for concurrent `Run` calls.** `$GOPATH/pkg/mod/github.com/bborbe/agent@v0.113.0/claude/claude-runner.go` defines `type claudeRunner struct { config ClaudeRunnerConfig }` — the receiver holds only immutable config, and `Run` builds a fresh `*exec.Cmd` per call via `buildCommand`; no receiver field is mutated. Build the runner ONCE in `runChunkedReview` via `s.buildRunner(worktreePath)` and share it. Do NOT build one runner per chunk.
- `github.com/bborbe/errors` exports `Is(err, target error) bool`; use `errors.Is(err, context.Canceled)` and `errors.Is(err, context.DeadlineExceeded)` to classify cancellation.
- `libtime.Duration` wraps `time.Duration`; `libtime.Duration(d)` converts, `.Duration()` unwraps, and `.String()` renders units without a zero seconds part (24 minutes is `24m`, not `24m0s`).
- The previous prompt in this batch already added `ReviewChunkConfig.Concurrency`, `WaveChunkDeadline`, the concurrency clamp in `runChunkedReview`, and the `chunkDuration`/`concurrency` arguments to `BuildChunkExecutionInstructions`. If any of those is absent when this prompt runs, report `status: failed` — do not re-implement them.
- `.dark-factory.yaml` sets `workflow: direct` and does not set `hideGit`, so `.git` is not masked. Do not run git commands; the verification below is non-git.
</context>

<requirements>
1. **Introduce a per-chunk result type** in `pkg/steps_checkout_execution.go` (unexported, beside `runOneChunk`):
   ```go
   // chunkRunResult is one chunk run's outcome, collected by the dispatching
   // goroutine. It carries no markdown handle: the collector is the single writer
   // of md (## Salvage, ## Review), so no chunk goroutine can race another.
   type chunkRunResult struct {
       index         int
       output        string
       elapsed       time.Duration
       budgetExpired bool
       partial       string
       earlyResult   *agentlib.Result // pre-run terminal result (findings filter failure)
       stepErr       error            // prompt-build failure, propagated as the step error
       err           error            // runner error
   }
   ```

2. **Restructure `runOneChunk`** to be safe to call from a chunk goroutine and to write nothing shared:
   - New signature (drop `md` and `outerDeadline`; add `chunkDuration` and `concurrency`):
     ```go
     func (s *checkoutExecutionStep) runOneChunk(
         ctx context.Context,
         runner claudelib.ClaudeRunner,
         taskContent string,
         baseRef string,
         funnel FunnelResult,
         chunk ReviewChunk,
         index, count int,
         chunkDuration libtime.Duration,
         concurrency int,
     ) chunkRunResult
     ```
   - Emit the start line byte-identically as the first statement:
     ```go
     glog.V(2).Infof("review chunk %d/%d files=%d additions=%d", index+1, count, len(chunk.Files), chunk.Additions)
     ```
   - Keep the findings filter, `neutralizeCodeFences` loop, and `BuildChunkExecutionInstructions(ctx, s.claudeConfigDir, s.reviewMode, baseRef, index+1, count, neutralized, chunkFindings, chunkDuration, concurrency)` unchanged apart from the result shape:
     - findings-filter failure → `return chunkRunResult{index: index, earlyResult: &agentlib.Result{Status: agentlib.AgentStatusFailed, Message: fmt.Sprintf("execution chunk findings filter failed: %v", err)}}` (same message as today);
     - prompt-build failure → `return chunkRunResult{index: index, stepErr: errors.Wrapf(ctx, err, "build execution instructions base_ref=%s mode=%s", baseRef, s.reviewMode)}` (same wrapping as today).
   - Run the prompt through `runWithSoftBudget(ctx, runner, prompt, chunkDuration)`.
   - After the run returns, ALWAYS emit the finished line (one per chunk, every outcome):
     ```go
     glog.V(2).Infof("fan-out chunk %d/%d finished elapsed=%s", index+1, count, elapsed)
     ```
     The elapsed is the value `runWithSoftBudget` returns. The literal MUST start with `fan-out chunk %d/%d finished elapsed=` and MUST NOT contain the substrings `review chunk` or `execution: chunk`.
   - On a budget-expired run: keep the existing log line byte-identical —
     ```go
     glog.V(2).Infof("execution: chunk %d/%d soft time budget exceeded nextPhase=human_review", index+1, count)
     ```
     — but do NOT call `writeSalvage` here. Return `chunkRunResult{index: index, elapsed: elapsed, budgetExpired: true, partial: ExtractBudgetPartial(runResult, runErr), err: runErr}`.
   - On any other runner error: return `chunkRunResult{index: index, elapsed: elapsed, err: runErr}`.
   - On success: return `chunkRunResult{index: index, output: runResult.Result, elapsed: elapsed}`.

3. **Rewrite `runChunkedReview` as a bounded concurrent fan-out.** Keep the exported signature and the pre-work (cache `prURLStr` with `ExtractPRURL` BEFORE any mutation; build the runner once; marshal `taskContent` once). The body must implement exactly this contract:
   - `n := len(chunks)`; `concurrency := s.chunkConfig.Concurrency; if concurrency < 1 { concurrency = 1 }`.
   - `outerDeadline := s.currentDateTime.Now().Time().Add(s.maxDuration.Duration())` and then `startedAt := s.currentDateTime.Now().Time()` (the wall-span start, read before the first chunk is dispatched).
   - Emit `glog.V(2).Infof("fan-out start concurrency=%d chunks=%d", concurrency, n)` once, before the dispatch loop. This literal MUST NOT contain `review chunk` or `execution: chunk`.
   - Create `chunkCtx, cancel := context.WithCancel(ctx)` and `defer cancel()`. All chunk runs receive `chunkCtx`.
   - Collect into `results := make([]*chunkRunResult, n)` guarded by a `sync.Mutex`, with a `sync.WaitGroup` for the join, and a `sync.Once`-guarded `cancel()` trigger.
   - Dispatch loop, index `i` from 0 to n-1 in order:
     1. `if chunkCtx.Err() != nil { break }` — no chunk starts after the shared context is cancelled.
     2. `now := s.currentDateTime.Now().Time()`; `deadline := WaveChunkDeadline(now, outerDeadline, n-i, concurrency)`; `chunkDuration := libtime.Duration(deadline.Sub(now))`. Exactly ONE clock read per dispatched chunk.
     3. Acquire a slot from `sem := make(chan struct{}, concurrency)`:
        ```go
        select {
        case sem <- struct{}{}:
        case <-chunkCtx.Done():
        }
        if chunkCtx.Err() != nil {
            break
        }
        ```
     4. `wg.Add(1)`; start a goroutine that `defer wg.Done()`, `defer func() { <-sem }()`, calls `runOneChunk(chunkCtx, runner, taskContent, baseRef, funnel, chunks[i], i, n, chunkDuration, concurrency)`, records it into `results[i]` under the mutex, and calls the cancel trigger when the result is terminal (`stepErr != nil || earlyResult != nil || budgetExpired || (err != nil && !isCancellation(err))`). This trigger set is the contract — prompt 3 must not narrow it: a `stepErr` or `earlyResult` also cancels, so no further chunk starts after one.
   - Add the helper:
     ```go
     // isCancellation reports whether err is a context cancellation, i.e. the
     // error a sibling chunk returns when the shared chunk context is cancelled.
     // Such an error is never the step's result; it is reported only when no
     // chunk produced a real outcome.
     func isCancellation(err error) bool {
         return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
     }
     ```
     (`errors` is `github.com/bborbe/errors`, already imported in this file.)
   - After `wg.Wait()`, decide the outcome in this order (the first matching rule wins; all rules scan `results` by ascending index so the outcome is deterministic, and every scan skips a nil slot — a chunk that never started because the dispatcher broke out of the loop leaves `results[i] == nil`):
     1. Any `stepErr` → `return nil, that stepErr`.
     2. Any `earlyResult` → `return that earlyResult, nil`.
     3. Any `budgetExpired` → `writeSalvage(md, fmt.Sprintf("Chunk %d/%d\n\n%s", i+1, n, results[i].partial))` ONCE for the lowest such index, then `return budgetExpiredResult("execution", s.maxDuration), nil`. This is the only salvage write on the chunked path; `## Review` is not written.
     4. Any non-cancellation `err` → `return &agentlib.Result{Status: agentlib.AgentStatusFailed, Message: fmt.Sprintf("execution claude run failed: %v", err)}, nil` for the lowest such index.
     5. Any cancellation `err` (no other outcome) → the same failed result using that error, which renders today's `execution claude run failed: context canceled` for a parent cancellation.
     6. Otherwise: `outputs[i] = results[i].output` for every i, treating a nil slot as a failed step (`return &agentlib.Result{Status: agentlib.AgentStatusFailed, Message: fmt.Sprintf("execution claude run failed: chunk %d never started", i+1)}, nil`) rather than dereferencing it; then `merged, _ := MergeChunkReviews(outputs)`, `md.ReplaceSection(agentlib.Section{Heading: "## Review", Body: merged})`, compute `span := s.currentDateTime.Now().Time().Sub(startedAt)`, and `return s.postAndRoute(ctx, md, prURLStr, worktreePath, time.Time(s.currentDateTime.Now()), funnel.Ran, span)`.
   - Rewrite the `runChunkedReview` doc comment: it must no longer say the elapsed is the SUM of chunk elapsed times. State that chunks run up to `s.chunkConfig.Concurrency` at a time in chunk order, that outputs are merged in chunk order, and that the concerns gate receives the wall-clock span from the first chunk start to the last chunk finish (at N = 1 the two differ only by loop overhead).
   - No shared mutable state is written from more than one goroutine: `md` is written only by the collector after `wg.Wait()`; `results` is mutex-guarded.

4. **Update the existing multi-chunk tests in `pkg/steps_checkout_execution_test.go`** — the default config now runs 2-chunk reviews concurrently, so these assumptions change:
   - `newStep` keeps its signature (default config, real clock). Add two helpers used by the new rows:
     ```go
     newStepWithConfig(runner, poster, funnel, budget, cfg pkg.ReviewChunkConfig) agentlib.Step
     newStepWithClock(runner, poster, funnel, budget, cfg pkg.ReviewChunkConfig, clock libtime.CurrentDateTimeGetter) agentlib.Step
     ```
     Both construct `pkg.NewCheckoutExecutionStep` exactly like `newStep`, differing only in `cfg` and the clock.
   - "salvages the cut-off chunk and routes to human_review when a chunk deadline fires": both chunks now start and both expire. Change `Expect(fakeRunner.RunCallCount()).To(BeNumerically("<", 2))` to `Expect(fakeRunner.RunCallCount()).To(Equal(2))`; keep the `## Salvage` assertions (`Chunk 1/2`, the partial) and the `## Review`-absent / post-count assertions.
   - "keeps the failed path with no ## Review and no ## Salvage on a non-deadline chunk error": no assertion change; verify it still passes with both chunks running.
   - "neutralizes code fences in the chunk's file paths before they reach the prompt": the `prompts` slice is now written from two goroutines — guard every append with a `sync.Mutex`; after the run assert `prompts` has 2 entries and AT LEAST ONE contains `a[code-fence]b.go` and NONE contains the raw `a```b.go`.
   - "hands the concerns gate the SUM of the chunk runs' elapsed time": REPLACE this test with the wall-span rows from requirement 5 (case 1 and case 2 below). Do not merely delete it — its replacement must prove both the non-demoting and the demoting wall-span cases.
   - "scopes each chunk's prompt to its own files and its own funnel findings": guard the `prompts` slice with a mutex and select each prompt by its chunk index parsed from the prompt text (regexp `chunk (\d+)/`), not by slice position.
   - "merges a request-changes chunk into a posted request-changes verdict": replace the `call++` counter with prompt-derived behavior — chunk 1 returns `approveChunkOutput`, chunk 2 returns `requestChangesChunkOutput`, independent of call order.
   - "invokes the runner once per chunk…" and "merges two approving chunks…": no change.
   - Any other test-side shared state written from a `RunStub` must be mutex-guarded: `go test -race ./pkg/...` must be clean.

5. **Add the new concurrency tests.** Add these helpers to the test file:
   ```go
   // chunkIndexFromPrompt returns the 1-based chunk index named in the prompt's
   // chunk-scope preamble ("chunk 1/7").
   var chunkIndexPattern = regexp.MustCompile(`chunk (\d+)/`)

   func chunkIndexFromPrompt(prompt string) int { /* parse, return 0 on no match */ }

   // fakeClock is a mutex-guarded settable clock for the step's
   // libtime.CurrentDateTimeGetter.
   type fakeClock struct { mu sync.Mutex; now time.Time }
   func (c *fakeClock) Now() libtime.DateTime { c.mu.Lock(); defer c.mu.Unlock(); return libtime.DateTime(c.now) }
   func (c *fakeClock) Set(t time.Time)       { c.mu.Lock(); defer c.mu.Unlock(); c.now = t }
   ```
   Use a seven-chunk fixture (seven files, each 301 additions — above the 500 engage threshold and each alone above the 300 max-additions bound, so each file is its own chunk) and a three-chunk fixture (three such files) where noted. Use `approveChunkOutput` for canned outputs unless stated.

   **Test orchestration for every blocking-stub row below.** Call `step.Run(...)` in its own goroutine and deliver `(result, err)` over a channel; the test goroutine then drives the waves from `startedCh`. A synchronous `step.Run` deadlocks the moment a stub blocks, because the test goroutine never regains control. `startedCh` must be `make(chan int, n)` — every chunk has to be able to signal without a reader, or chunks beyond the buffer block on send after `close(releaseCh)` and `wg.Wait()` never returns. AC4 must close `releaseCh` (or use a non-blocking stub), since at N = 1 nothing else releases chunk 1. AC5 must have chunk 3's stub close chunk 1's release channel before returning, so no test-goroutine observation is needed.

   - **AC3 — the bound is reached and respected.** 7 chunks, `cfg.Concurrency = 3`, budget 25m. `RunStub`: parse the chunk index; under a mutex increment `active`, update `maxActive`, append the index to `started`; signal the index on `make(chan int, 7)`; block on `releaseCh`; decrement `active`; return `approveChunkOutput`. The test waits until 3 chunks are active, asserts the three started indices are exactly `{1,2,3}` and `maxActive == 3`, holds `Consistently(func() int { mu.Lock(); defer mu.Unlock(); return maxActive }, 100*time.Millisecond).Should(Equal(3))`, then `close(releaseCh)`; after the step returns assert `maxActive == 3`, `RunCallCount() == 7`, and every index 1..7 was recorded exactly once.
   - **AC4 — N = 1 is sequential.** Same 7-chunk fixture with `cfg.Concurrency = 1`: assert `maxActive == 1` and the recorded start order is exactly `[1,2,3,4,5,6,7]`. The stub must close its own `releaseCh` (or not block at all) — nothing else releases chunk 1 at N = 1.
   - **AC5 — merge order is chunk order, not completion order.** 3 chunks, `cfg.Concurrency = 3`, budget 25m. Each chunk returns `fmt.Sprintf("body chunk %d\n\n```json\n{\"verdict\":\"approve\",\"reason\":\"ok\"}\n```\n", idx)`. Chunk 3 returns immediately; chunk 1 blocks on a channel that the test closes only after chunk 3's `Run` returned; chunk 2 returns immediately. After the step returns, assert `## Review` contains `### Chunk 1/3` before `### Chunk 2/3` before `### Chunk 3/3` (compare `strings.Index` values) and that the body equals `MergeChunkReviews([]string{out1, out2, out3})` in chunk order.
   - **AC6 — the concerns gate sees wall-clock time.** 6 chunks, `cfg.Concurrency = 3`, budget 25m, `newStepWithClock` with a `fakeClock` starting at `t0`; the stub signals its chunk index on `startedCh` and blocks on its own per-index release channel, then returns `chunkApproveWithUnverifiedConcern`; the fake poster returns success.
     - Case 1: after the first three starts, `clock.Set(t0.Add(5*time.Minute))` and release chunks 1–3; after the next three starts, `clock.Set(t0.Add(10*time.Minute))` and release chunks 4–6. Wall span 10m < 20m (0.8 × 25m), so the posted verdict stays `pkg.VerdictApprove`; the summed 30m would have demoted it.
     - Case 2: release chunks 2 and 3 at `t0+1m`, chunks 4 and 5 at `t0+2m`, chunk 1 at `t0+11m`, chunk 6 at `t0+21m` (chunk 6 starts at minute 2, so the span is 21m >= 20m while max = 19m and sum/N ≈ 11.3m are both below it). Assert the posted verdict is `pkg.VerdictRequestChanges`.
     - Orchestrate each wave by waiting for the expected number of starts before setting the clock and releasing, so the test is deterministic; never rely on sleeps.
   - **AC11 — logs.** Add a helper in the test file:
     ```go
     // captureGlog redirects glog's stderr output into a pipe for the duration of
     // fn and returns everything written. glog binds -logtostderr and -v to its
     // logging struct via flag.Var/flag.BoolVar at init, so flag.Set changes them
     // at runtime; V(2) lines require -v=2. Restore both flags afterwards.
     func captureGlog(fn func()) string
     ```
     Implementation: save and restore `flag.Lookup("v")` / `flag.Lookup("logtostderr")` values; `flag.Set("logtostderr","true")`, `flag.Set("v","2")`; swap `os.Stderr` with an `os.Pipe` writer; call `fn()`; `glog.Flush()`; close the writer; `io.ReadAll` the reader; restore `os.Stderr`. All chunk goroutines have exited when the step returns, so the capture is complete.
     Row: 3 chunks, `cfg.Concurrency = 3`, all approve. In the captured output assert exactly one `fan-out start concurrency=3 chunks=3` line; exactly three lines matching `review chunk \d+/3 files=\d+ additions=\d+`; exactly three matches of `regexp.MustCompile(`fan-out chunk \d+/3 finished elapsed=\S+`)`; and no captured line contains both `fan-out` and either `review chunk` or `execution: chunk`.

6. **Do not change** `MergeChunkReviews`, `ChunkDeadline`, `WaveChunkDeadline`, `FilterFindingsByBasenames`, `DemotesUnverifiedConcerns`, `postAndRoute`, `runUnscopedReview`, the posting path, or the execution tool allowlist.
</requirements>

<constraints>
- Existing log formats stay byte-identical: `review chunk %d/%d files=%d additions=%d` and `execution: chunk %d/%d soft time budget exceeded nextPhase=human_review`. The measurement parser keys on them. New lines must not contain `review chunk` or `execution: chunk`.
- The merged `## Review` keeps today's shape: chunk sections in chunk order, exactly one merged verdict block, posted once through `postAndRoute`.
- A 429 or any non-cancellation runner error is treated as a runner failure exactly as today (failed status, no salvage, no review).
- `## Review` is written only after every chunk has finished and only by the collector; no goroutine outlives the step.
- `## Salvage` is written at most once, by the collector, for the lowest-index budget-expired chunk. No chunk goroutine writes `md`.
- The outer deadline (`executionStart + REVIEW_MAX_DURATION`) remains the hard stop for the whole review.
- Errors use `github.com/bborbe/errors`; never `fmt.Errorf`, never `context.Background()` in package code.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass, and `go test -race ./pkg/...` must be clean (guard every test-side shared variable written from a `RunStub`).
</constraints>

<verification>
Run from the repo root:

```bash
make precommit
make test
go test -race ./pkg/...
grep -n 'fan-out chunk %d/%d finished elapsed=' pkg/steps_checkout_execution.go
grep -n 'fan-out start concurrency=%d chunks=%d' pkg/steps_checkout_execution.go
grep -n 'review chunk %d/%d files=%d additions=%d' pkg/steps_checkout_execution.go
grep -n 'execution: chunk %d/%d soft time budget exceeded nextPhase=human_review' pkg/steps_checkout_execution.go
```

All commands must exit 0. `go test -race ./pkg/...` must report no data race. Each grep must print at least one line; the two pre-existing formats must still be present exactly as shown.
</verification>
