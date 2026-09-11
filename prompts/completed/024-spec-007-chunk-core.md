---
status: completed
spec: [007-chunked-review-for-oversized-prs]
summary: 'Added the chunked-review core: changed-file inventory on FunnelResult, path-sorted greedy PartitionReviewChunks with sibling pairing, deterministic worst-wins MergeChunkReviews, and the three validated REVIEW_CHUNK_* env knobs wired into both entry points'
execution_id: github-pr-review-agent-exec-024-spec-007-chunk-core
dark-factory-version: dev
created: "2026-09-11T22:05:00Z"
queued: "2026-09-11T20:03:46Z"
started: "2026-09-11T20:03:48Z"
completed: "2026-09-11T20:18:00Z"
branch: dark-factory/chunked-review-for-oversized-prs
---

# Chunked review for oversized PRs — chunk core, inventory, and env knobs

<summary>
- The mechanical funnel's result now carries a changed-file inventory: every changed file with its added-line count, computed over the same resolved diff base the funnel already uses.
- A pure partition function splits a changed-file set into bounded chunks: path-sorted, greedy, ~300 added lines and ~15 files per chunk by default.
- A file is never split across chunks, and `X.go` always lands in the same chunk as its `X_test.go` sibling even when the additions bound would separate them.
- Chunking only engages above a configurable total (500 added lines by default); at or below it the partition yields exactly one chunk.
- Chunk outputs merge deterministically in Go — no extra model call — into one review body carrying one section per chunk and exactly one verdict, the worst of the chunk verdicts.
- An unparseable chunk output contributes a request-changes verdict, so a chunked review can never approve on a chunk the parser could not read.
- Three new environment variables (`REVIEW_CHUNK_ENGAGE_ADDITIONS`, `REVIEW_CHUNK_MAX_ADDITIONS`, `REVIEW_CHUNK_MAX_FILES`) carry the thresholds; each must be at least 1 and an invalid value fails startup naming the variable.
- None of the knobs can disable chunking, and a failure to compute the added-line counts skips chunking and leaves today's single unscoped review in place.
- Existing single-run reviews and every existing test are unaffected.
</summary>

<objective>
Land the pure, unit-testable core of chunked reviews — the changed-file inventory on the funnel result, the engagement gate, the path-sorted greedy partition with sibling pairing, the three validated env knobs, and the deterministic worst-wins merge — so the execution step (prompt 2) can consume a finished, tested chunk planner without re-deriving any of this logic.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Go 1.27 per `go.mod`, no `vendor/` directory, `-mod=mod`, Ginkgo/Gomega, Counterfeiter, `errors.Wrapf`, glog `V(n)`).

Read these files fully before writing code:
- `pkg/funnel.go` — the `FunnelResult` struct (fields `Ran bool`, `FindingsJSON string`, `FailDetail string`); `func (r *funnelRunner) Run(ctx context.Context, worktreePath string, baseRef string) (FunnelResult, error)`; `resolveAndCollectChangedFiles(ctx, worktreePath, baseRef string) (resolved string, files []string, detail string)`; `changedFiles(ctx, worktreePath, resolvedBase string) ([]string, error)`; `gitOutput(ctx, worktreePath string, args ...string) (string, error)`; `neutralizeCodeFences(s string) string`; and the `funnelReport` / `funnelStats` / `funnelFinding` JSON-mirror types with their `findings_count` recomputation pattern in `filterFindings`.
- `pkg/max_review_duration.go` — the exact shape to mirror for a startup validator: `const MinReviewMaxDuration = libtime.Duration(60 * time.Second)` and `func ValidateReviewMaxDuration(ctx context.Context, d libtime.Duration) error`, whose error names the env var and the offending value.
- `pkg/verdict.go` — `type Result struct { Verdict Verdict; Reason string }`, `type Verdict string`, the `VerdictApprove` / `VerdictRequestChanges` constants, `func ParseVerdict(reviewText string) Result` (fail-closed: empty/unparseable → request-changes), and `func StripJSONVerdict(reviewText string) string` (removes the fenced/bare verdict JSON block from a review body).
- `main.go` — the `application` struct (libargument `env:"..."` tags, e.g. `MaxReviewDuration libtime.Duration` with `default:"25m"`) and its `Run` method, which calls `prpkg.ValidateReviewMaxDuration(ctx, a.MaxReviewDuration)` and returns the error on failure.
- `cmd/run-task/main.go` — the same `application` struct pattern and the same `ValidateReviewMaxDuration` call in its `Run` method (it `return err` directly).
- `pkg/funnel_test.go` — the Ginkgo `Describe`/`DescribeTable` style and the `initWorktree()` git-repo helper used to assert against a real `git diff`.

