---
status: completed
approved: "2026-08-27T22:40:44Z"
generating: "2026-08-28T06:11:53Z"
prompted: "2026-08-28T06:11:53Z"
verifying: "2026-08-29T23:10:44Z"
completed: "2026-08-30T16:05:58Z"
branch: dark-factory/bug-ai-review-verifier-false-fails-on-gh-unavailable
---

# Summary

- The ai_review verifier — the step that double-checks a posted review for hallucinated file/line citations — is designed around a live `gh pr diff` shell-out (`pkg/prompts/review_workflow.md` tells it: "For each comment in ## Review, run gh pr diff <url> and verify the cited file + line actually exist").
- The verifier has no general `Bash` (its allowlist is only `Read`, `Grep`, `Bash(gh pr view:*)`, `Bash(gh pr diff:*)`), and the model excuses out of the shell-out: every run returns `verdict=fail` "Hallucination check failed ... gh is not available" — even though the image installs `github-cli` and the planning phase runs `gh` successfully in the same pod.
- The false-fail ends the Job with `NextPhase=human_review`, so every PR — sound or not — is dumped to the operator queue, and a clean APPROVE never lands.
- Fix: stop depending on the model running `gh`. Pre-supply the PR raw diff and the posted review comments inline in the verifier preamble, teach `review_workflow.md` to verify against that inline diff, and drop both `Bash(gh pr ...)` allowlist entries. Add regression-lock tests: clean APPROVE passes, fabricated hallucination fails with the correct hallucination object.
- The `callVerifier` REST poll (which confirms the review persisted on GitHub) is a separate mechanism and stays untouched.

# Problem

The ai_review verifier is a post-posting sanity gate: after the review phase posts an inline `## Review` section, it re-checks each comment's cited file + line against the actual PR diff, so a fabricated citation gets caught before the verdict stands. That check currently depends on the model executing `gh pr diff` at its own discretion. The model (deepseek-v4-flash-max) refuses the shell-out — reporting "gh is not available" — instead of running the binary that is installed and used elsewhere in the same pod. Because the verifier has no fallback input, every run false-fails with `verdict=fail` "Hallucination check failed ... gh is not available", the Job escalates to `human_review`, and every reviewed PR lands in the operator queue regardless of review soundness. The hallucination gate stops checking anything and becomes a pure escalator.

# Goal

After this work, the ai_review verifier validates a posted review against the PR diff deterministically — the diff is handed to it, not fetched at the model's discretion. A sound review passes and its verdict stands (APPROVE can land); a genuine hallucination still fails with the hallucination object naming the fabricated comment. No real run produces "gh is not available".

# Reproduction

