---
status: completed
spec: [008-parallel-chunk-review]
summary: Added ReviewChunkConfig.Concurrency (default 3, validated), WaveChunkDeadline, per-chunk prompt budget plus shared-worktree notice, wired both entry points, and added tests/README docs
execution_id: github-pr-review-agent-parallel-chunks-exec-027-spec-008-chunk-concurrency-config
dark-factory-version: v0.198.7
created: "2026-10-23T19:12:00Z"
queued: "2026-10-23T19:27:14Z"
started: "2026-10-23T19:27:17Z"
completed: "2026-10-23T19:45:11Z"
---

# Chunk concurrency config, wave deadlines, and the per-chunk prompt budget

<summary>
- Chunked reviews gain a concurrency setting, default 3, that both the pod entry point and the local CLI entry point read from the environment.
- A misconfigured concurrency (zero or negative) fails startup with an error naming the environment variable, so a bad value can never silently deadlock the review.
- A chunk's deadline is now derived from the number of remaining waves of chunks rather than the number of remaining chunks, so at the default concurrency each wave gets a fair share of the review budget.
- At concurrency 1 the deadline is byte-for-byte the deadline the code computes today; nothing about a sequential review changes.
- Each chunk's prompt now states that chunk's own time budget instead of the whole review's budget.
- When more than one chunk runs at once, the chunk prompt warns that the worktree is shared and forbids the git and filesystem commands that would corrupt it.
- The new setting is documented in the README configuration table.
- A malformed value is rejected at startup by the existing chunk-config validator, now covering the fourth field.
</summary>

<objective>
Add `Concurrency` to `ReviewChunkConfig` (default 3, validated >= 1, read by both entry points), add the pure wave-deadline helper, and make the chunk prompt state the chunk's own budget plus a shared-worktree notice when concurrency is above 1 — the configuration and prompt foundation the concurrent dispatcher in the next prompt builds on.
</objective>

<context>
Read `CLAUDE.md` and `docs/dod.md` for project conventions.

Read these files fully before writing code:
- `pkg/chunk.go` — `DefaultReviewChunkEngageAdditions`, `DefaultReviewChunkMaxAdditions`, `DefaultReviewChunkMaxFiles`, `ReviewChunkConfig`, `DefaultReviewChunkConfig()`, `ValidateReviewChunkConfig(ctx, cfg)`, `minChunkSlice`, `ChunkDeadline(now, outerDeadline time.Time, remainingChunks int) time.Time`, `MergeChunkReviews`.
- `pkg/chunk_test.go` — the `ValidateReviewChunkConfig` and `ChunkDeadline` specs (the validation table rows currently build 3-field `ReviewChunkConfig` literals).
- `pkg/prompts/execution.go` — `BuildChunkExecutionInstructions`, `assembleExecutionInstructions`, `chunkScopeTemplate`, `renderChunkScope`, `timeBudgetFooter`.
- `pkg/prompts/execution_test.go` — the existing `BuildExecutionInstructions` spec and its temp-plugin fixture (`writePlugin`, `tmpDir`, `cmdDir`).
- `pkg/steps_checkout_execution.go` — `runChunkedReview` (the sequential loop and `outerDeadline`), `runOneChunk` (the `ChunkDeadline` call and the `BuildChunkExecutionInstructions` call), and the struct field `chunkConfig ReviewChunkConfig`.
- `main.go` — the `application` struct fields `ReviewChunkEngageAdditions` / `ReviewChunkMaxAdditions` / `ReviewChunkMaxFiles`, the `ValidateReviewChunkConfig` literal in `Run`, and the `ReviewChunkConfig` literal in `dispatchAgent`.
- `cmd/run-task/main.go` — the same three struct fields, the `ValidateReviewChunkConfig` literal in `Run`, and the `ReviewChunkConfig:` literal in the `factory.RunConfig{...}`.
- `main_internal_test.go` — the `validate` helper and the `review chunk env knobs` spec.
- `README.md` — the configuration table rows for `REVIEW_CHUNK_ENGAGE_ADDITIONS` / `REVIEW_CHUNK_MAX_ADDITIONS` / `REVIEW_CHUNK_MAX_FILES`.