Read these coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md` — public interface + private struct + `New*`, error wrapping, doc comments on exported symbols
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega suite files, `DescribeTable`/`Entry`, external test package `pkg_test`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `errors.Wrapf` / `errors.Errorf` from `github.com/bborbe/errors`, never `fmt.Errorf`
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — new-code coverage ≥80%, error paths tested

Verified contracts (do not re-derive):
- `FunnelResult` is returned by `FunnelRunner.Run`; the interface is `Run(ctx context.Context, worktreePath string, baseRef string) (FunnelResult, error)`. Adding fields to `FunnelResult` is additive and does not change the interface.
- `gitOutput` returns trimmed stdout and wraps a non-zero exit as an error (`errors.Errorf(ctx, "git %s: %s", args[0], stderr)`), so any numstat failure surfaces as a Go error from that call.
- The `pkg` test suite is `pkg_test` (external package); exported helpers are called directly, unexported helpers are re-exported in `pkg/export_test.go`. The helpers you add in this prompt are exported, so `pkg/chunk_test.go` can call them directly without touching `export_test.go`.
- `libtime.CurrentDateTimeGetter` exposes `Now() libtime.DateTime`; `libtime.DateTime.Time()` returns `time.Time`. This prompt does not need the clock.
</context>

<requirements>
1. **Create `pkg/chunk.go` (new file, package `pkg`) with the chunk core.** Every exported symbol gets a GoDoc comment that starts with its name. Define:

   ```go
   // ChangedFile is one entry of the funnel's changed-file inventory: a
   // repo-relative changed path together with its added-line count.
   type ChangedFile struct {
       Path      string
       Additions int
   }

   // ReviewChunk is one bounded review unit: the chunk's repo-relative file
   // paths (in path-sorted order) and the sum of their added lines.
   type ReviewChunk struct {
       Files     []string
       Additions int
   }

   // ReviewChunkConfig carries the three chunking thresholds read from the
   // environment. Every value is >= 1; none of them can disable chunking.
   type ReviewChunkConfig struct {
       EngageAdditions int
       MaxAdditions    int
       MaxFiles        int
   }
   ```

   and the three defaults as exported constants:

   ```go
   const (
       DefaultReviewChunkEngageAdditions = 500
       DefaultReviewChunkMaxAdditions    = 300
       DefaultReviewChunkMaxFiles        = 15
   )
   ```

   plus `func DefaultReviewChunkConfig() ReviewChunkConfig` returning those three defaults.

2. **Startup validation.** Add `func ValidateReviewChunkConfig(ctx context.Context, cfg ReviewChunkConfig) error` to `pkg/chunk.go`, mirroring `ValidateReviewMaxDuration`. It returns nil when all three values are `>= 1`; otherwise it returns an `errors.Errorf` whose message names the offending environment variable and the offending value — one message per field, e.g. `REVIEW_CHUNK_ENGAGE_ADDITIONS must be at least 1, got 0` / `REVIEW_CHUNK_MAX_ADDITIONS must be at least 1, got 0` / `REVIEW_CHUNK_MAX_FILES must be at least 1, got 0`. Returning on the first offending field is acceptable; you do not need to aggregate all three errors. The returned error MUST contain the exact env-var name of the field it rejects.

3. **The partition.** Add:

   ```go
   // PartitionReviewChunks splits the changed-file inventory into bounded
   // chunks. It returns a single chunk holding every file when the total added
   // lines are at or below cfg.EngageAdditions (chunking does not engage), and
   // otherwise partitions path-sorted greedily into chunks that respect
   // cfg.MaxAdditions and cfg.MaxFiles. A file is never split; a file and its
   // _test.go sibling are never separated. It is pure and deterministic.
   func PartitionReviewChunks(files []ChangedFile, cfg ReviewChunkConfig) []ReviewChunk
   ```

   Implementation contract (state it in the doc comment):
   - **Units first.** Group the files into units. A unit is a single file, except that a file `p/X_test.go` and the file `p/X.go` in the same directory form one unit when both are present in `files` (the non-test sibling is obtained by stripping the `_test.go` suffix and appending `.go`). A `_test.go` file whose non-test sibling is not changed is its own unit. Files whose name does not end in `.go` are each their own unit.
   - **Path-sorted.** Sort the units by their lexicographically smallest member path (for a sibling pair this is the non-test `X.go` path — ASCII `.` sorts before `_`). A unit's members are emitted in sorted order.
   - **Engagement gate.** Compute `total` = sum of all additions. If `total <= cfg.EngageAdditions`, return exactly one `ReviewChunk` holding every file (sorted) and `total` additions. (Empty input → one chunk with no files and 0 additions.)
   - **Greedy fill.** Otherwise, walk the sorted units and append each unit to the current chunk; close the current chunk and start a new one when adding the next unit would make the chunk's additions exceed `cfg.MaxAdditions` OR its file count exceed `cfg.MaxFiles`. A chunk ALWAYS accepts its first unit even when that unit alone exceeds `cfg.MaxAdditions` (a file is never split). Each `ReviewChunk.Files` is path-sorted and `ReviewChunk.Additions` is the sum of its files' additions.
   - The chunks cover the input exactly once: every input path appears in exactly one chunk, and the union of all chunk files equals the input set.

4. **The merge.** Add:

   ```go
   // MergeChunkReviews merges the per-chunk review outputs into one review body
   // and one verdict. Each chunk contributes a section labelled
   // "### Chunk <i>/<n>" followed by that chunk's body with its verdict block
   // removed; the merged body ends in exactly one fenced JSON verdict block. The
   // merged verdict is worst-wins: request-changes if any chunk parsed to
   // request-changes, approve only if every chunk parsed to approve. A chunk
   // whose output carries no parseable verdict contributes request-changes
   // (ParseVerdict fail-closes).
   func MergeChunkReviews(outputs []string) (string, Result)
   ```

   Implementation contract:
   - `n := len(outputs)`. For each chunk `i` (1-based), compute `ParseVerdict(outputs[i-1])` and `StripJSONVerdict(outputs[i-1])`.
   - Build the body by joining, in order, a line `### Chunk <i>/<n>` followed by a blank line and the stripped chunk body, separated by blank lines. The stripped body of an empty chunk may be empty.
   - Compute the merged verdict: `VerdictRequestChanges` if any chunk's `ParseVerdict` returned `VerdictRequestChanges`, else `VerdictApprove`. Set `Result.Reason` to a deterministic, quote-free string: `"chunked review: all <n> chunks approved"` for approve, or `"chunked review: at least one chunk requested changes"` for request-changes.
   - Apply the existing blocking gate to each chunk's output BEFORE the fold: run the chunk through the same `ApplyBlockingGate` the posting path uses (`pkg/verdict.go`), so a chunk whose verdict block carries a `blocking: true` comment (or critical/major severity) contributes `VerdictRequestChanges` even when its parsed verdict is `approve` — the merged comment-free verdict block must not silently disable that gate.
   - Synthesize the merged verdict block with the union of the per-chunk `concerns_addressed` entries, worst disposition wins (any `not-verified` stays `not-verified`), so the downstream `HasUnverifiedConcerns` / `DemotesUnverifiedConcerns` gate still sees them on the chunked path.
   - Append exactly one fenced JSON verdict block to the end of the body, built with `fmt.Sprintf` (no `json.Marshal`, so the function returns no error):
     ` ```json\n{"verdict":"<merged>","reason":"<reason>"}\n```\n ` where `<merged>` is the literal `approve` or `request-changes`. The result of `ParseVerdict` on the merged body MUST equal the merged verdict.
   - `MergeChunkReviews` is pure and deterministic. An empty `outputs` slice is not a supported input (the caller always passes at least one chunk); do not add a branch for it.

