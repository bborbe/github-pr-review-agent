---
status: completed
spec: [003-bug-ai-review-verifier-false-fails-on-gh-unavailable]
summary: Embedded the host-fetched PR diff + posted review comments in the ai_review verifier preamble (new PRStateClient.PRDiff + ghClient impl + regenerated mocks), rewired the hallucination check to the inline diff, narrowed reviewTools to Read/Grep, and updated docs + CHANGELOG
execution_id: github-pr-review-agent-exec-011-spec-003-verifier-inline-diff
dark-factory-version: dev
created: "2026-08-27T22:44:08Z"
queued: "2026-08-28T06:11:44Z"
started: "2026-08-29T23:02:59Z"
completed: "2026-08-29T23:07:29Z"
---

# Embed the PR diff in the ai_review verifier preamble

<summary>
- The ai_review verifier no longer depends on the model running `gh` — the PR raw diff is fetched by the host step and embedded directly in the verifier prompt preamble
- The verifier's prompt now carries the PR raw diff and the posted review comments as host-supplied prompt text, so the hallucination check always has the ground truth to verify against
- The verifier workflow instructions direct the verifier to verify each cited file + line against the supplied inline diff instead of shelling out to `gh pr diff`
- The review-phase tool allowlist loses both `Bash(gh pr ...)` entries — `Read` and `Grep` are the only tools left, and no general `Bash` is added
- The planning phase keeps its `gh` access unchanged (planning legitimately runs gh to inspect the PR)
- If the diff cannot be fetched at verifier time, the run fails-closed so the controller re-triggers — the hallucination gate can never be silently bypassed or passed without verification
- The separate "review persisted on GitHub" REST poll is untouched
- The architecture doc's hallucination-check description and the CHANGELOG are brought in line with the new inline-diff behavior
</summary>

<objective>
Make the ai_review verifier validate a posted review against the actual PR diff deterministically — the diff is handed to it by the host code in the prompt preamble, not fetched at the model's discretion — so a sound review's verdict stands, the false "gh is not available" failure disappears, and a genuine hallucination is still caught against real ground truth.
</objective>

<context>
Read `CLAUDE.md` for project conventions (errors, logging, test style).

Read `docs/architecture.md` — the "ai_review's Consistency Check" section (line ~56, check 2 "No hallucinations" still says `gh pr diff`) and the "Three Phases" table. Read `docs/dod.md` — the CHANGELOG rule (§ Documentation) for where `## Unreleased` goes.

Read these files fully:
- `pkg/steps_review.go` — `reviewStep.Run` (builds the verifier prompt at `prompt := claudelib.BuildPrompt(s.instructions.String(), nil, taskContent)`), `callVerifier` (~line 267, the REST poll that MUST stay untouched), `githubPRURLPattern.FindString(md.Preamble)` usage, and the `shouldVerifyPost`/`md.FindSection("## Review")` patterns.
- `pkg/steps_checkout_execution.go` — the `githubPRURLPattern` var declaration (lines ~26-27).
- `pkg/pr_state_check.go` — the `PRStateClient` interface (add `PRDiff` here) and its counterfeiter directive.
- `pkg/github/client.go` — the `Client` interface and `ghClient.PRState` method (the exec + `errors.Wrapf` + GH_TOKEN pattern `PRDiff` must follow), and the `var _ github.Client = ...` compile guard in `pkg/github/client_test.go`.
- `pkg/factory/factory.go` — `reviewTools` and `planningTools` allowlists (the ai_review and planning tool scopes).
- `pkg/prompts/review.go` + `pkg/prompts/review_workflow.md` — the ai_review instructions (the hallucination check to reword).
- `pkg/prompts/review_output-format.md` — the verdict JSON schema (`pass | fail`, `hallucinations` array) that stays unchanged.
- `mocks/pr-state-client.go` — the regenerated mock contract your tests (next prompt) will drive; `make generate` regenerates it from the counterfeiter directive.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors` `Wrapf(ctx, err, ...)` style
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — CHANGELOG entry format

Verified contracts (do not re-derive):
- `claudelib "github.com/bborbe/agent/claude"` — `func BuildPrompt(instructions string, envContext map[string]string, taskContent string) string`. `envContext` is rendered (keys sorted) under `## Environment` between the instructions and `## Task` — this is the "verifier preamble". The second argument is currently `nil` at the reviewStep call site.
- `agentlib "github.com/bborbe/agent"` — `type Section struct { Heading string; Body string }`; `func (m *Markdown) FindSection(heading string) (*Section, bool)`.
- `PRStateClient` (pkg/pr_state_check.go) is a narrow interface satisfied by `pkg/github.Client`'s `ghClient`; adding a method to it ripples to the counterfeiter mock only (all three phase steps hold the interface by field, not by constructor signature, so no constructor call site changes).
- `reviewStep` already holds `prState PRStateClient` (nil = no check, the established nil-interface skip pattern used by `poster` and `verifier`).
</context>

<requirements>

