---
status: verifying
approved: "2026-10-23T19:04:06Z"
generating: "2026-10-23T19:06:12Z"
prompted: "2026-10-23T19:21:30Z"
verifying: "2026-10-23T20:10:52Z"
branch: dark-factory/parallel-chunk-review
---

## Summary

- A PR that splits into several review chunks has those chunks reviewed concurrently, at most N at a time, instead of one after another.
- N is a config value (default 3) shared by both entry points; N = 1 keeps today's deadlines, merge, routing and verdicts.
- The posted review is still one verdict: chunk outputs are merged in chunk order by the existing merge, regardless of which chunk finished first.
- A chunk that runs out of time, fails, or is cancelled stops the whole review the same way it does today — and stops every other chunk still running.
- Large PRs (7–9 chunks) that today hit the 25-minute review cap and end with no verdict finish inside it.

## Problem

The review LLM call is 83 % of a full review's wall time, and the chunks of a multi-chunk PR run strictly one after another at 2.5–6 min each (`runChunkedReview`, `pkg/steps_checkout_execution.go`, introduced by `specs/completed/007-chunked-review-for-oversized-prs.md`). A 9-chunk PR needs ~30 min, so it hits the 25-minute `REVIEW_MAX_DURATION` soft budget (`specs/in-progress/002-pr-reviewer-soft-time-budget-and-salvage.md`), salvages, and ends at `human_review` with no verdict. Measured 2026-10-23 on the sun agent cluster: 2 of 11 full reviews (18 %) did exactly this, both on `agent-task-executor#135` (source: the operator's vault analysis "Analyse Where sun's PR-Review Jobs Spend Their Time and List Speedups"). The chunks are independent reviews of disjoint file sets — nothing forces them to run in sequence.

## Goal

A multi-chunk review runs up to N chunk reviews at once, where N comes from config. Every chunk still gets its own scoped prompt and findings filter, the merged `## Review` has the same shape as a sequential run's (chunk sections in chunk order, one merged verdict block), and the existing stop conditions — soft time budget, runner failure, context cancellation — end the review with the same result as before while also stopping the chunks running alongside the one that tripped. At N = 1, deadlines, start order, merge, routing and the verdict are those of today's code; the only differences at any N are two added log lines and the chunk prompt stating the chunk's own budget instead of the whole review's.

## Non-goals

- Changing chunk size or the chunking algorithm (`MaxAdditions` / `MaxFiles` / `EngageAdditions`).
- Changing the review model, the planning phase, or the ai_review phase.
- Raising `REVIEW_MAX_DURATION`.
- Mid-run PR-state polling — owned by a separate change; this spec only requires that a cancelled context stops every in-flight chunk (see Constraints).
- Parallelising the single-chunk (unscoped) path.
- Salvaging the outputs of chunks that completed before a budget expiry — today they are discarded and stay discarded.
- Narrowing the execution tool allowlist.

## Acceptance Criteria

