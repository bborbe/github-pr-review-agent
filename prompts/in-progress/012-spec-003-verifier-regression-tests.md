---
status: approved
spec: [003-bug-ai-review-verifier-false-fails-on-gh-unavailable]
created: "2026-08-27T22:44:08Z"
queued: "2026-08-28T06:11:44Z"
---

# Regression-lock the ai_review verifier inline-diff behavior

<summary>
- Ginkgo regression tests lock the new verifier behavior using the existing fake-runner pattern — no live Claude, no network
- A wiring row proves the verifier's prompt embeds the PR raw diff and the posted review comments (the preamble the fix ships)
- A clean-pass row proves a review whose cited file + line exist in the supplied inline diff passes the verifier and does not escalate to `human_review`
- A fabricated-hallucination row proves a comment citing a file/line absent from the inline diff fails the verifier and the dismissal carries the hallucination naming the fabricated comment
- A factory test proves the ai_review allowlist contains only `Read` and `Grep` — no `Bash(gh pr ...)` entry survives
- Removing the inline-diff wiring flips the wiring and clean-pass rows to fail (the regression lock the spec requires)
</summary>

<objective>
Lock the fixed ai_review verifier contract with Ginkgo tests: the diff and posted review comments reach the verifier's prompt, a sound review passes without `human_review` escalation, and a genuine hallucination still fails with the correct hallucination object — all with the fake runner, so the regression is caught at `make precommit` instead of in production.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Ginkgo v2 / Gomega, Counterfeiter mocks in `mocks/`, external `pkg_test` packages, fake-runner canned verdicts).

Read `docs/architecture.md` — "ai_review's Consistency Check" (what `pass`/`fail` mean for routing) and `docs/dod.md` (§ Testing: Ginkgo v2 / Gomega with Counterfeiter mocks, no live Claude).