5. **Changed-file inventory on the funnel result.** In `pkg/funnel.go`, add two additive fields to `FunnelResult` (keep the existing three unchanged):

   ```go
   // ChangedFiles is the PR's changed-file inventory (path + added lines)
   // computed against the same resolved base the changed-file scan uses. Nil
   // when the inventory could not be computed.
   ChangedFiles []ChangedFile
   // InventoryDetail is non-empty when the added-line counts could not be
   // computed; the execution step then skips chunking and runs one unscoped
   // review (today's behavior).
   InventoryDetail string
   ```

   Add a method:

   ```go
   // changedFileInventory returns one ChangedFile per entry of files (the
   // funnel's changed-file list) carrying the added-line count from
   // `git diff --numstat <resolvedBase>...HEAD`. Binary entries (`-`) count 0;
   // a path absent from the numstat output counts 0 (including a rename entry
   // whose `old => new` form does not match the changed-file path — the fallback
   // only defers chunking). A non-empty detail means
   // the numstat call failed and the caller must skip chunking (the funnel
   // result itself stays Ran=true — the review still runs, just unscoped).
   func (r *funnelRunner) changedFileInventory(ctx context.Context, worktreePath string, resolvedBase string, files []string) ([]ChangedFile, string)
   ```

   Implementation: call `r.gitOutput(ctx, worktreePath, "diff", "--numstat", resolvedBase+"...HEAD")`; on error return `(nil, "could not compute added-line counts for base_ref <resolvedBase>: "+err.Error())`. Parse each non-empty line by splitting on `"\t"`: require at least 3 fields; the first field is additions (`-` → 0, otherwise `strconv.Atoi`; a parse failure for a non-`-` field is treated as 0), and the path is the remaining fields joined with `"\t"`. Build a `map[string]int` of path→additions, then return one `ChangedFile{Path: f, Additions: additions[f]}` per `f` in `files` (preserving the `files` order). Never construct a shell command from a file name — this is pure string work over `gitOutput`'s result.