- [ ] AC1: `ReviewChunkConfig` gains `Concurrency int`; `ValidateReviewChunkConfig` rejects `Concurrency < 1` with an error containing `REVIEW_CHUNK_CONCURRENCY` — evidence: unit test asserts `0` and `-1` error with that substring, `1` and `3` return nil; a `DefaultReviewChunkConcurrency = 3` constant exists and `DefaultReviewChunkConfig().Concurrency == 3`, so existing tests built on the default config run at N = 3, never N = 0.
- [ ] AC2: Both entry points read and pass N — evidence: `grep -n 'REVIEW_CHUNK_CONCURRENCY' main.go cmd/run-task/main.go` returns ≥1 line in **each** file, each with `default:"3"`, and both files set `Concurrency:` in every `ReviewChunkConfig{…}` literal they build.
- [ ] AC3: The bound is reached and respected — evidence: unit test drives `runChunkedReview` with 7 chunks and N = 3 using a fake runner that blocks each `Run` on a gate until 3 are active, then releases them; it asserts the observed maximum of concurrently active `Run` calls is exactly 3 (a sequential implementation fails), chunks 1, 2, 3 are the first three dispatched, and all 7 chunks ran exactly once.
- [ ] AC4: N = 1 is sequential — evidence: the same test with N = 1 observes a maximum of exactly 1 and dispatch order 1..7.
- [ ] AC5: Merge order is chunk order, not completion order — evidence: unit test whose fake runner finishes chunk 3 before chunk 1 asserts the `## Review` body contains `### Chunk 1/3` before `### Chunk 2/3` before `### Chunk 3/3`, and equals `MergeChunkReviews` applied to the outputs in chunk order.
- [ ] AC6: The concerns gate sees wall-clock time, not summed time — evidence: unit test with N = 3, 6 chunks each taking 5 min of fake-clock time (wall span 10 min), budget 25 min, and a chunk output carrying a `not-verified` concern on an `approve` verdict asserts the posted verdict stays `approve` (the summed 30 min would have demoted it); a second case with the same concern, N = 3 and uneven durations `[11, 1, 1, 1, 1, 19]` min (chunk 6 starts at minute 2, so wall span = 21 min ≥ 0.8 × 25, while max = 19 and sum/N ≈ 11.3 are both below 20) asserts the approve is demoted.
- [ ] AC7: Budget expiry stops everything and wins over sibling cancellations — evidence: using a sequenced fake clock so the dispatcher gives chunk 2 a deadline milliseconds away and its siblings seconds away, a unit test where chunk 2 of 5 (N = 3) exceeds its deadline while its fake siblings return a context-cancelled error *before* chunk 2's result is collected; it asserts the step returns the existing budget-expired result (`human_review`, not `failed`), no unstarted chunk starts afterwards, the log contains `execution: chunk 2/5 soft time budget exceeded nextPhase=human_review`, and `## Review` is not written.
- [ ] AC8: Salvage is written once — evidence: with the same sequenced fake clock, a unit test where chunks 1 and 2 of the same wave both exceed their deadline asserts `## Salvage` appears exactly once (carrying the lower-indexed expiring chunk's partial) and `go test -race` reports no race.
- [ ] AC9: Runner failure stops everything — evidence: unit test where chunk 1's runner returns a non-budget, non-cancellation error asserts status `failed`, every other in-flight chunk's context is cancelled, and no further chunk starts.
- [ ] AC10: External cancellation stops everything — evidence: unit test cancels the parent context while 3 chunks are in flight; it asserts every in-flight `Run` observes `ctx.Done()`, no new chunk starts, and the step returns only after all chunk goroutines have exited (WaitGroup-backed fake counts entries and exits equal), with today's result for a cancelled run: status `failed`, message `execution claude run failed: context canceled`.
- [ ] AC11: Logs — evidence: unit test captures the unchanged `review chunk i/N files=… additions=…` start line once per chunk, one `fan-out chunk i/N finished elapsed=<duration>` line per completed chunk, and one `fan-out start concurrency=<N> chunks=<count>` line before the first chunk; no new line contains `review chunk` or `execution: chunk` (the substrings the measurement parser and spec 007's checks key on).
- [ ] AC12: Deadlines — evidence: table test on the wave-deadline helper: for N = 1 it returns exactly `ChunkDeadline(now, outer, remaining)`; for N > 1 it returns `ChunkDeadline(now, outer, ceil(remaining/N))`, `remaining` = chunks not yet started including this one.
- [ ] AC13: Chunk prompt budget and shared-worktree notice — evidence: unit test asserts the instructions for a chunk state the chunk's own deadline duration (not `REVIEW_MAX_DURATION`), and, when N > 1, contain a notice that the worktree is shared with concurrently running chunk reviews and that `git fetch`, `git worktree`, `git branch` and `rm -rf` must not be run; at N = 1 the notice is absent.
- [ ] AC14: `make precommit` exits 0, and `go test -race ./pkg/...` exits 0.
- [ ] AC15: **Post-Deploy (Rung-3):** on the sun agent cluster, a ≥7-chunk review completes inside the budget with chunks running concurrently within the bound — evidence: in that reviewer pod's log, the `review chunk i/N` start lines and `fan-out chunk i/N finished` lines show ≥2 chunk windows overlapping and never more than the configured N open at once, the first start line to the last finish line spans < 25 min, there is no `soft time budget exceeded` line, and the review is posted on the PR — the budget-expiry path writes `## Review` salvage and posts nothing, so a posted review is the positive signal that the fan-out completed. Subject: the first naturally occurring ≥7-chunk review, or the `agent-task-executor#135` replay in `bborbe/pr-review-fixtures` triggered with `/github-pr-review-trigger`. (Pods run in namespace `sun-agents` as of 2026-10-23; the fleet moved off `a2a`.)
  - `deploy_check:` `grep -A4 'name: github-pr-review-agent' ~/Documents/workspaces/sun/agent/values.yaml | grep -E '^\s+tag:' | awk '{print $2}'`
  - `deploy_target:` `$(git describe --tags --abbrev=0 origin/master)`

Rung-2 (dev) has no behavioural AC: the dev watcher's allowlist admits only `bborbe/go-skeleton`, which produces no multi-chunk review.

## Verification

### Container-executable

- `make precommit`
- `go test -race ./pkg/...`
- `grep -n 'REVIEW_CHUNK_CONCURRENCY' main.go cmd/run-task/main.go`
- `grep -n 'fan-out chunk %d/%d finished elapsed=' pkg/steps_checkout_execution.go`
- `grep -n 'REVIEW_CHUNK_CONCURRENCY' README.md`

### Operator-executable

- Release and publish `docker.io/bborbe/github-pr-review-agent:<tag>` (`make buca` from a worktree at the tag — the release does not publish an image).
- Deploy to sun: bump `tag:` on the `github-pr-review-agent` entry in `~/Documents/workspaces/sun/agent/values.yaml`, then `cd ~/Documents/workspaces/sun/agent && make apply` (release `agent-sun`, namespace `sun-agents`, kubeconfig `~/.kube/sun`; sun pulls images from `docker.io` directly, no mirror).
- Deploy to nuke **only after AC15 passes on sun** — the nuke reviewer is production and no AC covers it; sun is the verification target. Then bump `agent.tag` in `~/Documents/workspaces/nuke/github-pr-reviewer/values-{dev,prod}.yaml` and `AGENT_TAG_{dev,prod}` in its `Makefile`, then `BRANCH=dev make mirror && BRANCH=dev make apply`, and the prod apply as the operator's own keystroke.
- AC15's `deploy_check:` prints the new tag.
- `kubectlsun -n sun-agents logs <pr-reviewer-agent-pod> --timestamps | grep -E 'review chunk|fan-out|soft time budget|NextPhase'` on a ≥7-chunk review — overlap, bound and span per AC15.

## Desired Behavior

1. Both entry points (`main.go`, `cmd/run-task/main.go`) read `REVIEW_CHUNK_CONCURRENCY` (default 3) into `ReviewChunkConfig.Concurrency`; `ValidateReviewChunkConfig` enforces N ≥ 1 at startup.
2. A chunked review dispatches chunks in chunk order, keeping at most N chunk reviews running at once; a slot freed by a finished chunk starts the next unstarted chunk.
3. Each chunk gets a deadline from the existing `ChunkDeadline`, with the remaining-chunk count replaced by the number of remaining waves (`ceil(unstarted/N)`); the chunk's prompt states that chunk's own budget and, when N > 1, tells the model the worktree is shared and which git / `rm -rf` commands not to run.
4. Chunk outputs are collected by chunk index and passed to `MergeChunkReviews` in chunk order once every chunk has finished; the merged body is written to `## Review` and posted exactly as today.
5. The elapsed time passed to the post step — and through it to `DemotesUnverifiedConcerns` — is the wall-clock span from the first chunk's start to the last chunk's finish, not the sum of chunk durations. At N = 1 the two differ only by loop overhead.
6. The first chunk to hit its soft time budget, or to fail with a non-cancellation error, cancels the shared chunk context; no further chunk starts; the step waits for every running chunk to return, then returns what today's code returns for that chunk (budget expiry → salvage + `human_review`; failure → `failed`). Errors caused by that cancellation are never reported as the step's result. Salvage is written by one goroutine, once.
7. Cancelling the parent context stops every running chunk and prevents new ones; the step returns only after all chunk goroutines have exited.
8. Each chunk logs its unchanged start line and a `fan-out chunk i/N finished elapsed=…` line; one `fan-out start concurrency=N chunks=M` line precedes the first chunk. Chunk deadlines are computed by the dispatching goroutine, in chunk order, from the injected clock.
9. Before each chunk is started the dispatcher calls `checkBeforeChunk(ctx, md)` once, from its own goroutine, outside the concurrent region. A `nil` return continues; a non-nil `*agentlib.Result` aborts the review with it (no further chunk starts, in-flight chunks cancelled, step returns after they exit). The placeholder body returns `nil`; the mid-run PR-state check is the intended filler and needs no loop change.

## Constraints

- Existing log formats stay byte-identical: `review chunk %d/%d files=%d additions=%d` and `execution: chunk %d/%d soft time budget exceeded nextPhase=human_review` — the measurement parser keys on them. New lines must not contain `review chunk` or `execution: chunk`.
- Spec 007's rule that the concerns gate receives the **sum** of chunk elapsed times (doc comment above `runChunkedReview`; test "hands the concerns gate the SUM…" in `pkg/steps_checkout_execution_test.go`) is intentionally replaced by the wall-clock span; that test and comment are rewritten to the new rule, not deleted.
- `README.md`'s environment table gains a `REVIEW_CHUNK_CONCURRENCY` row (default `3`, minimum `1`, "lower it when the model router queues").
- `MergeChunkReviews`, `ChunkDeadline`, `FilterFindingsByBasenames`, `DemotesUnverifiedConcerns` keep their signatures and semantics; the wave count and wall span are computed by the caller.
- The outer deadline (`executionStart + REVIEW_MAX_DURATION`) remains the hard stop for the whole review.
- No shared mutable state is written from more than one goroutine: `md` (salvage, `## Review`), the outputs slice, and timing are written under a mutex or by the collecting goroutine only.
- The Claude runner is either shown safe for concurrent `Run` calls (the prompt cites the implementation that makes it so) or one runner is built per chunk — agent decides at impl time.
- Any context cancellation — budget expiry, runner failure, the parent, or a future mid-run PR-state poller — stops every in-flight chunk and starts no new one. That poller is a separate change; this spec must not assume it exists and must not block it.
- **The pre-chunk seam is named and left open.** A concurrent fan-out destroys the meaning of "between chunks", so the dispatcher exposes exactly one call site for a pre-chunk state check: a method on the step, `checkBeforeChunk(ctx context.Context, md *agentlib.Markdown) *agentlib.Result`, called once per chunk from the dispatching goroutine immediately before that chunk starts, outside the concurrent region and before its slot is filled. It returns `nil` to continue; a non-nil result aborts the whole review with that result (no further chunk starts, in-flight chunks are cancelled, the step waits for them and returns it). In this spec the body is a `return nil` placeholder — the mid-run PR-state change fills it in and needs no change to the loop. The seam is tested: a unit test asserts it is called once per dispatched chunk, in chunk order, from the dispatching goroutine, and that a non-nil return aborts the remaining chunks.
- The execution tool allowlist (`pkg/factory/factory.go`) is unchanged.
- The router is shared: sun's reviewer fleet runs `concurrency: 8` Jobs against a provider cap of 6 concurrent requests, so at N = 3 the fleet can offer up to 24 requests and the router queues the excess. Queue wait counts against each chunk's deadline — the default N is a starting value to tune from the SC5 measurement, not a claim that 3 × 8 fits the cap.
- Tests use Ginkgo v2 / Gomega in external `_test` packages and counterfeiter fakes, per `CLAUDE.md`; errors via `github.com/bborbe/errors`; time via an injected clock.
- `CHANGELOG.md` gains a `## Unreleased` section (the head is currently `## v0.11.9`) with one bullet.

## Failure Modes

| Trigger | Expected behavior | Detection | Recovery | Concurrency |
|---|---|---|---|---|
| One chunk exceeds its deadline while others run | Shared context cancelled, others stop, salvage of the expiring chunk, `human_review` | `soft time budget exceeded` log line | Operator re-triggers the review | Lowest-index expiring chunk salvaged, once, after all chunks return |
| Runner returns a non-budget error | Others cancelled, step returns `failed` | `execution claude run failed` message | Re-trigger | First non-cancellation error wins |
| Router queues requests above its provider cap (sun `seibert-api`: 6 concurrent) | Chunks wait in the router; queue time counts against chunk deadlines | Rising `finished elapsed=` durations at unchanged chunk size; router `ccrouter_inflight_requests` above dispatched | Lower `REVIEW_CHUNK_CONCURRENCY` | Several reviews share the cap |
| Router or upstream returns 429 | Treated as runner failure (today's behaviour) | `429` in pod log | Lower N; check the router cap | — |
| Two chunks contend on git lock files or one runs `rm -rf` on a shared path | Model sees a tool error and continues; an `rm -rf` on a shared path degrades every concurrent chunk and, if it hits the worktree root, the post step that reads it | Tool error text in the chunk transcripts; post step failing on a missing worktree path | Prompt notice (AC13) forbids the commands when N > 1; set N = 1 if seen | Shared worktree |
| N concurrent Claude CLI processes exceed the pod's memory limit | Pod `OOMKilled`; no salvage written (worse than a budget expiry) | `kubectlsun -n sun-agents get pod <pr-reviewer-agent-pod>` reason `OOMKilled` | Lower N or raise the Job's memory limit | — |
| Parent context cancelled (pod shutdown, future PR-state poller) | Every chunk stops, step returns after goroutines exit | — | Existing re-run path | No goroutine outlives the step |
| N misconfigured (`0`, negative, non-integer) | Startup fails naming `REVIEW_CHUNK_CONCURRENCY` | Startup error | Fix the env value | — |

## Security / Abuse

No new input surface. Chunk file paths remain PR-author-controlled and keep going through `neutralizeCodeFences` per chunk. The tool allowlist is unchanged, so a malicious PR gains no new capability from concurrency.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | `Concurrency` in `ReviewChunkConfig` + validation + both entry points; wave-deadline helper; chunk prompt budget + shared-worktree notice | 1, 3 | 1, 2, 12, 13 | — |
| 2 | Bounded concurrent dispatch, index-ordered collection and merge, wall-span elapsed, fan-out/finished logs | 2, 4, 5, 8 | 3, 4, 5, 6, 11, 14 | prompt 1 |
| 3 | Shared-context cancellation: first-error-wins, budget expiry over sibling cancellations, single-writer salvage, parent cancel, goroutine join | 6, 7 | 7, 8, 9, 10, 14 | prompt 2 |

## Do-Nothing Option

Every PR that splits into ~8+ chunks keeps hitting the 25-minute budget and ends without a verdict, needing a human to read the salvage — 18 % of full reviews in the 2026-10-23 sample. The alternatives in the source analysis were rejected: bigger chunks trade review quality for speed, and a higher budget only makes the wait longer.