1. **Add a diff-fetch method to the `PRStateClient` interface** in `pkg/pr_state_check.go`. Extend the interface (keep the existing `PRState(ctx context.Context, prURL string) (state, mergedAt, headRefOid string, err error)` member unchanged) with:

   ```go
   // PRDiff fetches the raw unified diff of a pull request via the gh CLI.
   // Satisfied by pkg/github.Client (the gh CLI wrapper), same auth path as PRState.
   PRDiff(ctx context.Context, prURL string) (string, error)
   ```

   Do NOT change the constructor signatures of `NewPlanningStep`, `NewCheckoutExecutionStep`, or `NewReviewStep` — they already hold the interface.

2. **Implement `PRDiff` on `ghClient`** in `pkg/github/client.go`. Add `PRDiff(ctx context.Context, prURL string) (string, error)` to the `Client` interface (so the `var _ github.Client = github.NewGHClient("")` compile guard in `pkg/github/client_test.go` keeps passing) and implement it on `ghClient` following the exact shape of the existing `PRState` method:
   - `exec.CommandContext(ctx, "gh", "pr", "diff", prURL)` (mirror `PRState`'s `#nosec G204` comment — the URL comes from `githubPRURLPattern` extraction)
   - set `GH_TOKEN=<token>` on `cmd.Env` when `c.token != ""`
   - capture stdout + stderr into buffers
   - on `cmd.Run()` error: `errors.Wrapf(ctx, err, "gh pr diff failed: %s", strings.TrimSpace(stderr.String()))` and a `glog.V(2).Infof` line (mirror `PRState`)
   - on success return the raw stdout as the diff string (no trimming, no parsing — the diff is passed through verbatim)

3. **Regenerate the counterfeiter mock.** After step 1, run `make generate` (or `go generate -mod=mod ./...`) so `mocks/pr-state-client.go` gains the `PRDiff` stub (`PRDiffStub`, `PRDiffReturns`, `PRDiffCallCount`, `PRDiffArgsForCall`). Do not hand-edit the generated file.

4. **Embed the diff + posted review comments in the verifier preamble** in `pkg/steps_review.go`, inside `reviewStep.Run`, between the `taskContent, err := md.Marshal(ctx)` call and the `prompt := claudelib.BuildPrompt(...)` call. Replace the current `prompt := claudelib.BuildPrompt(s.instructions.String(), nil, taskContent)` with logic that:

   - Declares `envContext := make(map[string]string)` — MUST be non-nil from the start. A nil map (`var envContext map[string]string`) panics with `assignment to entry in nil map` on the first `envContext["PR Diff"] = diff`. An empty map renders byte-identically to today's call, since `BuildPrompt` skips the `## Environment` block when `len(envContext) == 0` — so the byte-identical claim below still holds.
   - If `s.prState != nil` (production always wires it; nil = test/local skip, mirroring the nil-poster / nil-verifier pattern):
     - extract `prURLStr := githubPRURLPattern.FindString(md.Preamble)`
     - if `prURLStr == ""`: return `&agentlib.Result{Status: agentlib.AgentStatusFailed, Message: "ai_review: no GitHub PR URL in preamble — cannot fetch diff"}` (fail-closed; a verifier with no diff must never pass)
     - call `diff, err := s.prState.PRDiff(ctx, prURLStr)`; on error log a `glog.Warningf` and return `&agentlib.Result{Status: agentlib.AgentStatusFailed, Message: fmt.Sprintf("ai_review: fetch PR diff failed: %v", err)}` — the controller re-triggers (spec Failure-Mode row 1 recovery is "Re-trigger the review"; this matches the existing transport-error path that returns `AgentStatusFailed`)
     - set `envContext["PR Diff"] = diff`
   - Independent of `prState`: if the `## Review` section exists (`sec, ok := md.FindSection("## Review")` with `ok && sec != nil`), set `envContext["Posted Review Comments"] = sec.Body` (the map is already non-nil — plain assignment).
   - Build the prompt with `claudelib.BuildPrompt(s.instructions.String(), envContext, taskContent)`. When `envContext` is nil this is byte-identical to today's call — existing tests that pass a nil `prState` and no `## Review` stay green.
   - Do NOT add any truncation/size-limit of the diff (spec Failure-Mode row 4: truncation is explicitly deferred to a follow-up spec; the existing `MAX_ADDITIONS`/`MAX_CHANGED_FILES` parking covers the extreme).
   - Do NOT touch `callVerifier` (the `VerifyReview` REST poll at ~line 267) or `shouldVerifyPost` — they are separate mechanisms.

5. **Reword the hallucination check in `pkg/prompts/review_workflow.md`** so the verifier consumes the inline diff:
   - In the `## Inputs` section, add a bullet: the inline PR diff and the posted review comments are supplied under `## Environment` in the prompt preamble (`PR Diff` and `Posted Review Comments`).
   - Replace check 2 ("No hallucinations") — currently "For each comment in `## Review`, run `gh pr diff <url>` and verify the cited file + line number actually exist in the diff" — with an instruction to verify each comment's cited file + line against the inline diff provided under `## Environment` (`PR Diff`), and that a comment citing a file or line absent from the inline diff is a hallucination. Use the literal phrase `inline diff` (the spec's verification greps for it).
   - Delete the `## Rules` bullet "If `gh` calls fail during hallucination check, return `failed`." — there are no `gh` calls anymore. Add a bullet stating the verifier must NOT attempt to run `gh` or any shell command (no Bash tool is available); the diff is already provided inline.
   - Do NOT change the verdict semantics, the `## Review` format, the `pass`/`fail`/`needs_input` rules, or the `## Three Checks` structure (spec Constraint — changes are limited to the diff-consumption instruction).