6. **Wire the inventory into `Run`.** In `func (r *funnelRunner) Run(...)`, after `resolveAndCollectChangedFiles` returns `resolved` and `files`, compute `inventory, inventoryDetail := r.changedFileInventory(ctx, worktreePath, resolved, files)`. Include `ChangedFiles: inventory, InventoryDetail: inventoryDetail` in the `FunnelResult` returned by the **no-changed-files** branch and the **success** branch. The fail-closed branches (runner missing, non-zero tooling exit, non-JSON output, filter failure) keep `Ran: false` and need no inventory. Do NOT change the existing fail-closed semantics: an inventory failure must not set `Ran: false` and must not populate `FailDetail`.

7. **Read the three env knobs at startup in both entry points.** In `main.go` and `cmd/run-task/main.go`, add three fields to the `application` struct next to `MaxReviewDuration`, using the libargument tag style already present:

   ```go
   ReviewChunkEngageAdditions int `required:"false" arg:"review-chunk-engage-additions" env:"REVIEW_CHUNK_ENGAGE_ADDITIONS" usage:"Total reviewable added lines above which a PR is reviewed in chunks" default:"500"`
   ReviewChunkMaxAdditions    int `required:"false" arg:"review-chunk-max-additions" env:"REVIEW_CHUNK_MAX_ADDITIONS" usage:"Maximum added lines per review chunk" default:"300"`
   ReviewChunkMaxFiles        int `required:"false" arg:"review-chunk-max-files" env:"REVIEW_CHUNK_MAX_FILES" usage:"Maximum changed files per review chunk" default:"15"`
   ```

   In each `Run` method, immediately after the existing `ValidateReviewMaxDuration` call, add:

   ```go
   if err := prpkg.ValidateReviewChunkConfig(ctx, prpkg.ReviewChunkConfig{
       EngageAdditions: a.ReviewChunkEngageAdditions,
       MaxAdditions:    a.ReviewChunkMaxAdditions,
       MaxFiles:        a.ReviewChunkMaxFiles,
   }); err != nil {
       // main.go: also RecordRun(AgentStatusFailed) + RecordDuration(time.Since(start)) before returning, matching the ValidateReviewMaxDuration block.
       return err
   }
   ```

   In `main.go` mirror the surrounding `ValidateReviewMaxDuration` error handling exactly (metrics recorded, then `return err`); in `cmd/run-task/main.go` the surrounding block is a plain `return err`. Do NOT thread the config further in this prompt — prompt 2 consumes it.