Read these files fully:
- `pkg/steps_review.go` — `reviewStep.Run` (the preamble wiring built by prompt 1: `envContext` with `PR Diff` / `Posted Review Comments`), `routeFailVerdict` (the dismiss + human_review path the fabricated row exercises), and `prStateCheck` (the pre/post-flight `PRState` call the new tests must stub).
- `pkg/pr_state_check.go` — `PRStateClient` interface (now with `PRDiff`; the `PRState` call the step's `prStateCheck` makes).
- `mocks/pr-state-client.go` — the regenerated mock (`PRStateReturns`, `PRDiffReturns`, `PRDiffCallCount`) and `mocks/claude-runner.go` (`RunReturns`, `RunArgsForCall(i int) (context.Context, string)`) and `mocks/pr-poster.go` (`DismissCurrentReviewReturns`, `DismissCurrentReviewArgsForCall`).
- `pkg/steps_review_test.go` — the existing `reviewStep` and `dismiss-and-comment routing` Describe blocks (the fake-runner construction, `pkg.NewReviewStep(...)` call shape with `prState` as the last argument, and the `poster.DismissCurrentReviewArgsForCall(0)` accessor pattern at the "case (a)" row).
- `pkg/soft_budget.go` — `runWithSoftBudget` calls `runner.Run(runCtx, prompt)`; the captured prompt is the 2nd return of `runner.RunArgsForCall(0)`.
- `pkg/factory/export_test.go` — the package-private-var export pattern (`var ExecutionTools = executionTools`); add the parallel `ReviewTools` export.
- `pkg/factory/factory_test.go` — the `Describe("ExecutionTools", ...)` allowlist assertion style to mirror for `ReviewTools`.
- `pkg/github/client_test.go` — the `var _ github.Client = ...` compile-guard style (do not add gh-exec success-path tests; the ghClient shell-out is not mockable without refactoring, per the file's own NOTE).

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo integration-style tests, Counterfeiter mocks
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md` — fake/mock conventions

Verified contracts (do not re-derive):
- `agentlib "github.com/bborbe/agent"` — `func (m *Markdown) FindSection(heading string) (*Section, bool)`; `ParseMarkdown(ctx, content)` builds a markdown from a string (see the existing tests' `md, err := agentlib.ParseMarkdown(ctx, content)` pattern).
- `pkg.NewReviewStep(runner, poster, instructions, verifier, ghToken, botLogin, maxDuration, prState)` — `prState` is the last (8th) argument; pass a `*mocks.PRStateClient` for the wired rows, `nil` is not acceptable for the new rows because the preamble wiring is gated on a non-nil `prState`.
- Counterfeiter accessors on `mocks.PRStateClient` after regeneration: `PRStateReturns(state, mergedAt, headRefOid string, err error)`, `PRDiffReturns(diff string, err error)`, `PRDiffCallCount() int`, `PRDiffArgsForCall(i int) (context.Context, string)`.
- The verifier is passed as `nil` in these rows — the `VerifyReview` REST poll is covered by the existing verification-behavior tests and is out of scope here (spec: `callVerifier` untouched).
- `pkg.PRStateClient`'s `PRState` pre-flight (`pkg/pr_state_check.go`) routes: `MERGED`/`CLOSED` → `closeMoot` (`NextPhase: done`); **`OPEN` with `headRefOid != ref` (frontmatter `ref` in the task md) → superseded → `closeMoot` too** — this fires BEFORE the `default` branch, so a stub of `PRStateReturns("OPEN", "", "", nil)` against a fixture with `ref: abc123` short-circuits every row (no runner call, no diff fetch). The new rows MUST stub `headRefOid` equal to the fixture's `ref` (e.g. `PRStateReturns("OPEN", "", "abc123", nil)`) so the pre-flight reaches `default` and proceeds to the runner + diff wiring. `MERGED`/`CLOSED`/superseded all close the task early — never use them in these rows.
</context>

<requirements>

1. **Add a new top-level Describe block** `var _ = Describe("verifier diff embedding", ...)` in `pkg/steps_review_test.go` (a sibling of the existing `reviewStep` and `dismiss-and-comment routing` blocks, with its own `BeforeEach`). It holds:
   - a canned inline diff whose hunk touches a real file + line, e.g.:

     ```go
     const inlineDiff = "diff --git a/pkg/foo.go b/pkg/foo.go\n" +
         "@@ -95,6 +95,7 @@ func foo() {\n" +
         "+ added line\n"
     ```

   - `BeforeEach`: `prState := &mocks.PRStateClient{}` with `prState.PRStateReturns("OPEN", "", "abc123", nil)` (headRefOid MUST equal the fixture's `ref: abc123` — see the prStateCheck note below) and `prState.PRDiffReturns(inlineDiff, nil)`; build the step via `pkg.NewReviewStep(runner, poster, claudelib.Instructions{}, nil, "test-token", "test-bot", libtime.Duration(25*time.Minute), prState)`.
   - a helper that parses a markdown with a GitHub PR URL in the preamble and a `## Review` section:

     ```go
     mdWithReview := func(reviewBody string) *agentlib.Markdown {
         md, err := agentlib.ParseMarkdown(ctx,
             "---\nref: abc123\n---\n\nReview the PR at "+prURL+"\n\n## Review\n\n"+reviewBody)
         Expect(err).NotTo(HaveOccurred())
         return md
     }
     ```

   Use `const prURL = "https://github.com/bborbe/maintainer/pull/2"` (the existing constant pattern).

2. **Wiring row (spec AC 2).** A row asserting the verifier receives a prompt whose preamble embeds both the PR raw diff and the posted review comments. Declare the row's review body first, e.g. `reviewBody := "line 97 in pkg/foo.go"` (a comment body whose cited file + line are present in `inlineDiff`), and build the markdown via `mdWithReview(reviewBody)`. With the runner returning a pass verdict, run the step and assert:
   - `prState.PRDiffCallCount()` equals 1 (the diff was fetched by the host step)
   - `_, prompt := runner.RunArgsForCall(0)` and `Expect(prompt).To(ContainSubstring(inlineDiff))` — the raw diff reached the runner call
   - `Expect(prompt).To(ContainSubstring("Posted Review Comments"))` and `Expect(prompt).To(ContainSubstring(reviewBody))` — the posted review comments reached the runner call
   - `result.NextPhase` equals `"done"` (the pass verdict routes normally)

   This row is the regression lock: deleting the `envContext` wiring in `reviewStep.Run` removes the diff and the `Posted Review Comments` key from the prompt and flips this row to fail.

3. **Clean-pass row (spec AC 3).** A row where the `## Review` cites a file + line that IS present in `inlineDiff` (e.g. `pkg/foo.go` line 97 within the `@@ -95,6 +95,7 @@` hunk). Runner returns `{"verdict":"pass","reason":"all cited files/lines in diff"}`. Assert:
   - `prState.PRDiffCallCount()` equals 1 (the diff was fetched)
   - the captured prompt `_, prompt := runner.RunArgsForCall(0)` contains the diff hunk marker (e.g. `"@@ -95,6 +95,7 @@"`), proving the verifier had the inline diff to verify against
   - `result.Status` equals `agentlib.AgentStatusDone` and `result.NextPhase` equals `"done"` — the clean review is NOT escalated to `human_review`

   This row is the second regression lock: removing the inline-diff wiring makes the prompt lack the diff, flipping this row to fail.

4. **Fabricated-hallucination row (spec AC 4).** A row where the `## Review` cites a file/line ABSENT from `inlineDiff` (e.g. `pkg/notpresent.go` line 99 — not touched by the canned diff). The fake runner returns a fail verdict whose `hallucinations` array names that fabricated comment, e.g.:

   ```go
   runner.RunReturns(&claudelib.ClaudeResult{Result:
       `{"verdict":"fail","reason":"line 99 not in diff","hallucinations":` +
       `[{"file":"pkg/notpresent.go","line":99,"issue":"line 99 not in diff"}]}`}, nil)
   ```

   Stub `poster.DismissCurrentReviewReturns(pkg.PostResult{Outcome: "success", HTTPStatus: 200})`. Assert:
   - `result.NextPhase` equals `"human_review"` (a genuine hallucination still fails and escalates)
   - the captured prompt contains `inlineDiff` (the check had the ground truth to detect the fabrication)
   - `poster.DismissCurrentReviewCallCount()` equals 1 and `_, _, _, hallucinations := poster.DismissCurrentReviewArgsForCall(0)` yields exactly one hallucination with `File == "pkg/notpresent.go"`, `Line == 99`, `Issue == "line 99 not in diff"` — the returned hallucination object names the fabricated comment (the accessor pattern at the existing "case (a)" dismiss row)

5. **Factory allowlist row (spec AC 1).** In `pkg/factory/export_test.go`, add the package-private export mirroring `ExecutionTools`:

   ```go
   // ReviewTools exposes the package-private ai_review-phase allowlist so tests
   // can assert the gh shell-out entries are gone (spec 003 AC 1).
   var ReviewTools = reviewTools
   ```

   In `pkg/factory/factory_test.go`, add a `Describe("ReviewTools", ...)` block in the same style as `Describe("ExecutionTools", ...)` asserting:
   - `factory.ReviewTools` contains `"Read"` and `"Grep"`
   - `factory.ReviewTools` has exactly 2 entries (locks the narrowed allowlist)
   - no entry contains `"Bash"` and no entry contains `"gh pr"` — the two `Bash(gh pr ...)` entries are gone and no general Bash was added

6. **PRDiff subprocess mirror test (spec AC 2 wiring, shell-out boundary).** In `pkg/github/client_test.go`, add a `Context("PRDiff", ...)` block mirroring the existing `PRState` subprocess tests (lines ~114-135) that validates the new `gh pr diff` shell-out + error surface against the real binary:
   - `It("fails for an unreachable PR URL")` — `github.NewGHClient("")` calls `client.PRDiff(ctx, "https://github.com/nonexistent-owner/nonexistent-repo/pull/1")` and `Expect(err).To(HaveOccurred())` (404 from `gh` against a non-existent repo — validates the shell-out + wrapped-error surface regardless of whether `gh` is present locally)
   - `It("respects context cancellation")` — a pre-cancelled `context.WithCancel(ctx)` passed to `PRDiff` returns an error (the `exec.CommandContext` honors cancellation, mirroring `PRState`)
   - Add the same NOTE comment as the `PRState` block: success-path routing is covered elsewhere (the fake-runner wiring rows here); the concrete client's contract is the shell-out + error surface

7. **Self-check before finishing:** re-run `<verification>` and confirm it passes; walk each acceptance criterion against the change (wiring row asserts both artifacts in the runner call, clean-pass row asserts no `human_review` escalation, fabricated row asserts the hallucination object, factory row asserts the narrowed allowlist, PRDiff subprocess rows assert the shell-out + error surface).
</requirements>

<constraints>
- Tests use Ginkgo v2 / Gomega with Counterfeiter and the existing fake-runner pattern in `pkg/steps_review_test.go` / `pkg/factory/factory_test.go` — no live Claude, no network (spec Constraint). The fake runner returns canned verdicts; the new rows never touch `gh` or any network.
- Do NOT modify `reviewStep.Run`, `pkg/prompts/review_workflow.md`, or `pkg/factory/factory.go` in this prompt — this prompt only adds tests + the test-only `ReviewTools` export. The wiring under test is the preamble embedding shipped by prompt 1 (spec: tests come after the wiring exists).
- `callVerifier` and the `VerifyReview` REST poll are out of scope — the new rows pass a nil verifier and leave the existing verification-behavior tests untouched (spec Constraint).
- Do NOT add gh-exec success-path tests to `pkg/github/client_test.go` — the ghClient shell-out is not mockable without refactoring (per that file's own NOTE). The `PRDiff` subprocess boundary IS covered by the two failure-path rows in requirement 6 (unreachable PR URL → wrapped error; pre-cancelled context → error), mirroring the existing `PRState` subprocess tests — add those only, never a success-path row.
- No diff truncation logic, no new config knobs, no new Prometheus metrics (spec Non-goals by reference).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass (including the existing `reviewStep`, `dismiss-and-comment routing`, `shouldVerifyPost`, and factory allowlist rows).
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license). Then `go test -mod=mod ./pkg/...` — the new rows must be green and the suite green.

- `grep -n 'PRDiffCallCount\|PRDiffReturns\|Posted Review Comments' pkg/steps_review_test.go` — must show the wiring/clean-pass/fabricated rows (AC 2/3/4 evidence)
- `grep -n 'pkg/notpresent.go' pkg/steps_review_test.go` — must show the fabricated-hallucination row (AC 4 evidence)
- `grep -n 'ReviewTools' pkg/factory/export_test.go pkg/factory/factory_test.go` — must show the export and its allowlist assertions (AC 1 evidence)
- `grep -n 'Bash(gh pr' pkg/factory/factory_test.go pkg/factory/export_test.go` — must return 0 lines (no gh allowlist regression slips into the test surface)
- `grep -n 'PRDiff' pkg/github/client_test.go` — must show the `Context("PRDiff", ...)` block with the unreachable-PR and context-cancellation rows (requirement 6 evidence)
</verification>