6. **Drop both `Bash(gh pr ...)` entries from `reviewTools`** in `pkg/factory/factory.go`:

   ```go
   reviewTools = claudelib.AllowedTools{
       "Read", "Grep",
   }
   ```

   `planningTools` stays UNCHANGED (it keeps `Bash(gh pr view:*)`, `Bash(gh pr diff:*)`, `Bash(gh pr list:*)` — planning legitimately runs gh, per spec Constraint "no other allowlist entries change"). No general `Bash` is added. Note for the human reviewer: spec AC 1's bare `grep -n 'Bash(gh pr' pkg/factory/factory.go` would also match planningTools' legitimate gh entries, so the file-content grep is scoped to the `reviewTools` block (see `<verification>`) — the intent (Desired Behavior #3) is that only `reviewTools` loses its gh entries.

7. **Update `docs/architecture.md`** check 2 under "ai_review's Consistency Check" (~line 56): replace "that actually exists in `gh pr diff`" with the inline-diff phrasing (e.g. "that actually exists in the inline PR diff supplied in the verifier preamble"). `docs/pr-post-back.md` has no diff reference and needs no change.

8. **Add a CHANGELOG entry.** Under `## Unreleased` (create the section if absent — insert it below the preamble block and above the newest `## vX.Y.Z` section, per `docs/dod.md` § Documentation), add a single `fix:` bullet describing the change in plain language: the ai_review verifier now verifies hallucinated citations against the PR diff supplied inline in the verifier preamble instead of a model-run `gh pr diff` shell-out, and the ai_review tool allowlist drops the `gh pr` Bash entries.

9. **Self-check before finishing:** re-run `<verification>` and confirm it passes; walk each requirement above against the change (interface + implementation + mock regenerated, preamble wiring in place, workflow reworded with the `inline diff` phrase, allowlist narrowed, docs + CHANGELOG updated).
</requirements>

<constraints>
- The only code path changed is the ai_review verifier spawn (`reviewStep.Run` → `s.runner` with `reviewTools` / `BuildReviewInstructions`). `callVerifier` (the GitHub REST poll confirming the review persisted) is a separate mechanism and must remain untouched (spec Constraint).
- The diff and posted review comments are supplied inline in the verifier preamble (prompt text via `BuildPrompt`'s envContext), never via stdin and never via a new tool (spec Constraint).
- The raw diff is NOT persisted by planning (`pkg/steps_planning.go` extracts only the `concerns` JSON) — the fix obtains the diff at the verifier spawn itself; the posted `## Review` section is reused from the task markdown, not re-fetched (spec Constraint).
- `reviewTools` loses exactly the two `Bash(gh pr ...)` entries; no other allowlist entries change and no general `Bash` is added (spec Constraint — the change narrows the Bash surface).
- `pkg/prompts/review_workflow.md` changes are limited to the diff-consumption instruction; review/verdict semantics, the `## Review` format, and the ai_review trigger conditions are unchanged (spec Constraint).
- Do NOT add `gh` to the image (it is already installed) and do not rework the verifier prompt beyond inline-diff consumption (spec Constraint).
- No diff truncation / size-limit knob — the spec Failure Mode explicitly defers truncation to a follow-up spec (spec Non-goal by reference).
- Errors use `github.com/bborbe/errors` with context wrapping; logging is `glog` with `V(n)`-gated info (project convention).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass (they construct `NewReviewStep` with a nil `prState` — the nil-interface skip keeps them green).
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license). Then `go test -mod=mod ./pkg/...` — must be green.

- `sed -n '/reviewTools = claudelib.AllowedTools{/,/^[[:space:]]*}/p' pkg/factory/factory.go | grep -c 'Bash(gh pr'` — must print `0` (reviewTools scoped AC 1 check; planningTools' gh entries are expected to remain)
- `grep -n -i 'gh pr diff' pkg/prompts/review_workflow.md` — must return 0 lines (AC 5)
- `grep -n -i 'inline diff' pkg/prompts/review_workflow.md` — must return line >= 1 (AC 5)
- `grep -n 'PRDiff' pkg/pr_state_check.go pkg/github/client.go mocks/pr-state-client.go mocks/github-client.go` — must show the interface member, the ghClient implementation, and the regenerated mock methods in BOTH mock files (`mocks/github-client.go` also gains a `PRDiff` stub when the `Client` interface changes — its `var _ github.Client` compile guard forces regeneration; `make generate` handles both)
- `grep -n 'envContext\|PR Diff\|Posted Review Comments' pkg/steps_review.go` — must show the preamble embedding in `reviewStep.Run`
</verification>