8. **Unit tests in `pkg/chunk_test.go` (new, package `pkg_test`).** Add a Ginkgo suite file with a `Describe("PartitionReviewChunks")` containing a `DescribeTable` whose rows assert the exact chunk composition (a row that runs no assertion is a failed acceptance criterion). Cover at least:
   - path-sorted order (input deliberately shuffled → output chunks and within-chunk order are path-sorted);
   - a chunk closes before the additions bound is exceeded;
   - a chunk closes before the file bound is exceeded;
   - `X.go` and `X_test.go` always land in one chunk even when the additions bound would separate them;
   - a unit larger than the additions bound forms its own chunk (a file is never split);
   - every changed file appears in exactly one chunk and the union of the chunks equals the input;
   - total added lines at or below the engage threshold (500) → exactly one chunk;
   - total above the threshold → at least two chunks;
   - empty changed-file list → one chunk and no error.

   Include these two anchor rows verbatim:
   - 30 files × 20 added lines (600 total) with the default config partitions into exactly two chunks of 15 files / 300 added lines;
   - `a.go` (200) + `a_test.go` (150) + `b.go` (100) + `c.go` (100) with the default config partitions into `{a.go, a_test.go}` (350) and `{b.go, c.go}` (200).

   Add a `Describe("MergeChunkReviews")` with rows asserting: the merged body contains exactly one verdict block and parses (via `ParseVerdict`) to the expected merged verdict; all-approve inputs → `approve`; any request-changes input → `request-changes`; an unparseable chunk output → `request-changes`; an `approve` chunk whose verdict block carries a `blocking: true` comment → `request-changes` (the per-chunk blocking gate); a chunk carrying a `not-verified` concern → the merged verdict block carries it (union, worst disposition wins); the merged body contains exactly `n` lines matching `^### Chunk ` (section count). Use a valid fenced JSON verdict block (e.g. `` ```json\n{"verdict":"approve","reason":"ok"}\n``` ``) as each chunk's canned output.

   Add a `Describe("ValidateReviewChunkConfig")` with rows: the three defaults accepted; each of the three fields set to `0` rejected with an error whose message contains that field's env-var name (assert `ContainSubstring("REVIEW_CHUNK_...")`).

   Add a `Describe("review chunk env knobs")` to `main_internal_test.go`, mirroring the existing `REVIEW_MAX_DURATION` rows there (lines 30-59), so the libargument boundary is traversed, not just the Go constants: (a) `libargument.DefaultValues(ctx, &application{})` then `libargument.Fill(ctx, app, defaults)` leaves `app.ReviewChunkEngageAdditions == 500`, `app.ReviewChunkMaxAdditions == 300`, `app.ReviewChunkMaxFiles == 15` (this asserts the `default:"..."` tags, not `DefaultReviewChunkConfig`); (b) `libargument.ParseEnv(ctx, &application{}, []string{"REVIEW_CHUNK_MAX_FILES=0"})` succeeds and binds `0` into `app.ReviewChunkMaxFiles`, and `prpkg.ValidateReviewChunkConfig` on the bound values then returns an error; (c) the same parse-then-reject pair for `REVIEW_CHUNK_ENGAGE_ADDITIONS=0` and `REVIEW_CHUNK_MAX_ADDITIONS=0`. A row that runs no assertion is a failed acceptance criterion.

   Add a `Describe("FunnelRunner changed-file inventory")` (or extend `pkg/funnel_test.go`) with a row that drives the real `initWorktree()`-style repo: a changed file with N added lines yields `ChangedFiles` containing that path with `Additions == N`, and the additions are computed over the funnel's resolved base (the diff base the funnel resolved). Include a binary-entry row (a changed file with a NUL byte / binary content) asserting its additions are 0.