Read these coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `errors.Errorf(ctx, ...)` from `github.com/bborbe/errors`, never `fmt.Errorf`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega, external `_test` packages
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md` — doc comments on every new exported symbol
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — coverage and error-path expectations

Verified contracts (do not re-derive):
- `libtime.Duration` is `github.com/bborbe/time.Duration`; `libtime.Duration(d).String()` renders a duration with week/day/hour/minute/second units and OMITS a zero seconds component — 24 minutes renders as `24m`, NOT `24m0s` — and `.Duration()` returns the stdlib value.
- `libtime.CurrentDateTimeGetter` is `interface { Now() libtime.DateTime }`; `libtime.DateTime.Time()` is a `time.Time`; `libtime.CurrentDateTimeGetterFunc` adapts a `func() libtime.DateTime`.
- `ChunkDeadline` floors its result at `now + minChunkSlice` (60s) and caps it at `outerDeadline`; `remainingChunks <= 0` returns `outerDeadline`.
- `ValidateReviewChunkConfig` is called at startup by BOTH `main.go` and `cmd/run-task/main.go`.
- `BuildChunkExecutionInstructions` has exactly one production caller, `runOneChunk` in `pkg/steps_checkout_execution.go`, plus three test call sites in `pkg/prompts/execution_test.go` that the signature change below must also update.
- `.dark-factory.yaml` sets `workflow: direct` and does not set `hideGit`, so `.git` is not masked in the container. Do not run git commands anyway; the verification below uses non-git checks.
</context>

<requirements>
1. **`pkg/chunk.go` — the constant and the config field.**
   - Add, beside the other `DefaultReviewChunk*` constants:
     ```go
     // DefaultReviewChunkConcurrency is the default number of chunk reviews that
     // may run at once. It is a starting value tuned from the router measurement,
     // never a claim that N reviews fit the provider's concurrency cap.
     const DefaultReviewChunkConcurrency = 3
     ```
   - Add `Concurrency int` to `ReviewChunkConfig` as the fourth field, with a doc comment on the struct explaining it is the maximum number of chunk reviews running at once and must be >= 1.
   - `DefaultReviewChunkConfig()` returns `Concurrency: DefaultReviewChunkConcurrency` in addition to the three existing thresholds.
   - Update the `ReviewChunkConfig` and `ValidateReviewChunkConfig` doc comments: they say "three" today, there are now four values.

2. **`pkg/chunk.go` — validation.** Append a fourth case to the `ValidateReviewChunkConfig` switch, **as the last case** (after `MaxFiles`), so an existing test that zeroes a different field still surfaces that field's own env var:
   ```go
   case cfg.Concurrency < 1:
       return errors.Errorf(
           ctx,
           "REVIEW_CHUNK_CONCURRENCY must be at least 1, got %d",
           cfg.Concurrency,
       )
   ```
   The error text MUST contain the literal `REVIEW_CHUNK_CONCURRENCY`.

3. **`pkg/chunk.go` — the wave-deadline helper.** Add, immediately after `ChunkDeadline`, an exported pure function with exactly this signature and body:
   ```go
   // WaveChunkDeadline returns the deadline for one chunk when up to concurrency
   // chunk reviews run at once. The remaining-chunk count is replaced by the
   // number of remaining waves, ceil(remainingChunks/concurrency), so each wave
   // is granted the same reservation ChunkDeadline grants each chunk sequentially.
   // At concurrency <= 1 it is exactly ChunkDeadline(now, outerDeadline,
   // remainingChunks). remainingChunks is the number of chunks not yet started,
   // including the one this deadline is for, and is >= 1 in practice.
   func WaveChunkDeadline(
       now, outerDeadline time.Time,
       remainingChunks, concurrency int,
   ) time.Time {
       if concurrency <= 1 {
           return ChunkDeadline(now, outerDeadline, remainingChunks)
       }
       waves := (remainingChunks + concurrency - 1) / concurrency
       return ChunkDeadline(now, outerDeadline, waves)
   }
   ```

4. **`pkg/prompts/execution.go` — per-chunk budget and the shared-worktree notice.**
   - Change `BuildChunkExecutionInstructions` to this signature (the last parameter is new, and the duration parameter is renamed):
     ```go
     func BuildChunkExecutionInstructions(
         ctx context.Context,
         claudeConfigDir claudelib.ClaudeConfigDir,
         reviewMode string,
         baseRef string,
         chunkIndex int,
         chunkCount int,
         chunkFiles []string,
         chunkFindings string,
         chunkBudget libtime.Duration,
         concurrency int,
     ) (claudelib.Instructions, error)
     ```
   - Its body renders the scope preamble, appends the shared-worktree notice when `concurrency > 1`, and passes `chunkBudget` (NOT the whole-review budget) as the budget handed to `assembleExecutionInstructions`:
     ```go
     scope := renderChunkScope(chunkIndex, chunkCount, chunkFiles)
     if concurrency > 1 {
         scope += renderSharedWorktreeNotice(concurrency)
     }
     return assembleExecutionInstructions(
         ctx, claudeConfigDir, reviewMode, baseRef, scope, steer, chunkBudget,
     )
     ```
     (`steer` is the existing `fmt.Sprintf(funnelInjectSteerTemplate, ...)` value; leave it unchanged.)
   - Add a new constant and renderer beside `chunkScopeTemplate` / `renderChunkScope`:
     ```go
     // sharedWorktreeNoticeTemplate warns a chunk review that the worktree is
     // shared with concurrently running chunk reviews and names the commands that
     // would corrupt or delete the checkout the other chunks and the post step read.
     const sharedWorktreeNoticeTemplate = "## Shared worktree\n\n" +
         "This worktree is shared with other chunk reviews running concurrently (concurrency %d). " +
         "Do NOT run `git fetch`, `git worktree`, `git branch`, or `rm -rf` — those " +
         "commands mutate or delete the checkout the other chunks and the posting " +
         "step read. Use read-only inspection only (`git diff`, `git log`, `git show`, " +
         "`git status`, `git rev-parse`, `git ls-files`).\n\n---\n\n"

     // renderSharedWorktreeNotice renders the notice naming the configured concurrency.
     func renderSharedWorktreeNotice(concurrency int) string {
         return fmt.Sprintf(sharedWorktreeNoticeTemplate, concurrency)
     }
     ```
   - Update the `BuildChunkExecutionInstructions` doc comment to state that `chunkBudget` is that chunk's own deadline duration (not `REVIEW_MAX_DURATION`) and that the shared-worktree notice is present only when `concurrency > 1`.

5. **`pkg/steps_checkout_execution.go` — wire the helper and the budget into the existing sequential loop.** This prompt does NOT change the dispatch order; the loop stays sequential.
   - In `runChunkedReview`, after `n := len(chunks)`, add:
     ```go
     concurrency := s.chunkConfig.Concurrency
     if concurrency < 1 {
         concurrency = 1
     }
     ```
     (The clamp is defense-in-depth against a zero-valued struct field: a zero bound would deadlock the dispatcher the next prompt adds. Startup validation still rejects a misconfigured env value.)
   - In `runOneChunk`, add a `concurrency int` parameter (last), and replace the two separate clock reads with ONE read:
     ```go
     now := s.currentDateTime.Now().Time()
     deadline := WaveChunkDeadline(now, outerDeadline, count-index, concurrency)
     chunkDuration := libtime.Duration(deadline.Sub(now))
     ```
     Keep the existing `glog.V(2).Infof("review chunk %d/%d files=%d additions=%d", ...)` line byte-identical.
   - In `runOneChunk`, pass `chunkDuration` and `concurrency` to the prompt builder:
     ```go
     instructions, err := prompts.BuildChunkExecutionInstructions(
         ctx,
         s.claudeConfigDir,
         s.reviewMode,
         baseRef,
         index+1,
         count,
         neutralized,
         chunkFindings,
         chunkDuration,
         concurrency,
     )
     ```
     (Today this call passes `s.maxDuration`; that is the bug this requirement fixes — a chunk prompt must state the chunk's own budget.)
   - Update the `runOneChunk` call in `runChunkedReview` to pass `concurrency`.

6. **Both entry points read and pass the value (AC2).**
   - `main.go`: add to the `application` struct, directly after `ReviewChunkMaxFiles`:
     ```go
     ReviewChunkConcurrency int `required:"false" arg:"review-chunk-concurrency" env:"REVIEW_CHUNK_CONCURRENCY" usage:"Maximum number of chunk reviews running at once; lower it when the model router queues" default:"3"`
     ```
     Add `Concurrency: a.ReviewChunkConcurrency,` to BOTH `ReviewChunkConfig{...}` literals in `main.go` — the one passed to `ValidateReviewChunkConfig` in `Run` and the one passed to `factory.CreateAgentProvider` in `dispatchAgent`.
   - `cmd/run-task/main.go`: add the same field to its `application` struct (same tags, same `default:"3"`) and add `Concurrency: a.ReviewChunkConcurrency,` to BOTH literals — the `ValidateReviewChunkConfig` one in `Run` and the `ReviewChunkConfig:` one inside `factory.RunConfig{...}`.
   - `pkg/factory/*.go` needs no change: `ReviewChunkConfig` carries the new field through `CreateAgentProvider`, `CreateAgent`, and `RunConfig` unchanged. Do not add a new factory parameter.

7. **Tests.**
   - `pkg/chunk_test.go`, `ValidateReviewChunkConfig` spec:
     - Seed `Concurrency: 1` in the three existing table rows so each row still names its own env var.
     - Add rows: `pkg.ReviewChunkConfig{EngageAdditions: 500, MaxAdditions: 300, MaxFiles: 15, Concurrency: 0}` and the same with `Concurrency: -1`, both expecting an error containing `REVIEW_CHUNK_CONCURRENCY`.
     - Add an `It` asserting `ValidateReviewChunkConfig(ctx, cfg)` returns nil for the two full configs `pkg.ReviewChunkConfig{EngageAdditions: 500, MaxAdditions: 300, MaxFiles: 15, Concurrency: 1}` and the same with `Concurrency: 3` — a bare `{Concurrency: N}` literal would fail on `EngageAdditions: 0` and prove nothing.
     - Rename the `main_internal_test.go` defaults `It` to `"resolves the four defaults when unset"` once the fourth assertion is added (see below).
     - Add an `It` asserting `pkg.DefaultReviewChunkConfig().Concurrency` equals `pkg.DefaultReviewChunkConcurrency` and that the constant is `3`.
   - `pkg/chunk_test.go`, new `Describe("WaveChunkDeadline", ...)` block using a fixed `now` and `outerDeadline`:
     - concurrency 1 returns exactly `pkg.ChunkDeadline(now, outer, remaining)` for `remaining` in {1, 3, 5, 7} — compare against the helper's result, not a hand-computed time;
     - concurrency 3 with remaining 6 → equal to `pkg.ChunkDeadline(now, outer, 2)`;
     - concurrency 3 with remaining 7 → equal to `pkg.ChunkDeadline(now, outer, 3)`;
     - concurrency 3 with remaining 2 and with remaining 1 → equal to `pkg.ChunkDeadline(now, outer, 1)`.
   - `pkg/prompts/execution_test.go` — a `Describe("BuildChunkExecutionInstructions", ...)` block **ALREADY EXISTS** at the end of this file (`:396`) with its own `tmpDir` / `cmdDir` / `fakePlugin` fixture; `writePlugin` belongs to the neighbouring `BuildExecutionInstructions` Describe and is NOT reachable from it. Do NOT add a second block — extend the existing one. **First** add the new `concurrency` argument to its three existing `BuildChunkExecutionInstructions(...)` calls (`:423`, `:452`, `:468`; they pass 9 args today and will not compile otherwise — the `libtime.Duration(25*time.Minute)` budget in each is a placeholder, not an assertion). **Then** add these two `It`s to that same block:
     - with `chunkBudget = libtime.Duration(24*time.Minute)` and `concurrency = 3`, the `workflow` instruction contains the chunk scope (`## Chunk scope`, `chunk 1/2`, the file list), the notice substrings `shared`, `concurrently`, `` `git fetch` ``, `` `git worktree` ``, `` `git branch` ``, `` `rm -rf` ``, and the budget text `24m`, and does NOT contain `25m`;
     - with `concurrency = 1`, the `workflow` instruction contains `24m` and does NOT contain `## Shared worktree` nor any of the four forbidden command strings.
   - `main_internal_test.go` (AC2 boundary): add `Concurrency: app.ReviewChunkConcurrency` to the `validate` helper's literal; assert `app.ReviewChunkConcurrency` equals `3` in the defaults test; add a fourth `Entry("concurrency", "REVIEW_CHUNK_CONCURRENCY", func(app *application) int { return app.ReviewChunkConcurrency })` row to the zero-value table.

8. **`README.md`.** Add one row to the configuration table, directly after the `REVIEW_CHUNK_MAX_FILES` row:
   ```markdown
   | `REVIEW_CHUNK_CONCURRENCY` | Maximum number of chunk reviews running at once (default `3`, must be ≥ 1); lower it when the model router queues |
   ```

9. **Changelog.** Do NOT touch `CHANGELOG.md` in this prompt; the whole-feature entry lands in the last prompt of the batch.
</requirements>

<constraints>
- Existing log formats stay byte-identical: `review chunk %d/%d files=%d additions=%d` and `execution: chunk %d/%d soft time budget exceeded nextPhase=human_review`. Do not reword, re-indent, or change the verbosity gate of either.
- `MergeChunkReviews`, `ChunkDeadline`, `FilterFindingsByBasenames`, `DemotesUnverifiedConcerns` keep their signatures and semantics. `WaveChunkDeadline` is a new caller of `ChunkDeadline`; do not modify `ChunkDeadline`.
- The execution tool allowlist in `pkg/factory/factory.go` (`executionTools`) is unchanged. Do not add or remove tools.
- Chunk file paths stay PR-author-controlled and keep going through `neutralizeCodeFences` per chunk; do not move that neutralization or change its output.
- At concurrency 1 the deadline, the start order, the merge, the routing and the verdict are those of today's code. The only prompt-content difference at any N is the budget text and, for N > 1, the notice.
- Errors use `github.com/bborbe/errors` (`errors.Errorf`, `errors.Wrapf`); never `fmt.Errorf`, never `context.Background()` in package code.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run from the repo root:

```bash
make precommit
make test
grep -n 'REVIEW_CHUNK_CONCURRENCY' main.go cmd/run-task/main.go
grep -n 'REVIEW_CHUNK_CONCURRENCY' README.md
grep -n 'func WaveChunkDeadline' pkg/chunk.go
grep -n 'chunkBudget' pkg/prompts/execution.go
```

`make precommit` must exit 0 (it runs lint, vet and the race-enabled test suite — the new concurrent code must be race-clean). The four greps must each print at least one line; the `main.go` and `cmd/run-task/main.go` matches must show `default:"3"` on the struct-field line, and each file must additionally contain a `Concurrency:` assignment in every `ReviewChunkConfig` literal it builds.
</verification>