**Observed incident** — pod `pr-reviewer-agent-ee3c5e25-20260609170519-956th` (2026-06-09, prod run on `bborbe/dark-factory` #23):

1. A PR review runs; the review phase posts an inline `## Review` (part of the `md.Marshal(ctx)` output fed into the ai_review prompt).
2. Because the posted review is non-LGTM, the ai_review verifier spawns: `reviewStep.Run` → `s.runner` with `reviewTools` + `BuildReviewInstructions` (`pkg/steps_review.go`).
3. The verifier reads `review_workflow.md`, which instructs: "For each comment in ## Review, run gh pr diff <url> and verify the cited file + line actually exist."
4. The model does not run `gh`; it reports `gh is not available` and returns a fail verdict.
5. Verifier returns `verdict=fail` "Hallucination check failed ... gh is not available"; the Job ends `NextPhase=human_review`; the PR is queued for an operator despite a clean, sound review.

Agent image at incident time: see pod `pr-reviewer-agent-ee3c5e25-20260609170519-956th`. The behavior reproduces at the current deployed tag `v0.6.3` (verified: the verifier false-fails every run).

**Minimal mechanism (no cluster needed):** the verifier's only source for the diff is a model-run `gh pr diff` under an allowlist with no general `Bash`. If the model does not (or cannot) emit an exact matching `gh pr diff ...` command, the input never arrives and the verifier fails. That input must instead be supplied by the host code.

# Expected vs Actual

- **Expected:** a sound review (every cited file + line exists in the PR diff) passes the ai_review verifier and the posted verdict stands; only a genuine hallucination (cited file/line absent from the diff) fails. The `gh` binary is present (the image installs `github-cli`) and the planning phase runs `gh pr view`/`gh pr diff` successfully in the same pod — "gh is not available" is a model claim, not a missing tool. Documented source: `pkg/prompts/review_workflow.md` (the hallucination check) and `pkg/factory/factory.go` (`reviewTools`).
- **Actual:** the verifier returns `verdict=fail` "Hallucination check failed ... gh is not available" on every run. The check never executes against the real diff, the Job ends `NextPhase=human_review`, and every PR is escalated to the operator queue regardless of review soundness.

# Why this is a bug

The verifier's documented contract is to reject hallucinated citations, and the diff it needs is available (the review phase already diffs the PR). Instead the check is gated on the model voluntarily running `gh`; the model declines, so the gate false-fails every time and never once does its job. This contradicts the autonomy promise of the agent: an `APPROVE` never lands (the review is always parked at `human_review`), the operator queue grows with sound reviews, and the hallucination check becomes a pure escalator. The design flaw is that the diff is model-fetched rather than host-supplied — exactly the dependency the fix removes.

# Workaround

None for the agent. Affected PRs are unblocked by operators clearing the `human_review` queue by hand. That is the ongoing cost this bug imposes.

# Acceptance Criteria

- [ ] The review-tools allowlist in `pkg/factory/factory.go` contains no `Bash(gh pr view:*)` or `Bash(gh pr diff:*)` entry, scoped to the `reviewTools` block only — `planningTools` legitimately retains its `Bash(gh pr ...)` entries and must not change — evidence: `sed -n '/reviewTools = claudelib.AllowedTools{/,/^[[:space:]]*}/p' pkg/factory/factory.go` shows no `Bash(gh pr` entry (file content, negative, reviewTools-scoped) AND a factory test asserts the narrowed allowlist
- [ ] Ginkgo regression-lock (wiring): with the fake runner, the ai_review verifier receives instructions whose preamble embeds the PR raw diff and the posted review comments JSON — evidence: `go test ./pkg/...` exits 0; the new spec row asserts both artifacts appear in the runner call; removing the inline-diff wiring flips the row to fail
- [ ] Ginkgo regression-lock (clean pass): a clean review whose cited file + line are present in the supplied inline diff passes the verifier and does not escalate to `human_review` — evidence: `go test ./pkg/...` exits 0 with the row green; removing the inline-diff wiring flips the row to fail
- [ ] Ginkgo regression-lock (fabricated hallucination): a comment citing a file/line absent from the supplied inline diff fails the verifier and the returned hallucination object names that fabricated comment — evidence: `go test ./pkg/...` exits 0 with the row green
- [ ] `pkg/prompts/review_workflow.md` instructs the verifier to verify each cited file + line against the supplied inline diff and no longer against a live `gh pr diff` — evidence: `grep -n -i 'gh pr diff' pkg/prompts/review_workflow.md` returns 0 lines AND `grep -n -i 'inline diff' pkg/prompts/review_workflow.md` returns line ≥ 1 (file content)
- [ ] **Post-Deploy (Rung-2):** on a small PR in the dev allowlist repo (`github.com/bborbe/go-skeleton`) that draws a non-LGTM review (so the verifier path executes), the newest review Job pod completes the verifier without the false-fail and the review is not parked at `human_review` — evidence: `kubectlnukedev -n dev logs <newest job pod> | grep -c 'gh is not available'` returns 0 AND the review task file's `## Verdict` section records the ai_review meta-verdict `pass` (the verifier completed, not failed) — evidence: `grep -c '"verdict": "pass"' <task file>` returns line ≥ 1 AND `grep '^phase:' <task file>` returns a terminal verdict value (`done`), not `human_review`.
  - `deploy_check:` `kubectlnukedev -n dev get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}'`
  - `deploy_target:` `docker.prod.nuke.benjamin-borbe.de:443/bborbe/github-pr-review-agent:v0.6.5`
- [ ] **Post-Deploy (Rung-3):** on a small PR in the prod allowlist repo (`github.com/bborbe/dark-factory`, the observed incident repo) that draws a non-LGTM review, the newest review Job pod completes the verifier without the false-fail — evidence: `kubectlnukeprod -n prod logs <newest job pod> | grep -c 'gh is not available'` returns 0 AND the review task file's `## Verdict` section records the ai_review meta-verdict `pass` (the verifier completed, not failed) — evidence: `grep -c '"verdict": "pass"' <task file>` returns line ≥ 1 AND `grep '^phase:' <task file>` returns a terminal verdict value (`done`), not `human_review`.
  - `deploy_check:` `kubectlnukeprod -n prod get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}'`
  - `deploy_target:` `docker.prod.nuke.benjamin-borbe.de:443/bborbe/github-pr-review-agent:v0.6.5`

# Verification Status (2026-08-30)

Prompts 2/2 complete. `/dark-factory:verify-spec 003` ran 2026-08-30 and **refused** — AC 1-5 and AC 7 PASS, **AC 6 FAIL** (its dev evidence predated the deploy). AC 6 was then re-earned on a purpose-built PR, and the two AC-text defects the verifier found were fixed. All ACs now carry evidence; awaiting a re-run. The AC checkboxes above stay unticked — flipping them is the verifier's job, not a hand edit.

AC 1-5 were re-verified first-hand rather than taken on trust: the verifier removed the inline-diff wiring itself, watched all 4 Ginkgo rows flip to FAIL, restored to a byte-clean tree and re-ran green.

The prod-repo substitution flagged earlier was **accepted**, with better reasoning than the note that raised it: prod `REPO_ALLOWLIST` is `github.com/bborbe/*,!github.com/bborbe/go-skeleton`, so `nuke` and `discord-assistant` are not substitutes for "the prod allowlist repo" — they *are* it, under the same wildcard that covers `dark-factory`. The defect lives in repo-agnostic verifier code, so the repo name carries no verification weight.

- **AC 1** ✅ `reviewTools` = `Read`/`Grep` only; `planningTools` unchanged (factory test locks it)
- **AC 2-4** ✅ Ginkgo rows green; regression lock *proven* — wiring removed → all 3 rows fail → restored → pass
- **AC 5** ✅ `grep -c 'gh pr diff' pkg/prompts/review_workflow.md` = 0; `inline diff` present
- **AC 6 (Rung-2 dev)** ✅ **satisfied 2026-08-30 15:53Z on a purpose-built PR** — Job `pr-reviewer-agent-f76c4f41-20260830155342`, `bborbe/go-skeleton#99`. Deliberately built as *presence*-based evidence after the first attempt failed (below):
  - **Image verified from the Job spec, not assumed:** `…/github-pr-review-agent:v0.6.5`.
  - **The verifier path genuinely ran:** the posted review was `CHANGES_REQUESTED` (non-LGTM), which is the precondition for ai_review spawning at all.
  - **Third spawn `--allowedTools Read,Grep`** — no `Bash`, no `gh`.
  - **Zero `Read` tool calls in the entire job**, so the verifier never opened a file from disk.
  - **`buildVerifierPreamble` skip warning fired 0 times**, so `envContext["PR Diff"]` was populated.
  - Yet the verifier adjudicated three comments against diff content: *"All comments cite lines that exist in the diff (line 15 time.Now, line 31 fmt.Errorf, and a valid file-level test-coverage comment)"* → `"verdict": "pass"`, `"hallucinations": []`, `{"Status":"done","NextPhase":"done"}`. With no `gh`, no `Bash`, and no `Read`, the inline diff is the only possible source. Corroborating detail: the cited lines are **15/31** while the file on disk has them at **16/32** — diff-relative numbering, not file-relative.
  - The single `no GitHub PR URL in preamble` line is `steps_review.go:317` (`callVerifier`), the pre-existing skip this spec left untouched.

- **AC 6 — first attempt, FALSIFIED 2026-08-30 by `/dark-factory:verify-spec`** (kept as the record of why the evidence above was rebuilt). The earlier claim here — "`bborbe/go-skeleton#98` on v0.6.5" — was wrong. The two dev runs started 08:28:23Z and 08:58:22Z; the chart bump that put v0.6.5 on dev is `nuke` commit `0a30e35` at **09:02:03Z**, and its own message reads `bump agent v0.6.3 -> v0.6.5` — dev went v0.6.3 → v0.6.5 directly, v0.6.4 never deployed anywhere. **Both dev runs were the pre-fix binary.** Compounding it: AC 6's evidence signature is non-discriminating — the 08:28 v0.6.3 run also yields `"verdict": "pass"`, `phase: done`, `hallucinations: []`, `gh is not available` = 0. The grep set is satisfiable by the unfixed binary, so those runs prove nothing about the fix either way. Re-running needs a fresh go-skeleton PR (#98 was closed 09:02:20Z; no open PRs there).
  - Related premise wobble worth recording: three **prod** v0.6.3 runs (07:50 / 08:12 / 08:41) also reached `phase: done`, so the spec's Reproduction claim that v0.6.3 "false-fails every run" did not hold on 2026-08-30. Consistent with the task's own note that the failure is model excuse-making rather than a missing binary — i.e. intermittent, which makes any single-day `gh is not available` = 0 weak evidence on its own. The decisive AC 7 evidence is positive, not absence-based (see below).
- **AC 7 (Rung-3 prod)** ✅ **evidence collected 2026-08-30 14:51–15:08 UTC** — 4 prod Jobs across 2 distinct PRs on v0.6.5, `gh is not available` count = 0 in all four, zero spurious escalations:
  - `bborbe/nuke#119` — Job `pr-reviewer-agent-5d366a0f-20260830145727`: ai_review `"verdict": "pass"`, `Status=done`, `NextPhase=done`. **This is the AC 7 evidence.**
  - `bborbe/discord-assistant#37` — three Jobs. The first two (`56cf1742`, `fcca21ee`) returned `verdict=fail` → `human_review`, correctly: holding the diff inline, the verifier enumerated the changed files (`CHANGELOG.md`, `shim/claude_openai_shim.py`, `src/commands.js`) and flagged the review's comment on `test/test_shim.py`, absent from the diff. That is AC 5 firing on real traffic, not a regression. Run three (`9f6b7f89`) returned `NextPhase=done` after the PR was corrected.
  - The v0.6.5 skip warning fired 0 times; the two `no GitHub PR URL` lines per run are `steps_review.go:317` (`callVerifier`) and `:384` (dismiss) — pre-existing paths this spec left untouched.
  - **Deviation from the AC text:** the AC names `github.com/bborbe/dark-factory` as the prod repo; the evidence comes from other `bborbe/*` repos. **Adjudicated and accepted** by `/dark-factory:verify-spec` — see the reasoning at the top of this section.
  - **Stronger evidence added by the verifier run (2026-08-30).** Two further prod jobs on `nuke#120`, both stronger than the four above because the proof is *positive* rather than absence-based:
    - `pr-reviewer-agent-c8b97098-20260830152725` — the third claude spawn runs `--allowedTools Read,Grep` (no `Bash`, no `gh` at all), yet returns `"verdict": "pass"`, `"hallucinations": []`, `{"Status":"done","NextPhase":"done"}`; durable task file shows `phase: done`, `status: completed`.
    - `pr-reviewer-agent-f726b93b` (15:10Z) — **the decisive one.** Its verifier, holding only `Read`/`Grep`, wrote: *"line 103 is not present in the provided diff … diff only shows EXECUTOR_VERSION assignment change around line 36"*. It described diff **content it had no tool to fetch**, then emitted a correct hallucination object (`agent/Makefile:103`) → `human_review`. That is the host-supplied inline diff demonstrably working AND the gate still biting — the two things AC 4 and AC 5 exist to prove, observed together on real prod traffic.
  - Verified distinct log strings: `callVerifier` logs `"ai_review verify: no GitHub PR URL in preamble"`; `buildVerifierPreamble` logs `"ai_review: no GitHub PR URL in task — skipping inline diff"`. The latter fired **0 times** across all prod jobs, so the diff was genuinely embedded every time.

**Post-approval correction:** v0.6.4 shipped this spec's fix but introduced a new universal false-fail (`buildVerifierPreamble` matched the PR URL against `md.Preamble` only and fail-closed on a miss; the URL lives in a pre-H2 section by the ai_review phase). Fixed in v0.6.5 (commit `d63fb13`) via `ExtractPRURL(md)` + skip-instead-of-fail, with a regression row reproducing the dev shape. The spec's ACs are satisfied by v0.6.5, not v0.6.4 — `deploy_target` in AC 6/7 should read v0.6.5.

**To close this out — one item left, and it needs a human:**

1. ~~Fix `deploy_target:` → v0.6.5 in AC 6/7~~ — done 2026-08-30.
2. ~~Fix AC 6/7's grep pattern to `'"verdict": "pass"'`~~ — done 2026-08-30. The old `'verdict: pass'` returned 0 against real task files, which record JSON; the AC as written failed on a genuinely passing run.
3. ~~Open a small `bborbe/go-skeleton` PR carrying a real defect so the dev rung executes on v0.6.5~~ — done 2026-08-30 via `go-skeleton#99`; evidence in AC 6 above. PR to be closed, not merged (`test/verify-dev-rung-v065`).
4. Re-run `/dark-factory:verify-spec 003`. **All ACs now have evidence.**

Note: this spec's completion is independent of the parent task's 5-PR temporal criterion, which is a task-level DoD gate (2 of 5 as of 2026-08-30) and is not an AC here.

# Verification

## Container-executable (runs inside the YOLO container at prompt time)

- `sed -n '/reviewTools = claudelib.AllowedTools{/,/^[[:space:]]*}/p' pkg/factory/factory.go` — shows no `Bash(gh pr` entry (reviewTools-scoped; planningTools keeps its gh entries)
- `grep -n -i 'gh pr diff' pkg/prompts/review_workflow.md` — returns 0 lines; `grep -n -i 'inline diff' pkg/prompts/review_workflow.md` — returns line ≥ 1
- `go test ./pkg/...` — new Ginkgo rows (wiring, clean pass, fabricated hallucination) pass; suite green
- `make precommit` — fmt, generate, test, lint, vet, vuln, license all clean

## Operator-executable (runs on the host after PR merge, verification ladder)

- Publish the image: `VERSION=v0.6.5 make buca` produces `docker.io/bborbe/github-pr-review-agent:v0.6.5`
- Bump BOTH version sources in `~/Documents/workspaces/nuke/github-pr-reviewer/` (dual-source footgun): `values-dev.yaml` `agent.tag` AND `values-prod.yaml` `agent.tag`, plus `Makefile` `AGENT_TAG_dev` and `AGENT_TAG_prod` (current dev tag v0.6.3). Confirm the per-stage registry (`image.registry`) matches the deploy_target above; adjust `deploy_target:` to the actual released tag if the release lands on a different number
- Deploy dev first, then prod: `BRANCH=dev make mirror` + `BRANCH=dev make apply`, then `BRANCH=master make mirror` + `BRANCH=master make apply` (per runbook "Agent - Deploy New Version" § standalone agents). **Prod is `BRANCH=master`, not `BRANCH=prod`** — `nuke/Makefile.env` maps `master`→`STAGE=prod` and hard-errors on `BRANCH=prod` with "Invalid BRANCH 'prod' — deploy from dev or master". Hit live during the 2026-08-30 prod deploy; the runbook has been corrected too.
- Functional verify (agents are Jobs — check the newest Job pod + task outcome): open a small PR with a clear defect on `github.com/bborbe/go-skeleton` (dev) and on `github.com/bborbe/dark-factory` (prod), confirm the posted review is non-LGTM (the verifier runs only on a non-LGTM review; if the bot LGTM-approves, the verifier did not run — amend the PR with a stronger defect and re-trigger), then run the Rung-2 / Rung-3 pod-log greps above and confirm `gh pr view <n> --repo <owner>/<repo> --json reviews` shows the bot review at the head SHA.

# Desired Behavior

1. The ai_review verifier spawn (`reviewStep.Run` → `s.runner` with `reviewTools` / `BuildReviewInstructions`) embeds the PR raw diff and the posted review comments inline in the verifier preamble — supplied as prompt text by the host code, not fetched at model discretion.
2. The verifier instructions (`pkg/prompts/review_workflow.md`) direct the verifier to verify each cited file + line against the supplied inline diff, replacing the `gh pr diff` shell-out instruction.
3. `reviewTools` in `pkg/factory/factory.go` drops both `Bash(gh pr view:*)` and `Bash(gh pr diff:*)`; no general `Bash` is added; `Read` and `Grep` remain the only tools.
4. A sound (clean APPROVE) review whose cited files/lines all exist in the inline diff passes the verifier; the Job ends with the posted verdict and is not escalated to `human_review`.
5. A genuine hallucination (a comment citing a file/line absent from the inline diff) still fails the verifier with the hallucination object naming that comment — the check is preserved, only its input source changes.
6. Real runs no longer emit "gh is not available"; `callVerifier` (the GitHub REST poll confirming the review persisted) is untouched; the conditions that trigger ai_review are unchanged.

# Constraints

- The only code path changed is the ai_review verifier spawn (`reviewStep.Run` → `s.runner` with `reviewTools` / `BuildReviewInstructions`). `callVerifier` (the GitHub REST poll at `pkg/steps_review.go` ~264-267 confirming the review persisted) is a separate mechanism and must remain untouched.
- The diff and posted review comments are supplied inline in the verifier preamble (prompt text), never via stdin and never via a new tool.
- The raw diff is NOT persisted by planning (`pkg/steps_planning.go` extracts only the `concerns` JSON) — the fix must obtain/thread the diff into the verifier preamble itself, not forward a planning field that does not exist. The posted `## Review` section is already part of the `md.Marshal(ctx)` output fed into the ai_review prompt and is reused, not re-fetched.
- `reviewTools` loses exactly the two `Bash(gh pr ...)` entries; no other allowlist entries change and no general `Bash` is added (the change narrows the Bash surface — keep it that way).
- `pkg/prompts/review_workflow.md` changes are limited to the diff-consumption instruction; review/verdict semantics, the `## Review` format, and the ai_review trigger conditions (verifier runs only on a posted non-LGTM review) are unchanged.
- Do NOT add `gh` to the image (it is already installed; adding it would paper over the design) and do not rework the verifier prompt beyond inline-diff consumption.
- Regression-lock: removing the inline-diff wiring flips the clean-pass and fabricated-hallucination Ginkgo rows to fail.
- Tests use Ginkgo v2 / Gomega with Counterfeiter and the existing fake-runner pattern in `pkg/steps_review_test.go` / `pkg/factory/factory_test.go` — no live Claude, no network.
- Errors use `github.com/bborbe/errors` with context wrapping; CHANGELOG entry lands under `## Unreleased` (per `docs/dod.md`).
- Reference docs: `docs/architecture.md` (ai_review phase + the four checks in `review_workflow.md`, lines 51-60; verifier spawn context) and `docs/pr-post-back.md` (posting contract beside the fix) — keep both consistent with the changed diff-consumption instruction.

# Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| GitHub API unavailable when the review phase fetches the diff | The review fails before posting (diff is required) — same as today; no new failure surface | Re-trigger the review; the verifier itself no longer depends on live GitHub state |
| The model claims `gh is not available` despite the inline diff | Irrelevant to the outcome: the diff is embedded in the preamble regardless of model claims, and `Bash` is no longer in the allowlist | Nothing to do — the input path is host-supplied; the Ginkgo wiring row locks it |
| Hallucination detection regresses (wrong hallucination object) | The fabricated-hallucination Ginkgo row fails | Fix detection against the inline diff; do not revert to a `gh` shell-out |
| Preamble with a very large diff inflates the verifier context (resource exhaustion) | Verifier still runs for normal diffs; an oversized PR may fail the spawn | Escalate explicitly to `human_review` for oversized PRs (existing `MAX_ADDITIONS`/`MAX_CHANGED_FILES` parking already covers the extreme); if a mid-size diff OOMs, add truncation in a follow-up spec |
| `callVerifier` accidentally modified during the fix | The review persistence poll behaves differently | Keep it untouched — the fix targets only the `s.runner` call |
| New image not deployed before verification | Rung-2/Rung-3 `deploy_check` mismatch flags it (pre-fix env) | Deploy v0.6.4 (dev then prod), bump both version sources, re-run the ladder |
| The verify PR draws an LGTM/APPROVE review | The verifier never runs — the pod-log greps are vacuously empty | Amend the PR with a stronger defect so the posted review is non-LGTM, then re-check |

# Suggested Decomposition

Prompts should be generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Core fix: embed diff + posted comments in the verifier preamble (`pkg/steps_review.go`), consume the inline diff in `pkg/prompts/review_workflow.md`, drop both `Bash(gh pr ...)` entries from `reviewTools` (`pkg/factory/factory.go`) | 1, 2, 3, 6 | 1, 5 | — |
| 2 | Regression-lock Ginkgo tests: wiring (diff + comments present in runner call), clean APPROVE passes, fabricated hallucination fails with the correct hallucination object | 4, 5 | 2, 3, 4 | prompt 1 (tests assert the wiring it lands) |

Rationale: prompt 1 ships the behavior change, prompt 2 locks it with the fake-runner tests — the tests must come after the wiring exists, and both must land before the operator verification ladder (Rung-2/3) runs against the deployed image.

## Verification Result

**Verified:** 2026-08-30T16:05:21Z (HEAD 4f7989c)
**Binary:** installed `dark-factory` (spec targets `github.com/bborbe/github-pr-review-agent`, not dark-factory itself)
**Scenario:** AC 1-5 re-derived in-repo incl. a live regression-lock break/restore; AC 6/7 re-derived from live dev+prod Job specs, pod logs and durable task files — the spec's transcriptions were not taken on trust.
**Evidence:**
- deploy_check: dev + prod `config.agent.benjamin-borbe.de/github-pr-review-agent` `.spec.image` = `docker.prod.nuke.benjamin-borbe.de:443/bborbe/github-pr-review-agent:v0.6.5` (both match `deploy_target`)
- AC 1: `reviewTools = {"Read", "Grep"}`; `planningTools` still holds `Bash(gh pr view:*)`/`Bash(gh pr diff:*)`; `factory_test.go` "grants no Bash and no gh pr entries" locks it. AC 5: `grep -c 'gh pr diff' pkg/prompts/review_workflow.md` = 0; `inline diff` at lines 22, 24
- AC 2-4 regression lock re-proven live: early-returning `buildVerifierPreamble` before the envContext wiring → `Ran 372 of 372 ... 368 Passed | 4 Failed`, exactly the 4 "verifier diff embedding" rows; file restored byte-identical (`git diff -- pkg/steps_review.go` empty) → `go test -count=1 ./pkg/...` green; `make precommit` → `ready to commit`
- AC 6 (dev, `bborbe/go-skeleton#99`, Job `pr-reviewer-agent-f76c4f41-20260830155342`): Job spec image `…:v0.6.5`; posted review `CHANGES_REQUESTED` 15:56:01Z at headRefOid `60b58543…` (non-LGTM → verifier spawned); verifier CLI `--allowedTools Read,Grep` (no Bash/gh) at `claude-runner.go:117`; **zero `log-tool-use` lines after that spawn**; `grep -c 'gh is not available'` = 0; `grep -c 'skipping inline diff'` = 0 and no `fetch PR diff failed` → `envContext["PR Diff"]` populated; `"verdict": "pass"`, `"hallucinations": []`, `{"Status":"done","NextPhase":"done"}`; task file `phase: done`, `status: completed`, `grep -c '"verdict": "pass"'` = 1
- AC 7 (prod, `bborbe/nuke#121`, Job `pr-reviewer-agent-045d3a85-20260830154924`): image `…:v0.6.5`; review verdict `request-changes`; verifier `--allowedTools Read,Grep`, zero tool calls after spawn; `gh is not available` = 0; `skipping inline diff` = 0; verifier wrote *"every cited file+line in the review comments maps to actual content in the inline diff"* and named `values-prod.yaml:36` — cross-checked against the real `gh pr diff 121`, whose `dark-factory/values-prod.yaml` hunk `@@ -33,9 +33,10 @@` covers line 36. Positive proof of host-supplied diff: no tool could have fetched it. `{"Status":"done","NextPhase":"done"}`; task file `phase: done`, `status: completed`, `grep -c '"verdict": "pass"'` = 1
- AC 7 repo substitution re-checked against the live config, not the spec's prose: prod `REPO_ALLOWLIST` = `github.com/bborbe/*,!github.com/bborbe/go-skeleton`, so `bborbe/nuke` is the prod allowlist repo under the same wildcard as `dark-factory`; the defect is in repo-agnostic verifier code. Accepted.
- `go-skeleton#99` being an intentionally defective throwaway does **not** invalidate AC 6: the spec's own § Verification prescribes "open a small PR with a clear defect on github.com/bborbe/go-skeleton", and the Failure Modes row for an LGTM review prescribes strengthening the defect. Merge status is irrelevant — the verifier path runs at review time.
**Correction to `# Verification Status`:** the AC 6 bullet's "Zero `Read` tool calls in the entire job" is inaccurate — the review-phase spawn made 3 `[read]` calls (log lines 106, 108, 126). The load-bearing claim holds and is what was verified: **zero `Read` (and zero of any tool) calls in the ai_review verifier spawn**.
**Verdict:** PASS