9. **Self-check before finishing.** Re-run `<verification>` and confirm it passes. Walk every acceptance criterion this prompt covers (spec ACs 1, 2, 4) against the change: the partition table rows assert exact composition, the three defaults and three invalid-value rejections are covered, and the merge table asserts a single verdict block plus the parsed verdict.

</requirements>

<constraints>
- The mechanical funnel contract is unchanged: `Ran` / `FindingsJSON` / `FailDetail` semantics and the findings JSON shape stay as they are; the changed-file inventory is additive.
- Below the engage threshold the execution path is unchanged: one runner invocation, the same unscoped prompt content, one verdict, one posted review.
- The verdict set stays binary (`approve`, `request-changes`). No `comment` verdict value is introduced anywhere; a chunk that emits anything else fails closed to `request-changes` through the existing parser.
- Do NOT add an opt-out or disable knob for chunking — invariant; none of the three thresholds may disable chunking.
- Do NOT split a single file across chunks; a file plus its `_test.go` sibling always share a chunk.
- Do NOT add an extra synthesis LLM pass — the merge is deterministic Go.
- Do NOT change the watcher, `MAX_ADDITIONS` / `MAX_CHANGED_FILES`, the model, `REVIEW_MODE`, or the `ai_review` phase.
- Do NOT re-implement `.reviewignore` in the agent — the watcher's exclusion logic stays watcher-side.
- Errors use `github.com/bborbe/errors` (never `fmt.Errorf`); `context.Context` is threaded through IO; exported symbols carry doc comments; no debug output.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make test` — the Ginkgo suite is green, including the new partition table, the merge table, the config-validation rows, and the inventory rows; no live Claude calls.
- `make precommit` — must exit 0 (fmt, generate, test, lint, vet, vuln, license). Keep every new function within funlen 80 lines / 50 statements, gocognit 20, nestif 4; extract helpers rather than exceeding the limits, or add a `//nolint:funlen // <reason>` only if a helper would obscure the required ordering.
- `grep -n 'REVIEW_CHUNK_ENGAGE_ADDITIONS\|REVIEW_CHUNK_MAX_ADDITIONS\|REVIEW_CHUNK_MAX_FILES' main.go cmd/run-task/main.go` — each name appears in both entry points.
- `grep -n 'ValidateReviewChunkConfig' main.go cmd/run-task/main.go pkg/chunk.go` — the validator is defined and called from both entry points.
</verification>

<!-- AUDITOR NOTES
1. This is prompt 1 of a three-prompt batch (chunk core → execution-step loop → regression sweep + precommit + changelog), matching the spec's Suggested Decomposition. It covers spec DBs 1, 2, 3, 5 and ACs 1, 2, 4.
2. Deliberate staging: the three env knobs are read and validated at startup here, but the config is NOT threaded into the execution step until prompt 2 (which also consumes it). This avoids storing an unread unexported struct field on `checkoutExecutionStep`, which staticcheck's `unused` checker would reject, and avoids an unused `unparam`-flagged constructor parameter.
3. `MergeChunkReviews` returns no error on purpose: it uses `fmt.Sprintf` to build the verdict block, so it can never fail and `unparam` will not flag an always-nil error return.
4. Open question flagged inline: the merge uses per-chunk `ParseVerdict` only, so the downstream blocking gate sees no chunk comments on a chunked review. This is the literal spec reading; changing it is a follow-up decision.
5. OPEN QUESTION for the auditor: the spec defines the merged verdict as worst-wins over the per-chunk `ParseVerdict` result. Because the merged body carries a synthesized verdict block with no `comments`, the existing Go-side blocking gate (`ApplyBlockingGate` in `pkg/verdict.go`, applied downstream in `postAndRoute`) sees no chunk comments on a chunked review. This prompt implements the literal spec (per-chunk `ParseVerdict` only). If the reviewer wants the blocking gate to keep its defense-in-depth on the chunked path, that is a separate decision (apply `ApplyBlockingGate` per chunk before the worst-wins merge) and should be a follow-up spec change, not silent scope added here.
-->
