---
status: completed
summary: Added override_reason handling to the pr-override step so trivial-bot-bump posts a mechanical APPROVE, unknown reasons post nothing, with tests and docs updated
execution_id: github-pr-review-agent-trivial-bump-override-exec-027-override-reason-trivial-bot-bump
dark-factory-version: v0.198.7
created: "2026-10-23T21:30:00Z"
queued: "2026-10-23T19:50:16Z"
started: "2026-10-23T19:52:32Z"
completed: "2026-10-23T20:01:13Z"
---

# Select the pr-override APPROVE body from an `override_reason` frontmatter field

<summary>
- The override step can now post a second, clearly labelled kind of APPROVE: a mechanical approve for trivial bot dependency bumps
- Which kind is posted is chosen by a new task field the watcher sets; a task without the field behaves exactly as today
- The mechanical approve's text names the rule (`trivial-bot-bump`), says no LLM review ran, and says CI checks still apply
- An unrecognised reason posts nothing and hands the task to a human, so a watcher/agent version mismatch can never post a misleading approve
- The project's own guidance, which said "do not widen the override path", is updated to record this deliberate widening and the operator decision behind it
- Unit tests prove all three cases: no reason, the trivial-bump reason, an unknown reason
- A changelog entry describes the new field
</summary>

<objective>
Let the github-pr-watcher hand trivial `ben-s-go-updater[bot]` dependency-bump PRs to the existing `pr-override` task type instead of a full LLM review, so those PRs get the one approving review the master ruleset requires without spending a reviewer pod — while the posted APPROVE says truthfully that it is a mechanical, rule-based approve rather than a label override. The operator chose this widening on 2026-10-23.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Ginkgo/Gomega tests, counterfeiter mocks under `mocks/`, `github.com/bborbe/errors`, `glog` with `V(n)`).

Read these files in full:
- `pkg/steps_override.go` — `overrideStep.Run`: reads frontmatter `ref` via `md.Frontmatter.String("ref")`, builds a fixed label-override `body` with `fmt.Sprintf(... s.botLogin)`, calls `s.prPoster.PostOverrideApprove(ctx, *prInfo, ref, body)`, writes `## Override` via `formatOverrideResult`, routes to `human_review`. The early returns for missing PR URL / parse failure / non-GitHub use `AgentStatusDone` + `NextPhase: "human_review"`; that is the pattern to copy for the unknown-reason case.
- `pkg/steps_override_test.go` — existing `Describe("Run")` cases; the success case asserts `Expect(body).To(ContainSubstring("override-review"))`. New cases follow the same shape (frontmatter in a raw-string markdown, `poster.PostOverrideApproveReturns`, `poster.PostOverrideApproveArgsForCall(0)`).
- `CLAUDE.md` — the bullet starting ``- `pkg/steps_override.go` — `pr-override` path`` (package map) and the bullet starting ``- **`pr-override` is the one unconditional-approve path**`` (Key Design Decisions).
- `docs/dod.md` — § Review-Posting Safety: the bullet starting ``- The `pr-override` path (`PostOverrideApprove`) is the one deliberate exception`` and the bullet starting `- No new code path posts to a PR other than`.
- `CHANGELOG.md` — head only: the newest section is `## v0.11.9`; there is currently NO `## Unreleased` section.

The producer side (not in this repo): github-pr-watcher will emit `pr-override` tasks carrying frontmatter `override_reason: trivial-bot-bump` only when the PR author is on its mechanical-override allowlist, the PR is not a draft, the repo's `.maintainer.yaml` sets both `autoMerge.trivial` and `prReviewer.autoApprove`, and every changed file classifies trivial (go.mod / go.sum / CHANGELOG.md mechanical lines only). The label path keeps emitting tasks with no `override_reason`. The agent does not re-check any of this — exactly as it does not re-check the label/trusted-author gate today.

This prompt is standalone (no spec): the change is one switch in one step plus docs. The literal `trivial-bot-bump` is a cross-repo contract with github-pr-watcher; `docs/dod.md` (requirement 5) is where this repo records it.
</context>

<requirements>
1. **Add the reason constant** in `pkg/steps_override.go`, next to `TaskTypePROverride`:

   ```go
   // OverrideReasonTrivialBotBump is the override_reason the github-pr-watcher
   // sets on a pr-override task for a trivial bot dependency bump (operator
   // decision 2026-10-23). An empty override_reason is the label path.
   const OverrideReasonTrivialBotBump = "trivial-bot-bump"
   ```

2. **Select the body from `override_reason`** in `overrideStep.Run`, after the `ref == ""` check and before the `glog` line. Replace the existing `body := fmt.Sprintf(...)` with a `var body string` declaration followed by a `switch reason` whose arms assign `body`. Read the reason with `reason, _ := md.Frontmatter.String("override_reason")`. Arms:
   - `""` → today's label body, byte-for-byte unchanged.
   - `OverrideReasonTrivialBotBump` → exactly:

     ```go
     body = fmt.Sprintf(
         "Mechanical APPROVE by %s — rule `trivial-bot-bump`: the github-pr-watcher "+
             "classified this bot-authored PR as trivial (go.mod / go.sum / CHANGELOG.md "+
             "mechanical lines only) in a repo opted into `autoMerge.trivial` and "+
             "`prReviewer.autoApprove`. No LLM review ran. CI status checks still apply.",
         s.botLogin,
     )
     ```
   - any other value → do NOT call `PostOverrideApprove`; return `&agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "human_review", Message: fmt.Sprintf("override: unknown override_reason %q — not posting", reason)}, nil`.

   Extend the existing `glog.V(2).Infof("override: posting APPROVE pr=%s/%s#%d sha=%s", ...)` to also log `reason=%s` (log `label` when the reason is empty). Update the `overrideStep` type doc comment so it states both callers: the trusted-author label path and the `trivial-bot-bump` path.

3. **Tests** in `pkg/steps_override_test.go`, inside `Describe("Run")`:
   - Keep the existing success case; add `Expect(body).NotTo(ContainSubstring("trivial-bot-bump"))` to it (empty reason keeps the label body).
   - New case: frontmatter `ref: abc123` + `override_reason: trivial-bot-bump`, poster returns success. Assert `PostOverrideApproveCallCount() == 1`, head SHA `abc123`, `body` contains `` rule `trivial-bot-bump` `` and `No LLM review ran`, `body` does NOT contain `override-review`, result `AgentStatusDone` / `human_review`.
   - New case: frontmatter `ref: abc123` + `override_reason: something-else`. Assert `PostOverrideApproveCallCount() == 0`, result `AgentStatusDone`, `NextPhase == "human_review"`, `Message` contains `unknown override_reason`.

4. **Record the widening in `CLAUDE.md`.** Replace the package-map bullet with:

   ```
   - `pkg/steps_override.go` — `pr-override` path: post an unconditional `APPROVE` for trusted-author label PRs, or (frontmatter `override_reason: trivial-bot-bump`) a mechanical rule-named `APPROVE` for trivial bot dependency bumps.
   ```

   Replace the Key Design Decisions bullet with:

   ```
   - **`pr-override` is the one unconditional-approve path** — `PostOverrideApprove`, no autoApprove gate, no clone, no container. Two producers only: the trusted-author `override-review` label (no `override_reason`), and `override_reason: trivial-bot-bump`, emitted by github-pr-watcher for allowlisted bot authors' trivial bumps in repos opted into `autoMerge.trivial` + `prReviewer.autoApprove` (widened by operator decision 2026-10-23). The agent trusts the watcher's gate and re-checks nothing; an unknown `override_reason` posts nothing and routes to `human_review`. Do not add a further reason without an operator decision.
   ```

5. **Record the widening in `docs/dod.md`** § Review-Posting Safety. Replace the `pr-override` exception bullet with:

   ```
   - The `pr-override` path (`PostOverrideApprove`) is the one deliberate exception and posts unconditionally for exactly two producers: the trusted-author label (no `override_reason`) and `override_reason: trivial-bot-bump` (operator decision 2026-10-23). An unknown `override_reason` must post nothing. Do not add a further reason without an operator decision
   ```

   Leave the `- No new code path posts to a PR other than ...` bullet unchanged.

6. **CHANGELOG.** Create `## Unreleased` directly above `## v0.11.9` (below the preamble; final order `# Changelog` → preamble → `## Unreleased` → `## v0.11.9`) with exactly one bullet:

   ```markdown
   - feat: `pr-override` tasks accept frontmatter `override_reason`; `trivial-bot-bump` posts a mechanical APPROVE whose body names the rule and states no LLM review ran (github-pr-watcher emits it for trivial bot dependency bumps in repos opted into `autoMerge.trivial` + `prReviewer.autoApprove`, operator decision 2026-10-23); an empty reason keeps the label-override body unchanged; an unknown reason posts nothing and routes to `human_review`
   ```

7. **Self-check before finishing.** Re-run `<verification>` and confirm it passes. Walk each acceptance criterion: empty reason → unchanged body; `trivial-bot-bump` → rule-named body; unknown → no post; CLAUDE.md and docs/dod.md no longer contain `Do not widen it` / `do not widen that surface`; changelog bullet under `## Unreleased`.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Change exactly these files: `pkg/steps_override.go`, `pkg/steps_override_test.go`, `CLAUDE.md`, `docs/dod.md`, `CHANGELOG.md`. No other file.
- Do NOT change the `PrPoster` interface, `PostOverrideApprove`, `formatOverrideResult`, or the label-path body text.
- Do NOT add any re-check of author, labels, files or `.maintainer.yaml` in the agent — the watcher owns the gate.
- An unknown `override_reason` must never reach `PostOverrideApprove`.
- Existing tests must still pass.
</constraints>

<verification>
Run `make ROOTDIR=/workspace precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- Constant present: `grep -q 'OverrideReasonTrivialBotBump = "trivial-bot-bump"' pkg/steps_override.go`.
- Old "do not widen" wording gone: `! grep -q 'Do not widen it' CLAUDE.md` and `! grep -q 'do not widen that surface' docs/dod.md`.
- New wording present: `grep -q 'operator decision 2026-10-23' CLAUDE.md` and `grep -q 'operator decision 2026-10-23' docs/dod.md`.
- Changelog placement: `awk '/^## /{sec=$0} /override_reason/{print "sits under: " sec}' CHANGELOG.md` prints exactly `sits under: ## Unreleased`; `awk '/^## /{print $0}' CHANGELOG.md | head -2` prints `## Unreleased` then `## v0.11.9`.
- New tests present: `grep -q 'unknown override_reason' pkg/steps_override_test.go` and `grep -q 'No LLM review ran' pkg/steps_override_test.go`.
- Literal not leaked outside the step: `grep -rl 'trivial-bot-bump' --include='*.go' pkg/ | sort` prints only `pkg/steps_override.go` and `pkg/steps_override_test.go`.
</verification>
