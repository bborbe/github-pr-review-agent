---
status: completed
approved: "2026-08-30T19:38:49Z"
generating: "2026-08-30T19:39:18Z"
prompted: "2026-08-30T19:56:51Z"
verifying: "2026-08-30T20:23:41Z"
completed: "2026-08-30T20:42:11Z"
branch: dark-factory/bug-benign-gap-whitelist-demotes-clean-approve
---

# Summary

- `concerns_addressed` is a list of free-text strings whose first words are supposed to carry one of three dispositions — `addressed`, `not an issue`, `not verified` (`pkg/prompts/execution_output-format.md:29-35`). The disposition and the model's explanatory prose share one field.
- `HasUnverifiedConcerns` (`pkg/verdict.go:299`) therefore has to *guess* the disposition by regex: `not verified|unverified` to detect it, then `benignGapPattern` — a hand-maintained whitelist of "benign explanation" phrasings — to decide whether to believe it.
- The model routinely writes an examined judgment under the `not verified` label. When its wording falls outside the whitelist, a clean `approve` posts as `CHANGES_REQUESTED`. Observed 2026-08-24 (`bborbe/nuke#68`) and 2026-08-30 (`bborbe/discord-assistant#37`). v0.6.3 "fixed" it by replacing one prose whitelist with another.
- Fix: make the disposition a **structured field**. `concerns_addressed` becomes a list of objects with a three-value `disposition` enum; the gate reads that field and both prose regexes are deleted. Sharpen the prompt so the enum values are mutually exclusive and `not-verified` means only "I never looked".
- The incomplete-review protection from spec 002 is **preserved exactly** — only its encoding changes, from guessed-by-regex to read-from-a-field.

# Problem

The PR-review bot's whole value is that its verdict can be trusted without a human reading it. A false `CHANGES_REQUESTED` breaks that: it blocks a PR the bot actually approved, forces a manual admin-merge, and — because the trigger is model wording rather than code state — reproduces non-deterministically across identical commits, so the operator cannot tell a real rejection from a parser artifact without opening the review body. The standing workaround (force a re-review, then merge on the body verdict) trains operators to distrust the bot's posted state. Each round's fix has added phrases to a regex that enumerates natural language; that enumeration cannot converge, so the next recurrence is scheduled rather than avoided. The root cause is upstream of the regex: a schema that asks one free-text field to carry both a categorical decision and its justification, then asks the parser to separate them again.

# Goal

The review's verdict JSON states each concern's disposition in a dedicated enum field. The demotion gate reads that field and nothing else — an `approve` carrying a `not-verified` disposition is fail-closed to `request-changes` exactly as today; an `approve` whose concerns are all `addressed` or `not-an-issue` posts as `APPROVED`, no matter how the model worded its explanation. No regular expression matches concern prose anywhere in the demotion path.

# Reproduction

**Observed incident** — `bborbe/discord-assistant#37`, 2026-08-30, agent at `v0.6.5`.

Three bot reviews exist on the PR (`gh api repos/bborbe/discord-assistant/pulls/37/reviews`):

| Review id | SHA | Event | Body verdict | `comments[]` |
|---|---|---|---|---|
| `5061129415` 14:55:38Z | `f7ce89a` | `DISMISSED` | — | — |
| `5061143826` 15:02:37Z | `dc580aa` | **`CHANGES_REQUESTED`** | `"verdict": "approve"` | 1 entry, `"severity": "nit"` |
| `5061154036` 15:07:43Z | `dc580aa` | `APPROVED` | `"verdict": "approve"` | `[]` |

The two `dc580aa` reviews ran the same binary against the same commit and posted opposite events. The only difference is model wording in `concerns_addressed`.

Run 2's demoting entry, verbatim:

```
tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested
```

The review body above it shows the model **did examine** the concern: *"True: no test creates 200+ transcript files to verify the cap is respected. The test covers the age-window and label preference. This is a reasonable gap."* Per the schema (`pkg/prompts/execution_output-format.md:29-35`) that is the definition of `not an issue` — "examined and confirmed". `not verified` is defined narrowly as "investigation stopped at the time budget before the concern could be examined". The model applied the wrong label, and the free-text field made the two indistinguishable to the parser.

Trace through `HasUnverifiedConcerns` (`pkg/verdict.go:299`):

1. `unverifiedConcernPattern` (`pkg/verdict.go:262`) matches `not verified` → entry is considered.
2. `mustTierBlockerPattern` (`pkg/verdict.go:275`) does not match — the string has `requires`, the pattern has `required (before|to)`.
3. `benignGapPattern` (`pkg/verdict.go:286`) does not match — "gap is reasonable to leave untested" is not in the whitelist.
4. Falls through to the bare-unexamined-admission branch → `return true` → `postAndRoute` demotes the `approve` (`pkg/steps_checkout_execution.go:405`).

Run 3 labelled the same gaps `not an issue` / `not bugs` and contains no `not verified` substring, so step 1 never fires and the `approve` stands.

**Minimal mechanism (no cluster needed):** feed either body verbatim to `pkg.HasUnverifiedConcerns`. Run 2 returns `true`, run 3 returns `false`.

**Prior occurrence, same shape:** 2026-08-24, `bborbe/nuke#68`, agent v0.6.2 — phrase `"could not be cross-checked against the actual controller code. Not verified."` escaped the then-current whitelist; a re-review 5 minutes later posted `APPROVED`. Recorded in the `mustTierBlockerPattern` doc comment at `pkg/verdict.go:265-274`.

# Expected vs Actual

- **Expected:** an `approve` whose concerns were all examined posts as GitHub `APPROVED`. The gate exists to stop an *incomplete* review green-lighting a PR (`pkg/verdict.go:255-258`, spec 002 DB 4); a review that examined every concern is complete regardless of how it phrased the outcome.
- **Actual:** the review posts `CHANGES_REQUESTED` whenever the model's explanatory prose falls outside `benignGapPattern`. On `discord-assistant#37` the PR was blocked (`mergeStateStatus: BLOCKED`) until an operator force-re-reviewed and merged on the body verdict.

# Why this is a bug

The gate's stated contract (`pkg/verdict.go:290-298`) is that a concern explaining its gap as non-blocking passes. Run 2's entry explained its gap as non-blocking in plain English and was demoted anyway. The implementation cannot deliver its documented contract, because the schema hands it one field containing a category and a justification fused together, and no finite phrase list can separate them. That is a defect in the schema and the gate, not in the model's behaviour on this PR — though the model also mislabelled, which the prompt change addresses.

# Workaround

Operator-side: detect via `gh pr view <n> --json reviewDecision,reviews`, re-run the review trigger with `--force` once, and if the second review still inverts, merge on the body verdict. Costs a manual admin-merge per occurrence.

# Acceptance Criteria

- [ ] The verdict schema declares `concerns_addressed` as a list of objects with a required `disposition` field constrained to `addressed | not-an-issue | not-verified`, and states that `not-verified` means only that the concern was never examined — evidence, four mechanical greps on `pkg/prompts/execution_output-format.md`: `grep -c 'disposition'` ≥ 1, `grep -c 'not-an-issue'` ≥ 1, `grep -c 'not-verified'` ≥ 1, `grep -c 'addressed'` ≥ 1
- [ ] The time-budget footer uses the same enum vocabulary and states the values are mutually exclusive (examined ⇒ never `not-verified`) — evidence: `grep -c 'not-an-issue' pkg/prompts/execution.go` returns ≥ 1 AND `grep -c 'mutually exclusive' pkg/prompts/execution.go` returns ≥ 1
- [ ] No regular expression matches concern prose in the demotion path — evidence, **package-scoped** so the patterns cannot survive by being moved to a sibling file: `grep -rnE 'benignGapPattern|mustTierBlockerPattern|unverifiedConcernPattern' pkg/` returns 0 lines, AND `grep -c 'MustCompile' pkg/verdict.go` returns exactly **1** — `verdictFieldRegexp` at `pkg/verdict.go:39`, which `ParseVerdict` owns and Constraints freeze (negative file-content evidence for DB 4)
- [ ] Ginkgo regression-lock (the incident): the **verbatim** `discord-assistant#37` run-2 review body, migrated to the object shape with `disposition: not-an-issue`, is a testdata fixture; `HasUnverifiedConcerns` returns `false` for it and `ParseVerdict` yields `VerdictApprove`. The same fixture with `disposition: not-verified` returns `true`. Evidence: `go test ./pkg/...` exits 0 with both rows green; reverting the gate change flips the `false` row to fail
- [ ] Ginkgo regression-lock (prose is inert): a table over ≥ 3 distinct organic explanation wordings — including one invented phrasing matching no former whitelist entry — all carrying `disposition: not-an-issue`, none demoted; and the same three wordings under `disposition: not-verified`, all demoted — evidence: `go test ./pkg/...` exits 0; the pairs differ only in the enum value
- [ ] Ginkgo regression-lock (spec 002 contract preserved): the `bborbe/nuke#73` positive control (`pkg/verdict_unverified_test.go:77-79`) and the `security: rate-limit not verified` fixture (`:26`), both migrated to `disposition: not-verified`, still demote — evidence: `go test ./pkg/...` exits 0 with both rows green
- [ ] Legacy string entries still parse and preserve today's spec-002 behaviour: a `concerns_addressed` entry that is a bare string containing `not verified` demotes — evidence: `go test ./pkg/...` exits 0 with the legacy row green
- [ ] `make precommit` exits 0 in the repo root, and CHANGELOG has an entry under `## Unreleased` naming the false-`CHANGES_REQUESTED` fix — evidence: `make precommit` exit 0 AND `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'changes_requested'` returns ≥ 1
- [ ] `docs/releasing-github-pr-review-agent.md` carries the concrete deploy procedure (both `agent.tag` version sources under `~/Documents/workspaces/nuke/github-pr-reviewer/`, and the `BRANCH=master`-not-`BRANCH=prod` trap) instead of punting to an out-of-repo page, and pins when those out-of-repo facts were last checked — evidence: `grep -c 'BRANCH=master' docs/releasing-github-pr-review-agent.md` returns ≥ 1 AND `grep -c 'values-prod.yaml' docs/releasing-github-pr-review-agent.md` returns ≥ 1 AND `grep -c 'verified against bborbe/nuke' docs/releasing-github-pr-review-agent.md` returns ≥ 1
- [ ] **Post-Deploy (Rung-2):** on a dev-allowlist PR (`github.com/bborbe/go-skeleton`) whose review yields `"verdict": "approve"` with every concern dispositioned `addressed` or `not-an-issue`, the posted GitHub review state is `APPROVED` — evidence: `gh pr view <n> --repo bborbe/go-skeleton --json reviews --jq '[.reviews[] | select(.author.login=="ben-s-pull-request-reviewer")] | last | .state'` returns `APPROVED`, and `gh pr view <n> --repo bborbe/go-skeleton --json mergeStateStatus` is not `BLOCKED`.
  - `deploy_check:` `kubectlnukedev -n dev get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git fetch --tags -q; git describe --tags --abbrev=0 origin/master)`
- [ ] **Post-Deploy (Rung-3):** the same check on a prod-allowlist PR. Prod's allowlist is `github.com/bborbe/*,!github.com/bborbe/go-skeleton` — **`go-skeleton` is explicitly excluded from prod**, so the Rung-2 repo cannot be reused here or the review is silently skipped; pick another `bborbe/*` repo. Evidence: newest bot review `state` is `APPROVED`; and that Job's pod log contains 0 occurrences of the fail-closed reason — `kubectlnukeprod -n prod logs $(kubectlnukeprod -n prod get pods --sort-by=.metadata.creationTimestamp -l app=pr-reviewer-agent -o name | tail -1) --since=15m | grep -c 'concerns not verified'` returns 0.
  - `deploy_check:` `kubectlnukeprod -n prod get config.agent.benjamin-borbe.de github-pr-review-agent -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git fetch --tags -q; git describe --tags --abbrev=0 origin/master)`

# Verification

## Container-executable (runs inside the YOLO container at prompt time)

- `grep -c 'MustCompile' pkg/verdict.go` — returns exactly 1 (`verdictFieldRegexp` at `pkg/verdict.go:39`, which `ParseVerdict` owns and Constraints freeze) AND `grep -rnE 'benignGapPattern|mustTierBlockerPattern|unverifiedConcernPattern' pkg/` returns 0 lines
- `grep -n 'disposition' pkg/prompts/execution_output-format.md` — returns ≥ 3 lines with the three enum values
- `go test ./pkg/...` — incident, prose-inert, spec-002-preserved and legacy-string rows all green
- `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'changes_requested'` — returns ≥ 1
- `make precommit` — fmt, generate, test, lint, vet, vuln, license all clean

## Operator-executable (runs on the host after PR merge, verification ladder)

- Merge to master; let `github-releaser-agent` cut the tag (`.maintainer.yaml` is `release.autoRelease: true` — do NOT hand-tag). Read the tag with `git fetch --tags && git describe --tags --abbrev=0`. This repo ships **tags, not GitHub Releases** — `gh release list` returns empty by design (`docs/releasing-github-pr-review-agent.md`).
- Deploy per `docs/releasing-github-pr-review-agent.md`: bump both version sources in `~/Documents/workspaces/nuke/github-pr-reviewer/` (`values-dev.yaml` + `values-prod.yaml` `agent.tag`, and `Makefile` `AGENT_TAG_dev` / `AGENT_TAG_prod`), then `BRANCH=dev make mirror` + `BRANCH=dev make apply`, then `BRANCH=master make mirror` + `BRANCH=master make apply`. **Prod is `BRANCH=master`, not `BRANCH=prod`.**
- Run both `deploy_check` commands and confirm each equals `deploy_target` before collecting AC evidence.
- Open a trivial PR on `bborbe/go-skeleton` expected to draw a clean approve, trigger a review, then run the Rung-2 checks; repeat on a `bborbe/*` prod repo for Rung-3.

# Desired Behavior

1. `concerns_addressed` entries are objects carrying the concern text, a `disposition` enum (`addressed | not-an-issue | not-verified`), and free-text detail; the schema in `pkg/prompts/execution_output-format.md` declares this shape.
2. The prompt defines the three dispositions as mutually exclusive and states that a concern the model examined is `not-an-issue`, never `not-verified` — `not-verified` means only that the time budget stopped it before it looked.
3. `HasUnverifiedConcerns` returns true iff at least one entry's `disposition` is `not-verified`. Detail prose is never inspected.
4. `benignGapPattern` and `mustTierBlockerPattern` are both deleted; no regular expression matches concern prose anywhere in the demotion path.
5. A `concerns_addressed` entry that is a bare string falls back to today's substring detection, so spec 002's contract holds for legacy data — **implemented with `strings.Contains` on the lowercased entry, not a regex**, so AC 3's package-wide ban on the pattern variables still holds. This path is reachable, not speculative future-proofing: `reviewBody` is read from the persisted vault `## Review` section (`pkg/steps_checkout_execution.go:381-383`), so a task retried across an agent upgrade genuinely re-parses pre-change data.
6. When a demotion occurs its `Result.Reason` remains `ReasonConcernsNotVerified`, so `isFailClosedReason` logging and existing diagnostics are unaffected.

# Constraints

- **Spec 002's contract is preserved, not superseded.** `specs/in-progress/002-pr-reviewer-soft-time-budget-and-salvage.md` (status `verifying`) DB 4 — "a review that flags any concern as not verified must not emit `approve`" — remains true; only the encoding changes from regex-guessed to field-read. The fixtures at `pkg/verdict_unverified_test.go:26` and `:77-79` are **migrated to the object shape, not inverted**; their expectations stay `true`.
- The change is confined to `pkg/verdict.go`, `pkg/prompts/execution_output-format.md`, `pkg/prompts/execution.go` (the time-budget footer), their tests, and `docs/releasing-github-pr-review-agent.md`. `ParseVerdict`'s block-finding, the verdict→event mapping in `pkg/githubposter/`, and the `autoApprove` gate must not change.
- The funnel gate (`ReasonFunnelDidNotRun`, `pkg/steps_checkout_execution.go:389`) is a separate mechanism and stays untouched, including its precedence over the unverified-concerns gate (the comment at `:404`).
- `comments[]` and `severity` play **no part** in the demotion decision. The severity vocabulary (`critical | major | minor | nit`) and the verdict roll-up at `pkg/prompts/execution.go:98-105` are unchanged; note that `minor` does not contribute to `request-changes`, so any future rule keyed on severity must not treat it as blocking.
- An entry whose `disposition` is absent or holds an unrecognised value is treated as `not-verified` (fail-safe): unknown input demotes rather than permits. An entry that is a bare string takes the DB 5 legacy path instead.
- `concerns_addressed` absent or empty ⇒ no entries ⇒ `HasUnverifiedConcerns` returns false. A malformed or missing verdict block also returns false, unchanged from today (no over-trigger).
- **Element-shape edge, stated explicitly:** an entry that is neither a JSON string nor a JSON object (a number, a nested array, a mixed list) is **not** a parse failure of the whole block — it is skipped as uninterpretable, and remaining entries are still evaluated. Only a `concerns_addressed` value that fails to unmarshal as a list at all falls back to today's permissive `false`. Add a regression row for a mixed list containing one valid `not-verified` object and one uninterpretable element: the entry demotes.
- **Residual risk, accepted and named:** the enum removes the parser's guesswork but cannot stop a model from selecting the wrong value. `discord-assistant#37` would be fixed only insofar as the sharpened prompt (DB 2) leads the model to pick `not-an-issue`. If mislabelling recurs after this ships, the next lever is a host-side cross-check (compare run elapsed time against the soft budget — a `not-verified` on a run that finished well inside the budget is provably a mislabel), **not** another prose pattern. Recorded here so the follow-up is chosen deliberately rather than rediscovered.
- Prompt and parser ship in one binary (`//go:embed execution_output-format.md`, `pkg/prompts/execution.go:20`), so there is no prompt/parser version skew; the legacy string path exists only for re-parsing task files written before this change.
- Tests use Ginkgo v2 / Gomega with the existing table-driven pattern in `pkg/verdict_unverified_test.go`; the incident body is stored verbatim as a fixture, not paraphrased inline.
- Errors use `github.com/bborbe/errors` with context wrapping; CHANGELOG entry lands under `## Unreleased` (per `docs/dod.md`).
- Reference docs: `docs/architecture.md` and `docs/pr-post-back.md` — update if either describes the demotion rule or the verdict schema.

# Failure Modes

| Trigger | Expected behavior | Detection | Recovery |
|---|---|---|---|
| Model marks an examined concern `not-verified` anyway | Demoted — same false `CHANGES_REQUESTED` as today | Review body shows an examined judgment under `disposition: not-verified` | Apply the elapsed-time cross-check named in Constraints; never re-add a prose pattern. Observable: a new regression row pairing a short run duration with `not-verified` |
| `disposition` absent or an unrecognised string | Treated as `not-verified` ⇒ demote (fail-safe) | Regression row asserts the unknown-value case demotes | None needed; the safe direction is chosen by construction |
| Task file written by an older binary is re-parsed | Legacy bare-string path applies today's substring rule | Legacy-string regression row | None needed; transient by construction (prompt and parser co-ship) |
| Verdict JSON malformed / no verdict block | `HasUnverifiedConcerns` returns false; `ParseVerdict`'s fail-closed path handles it | Existing `ParseVerdict` tests + `isFailClosedReason` logging | Unchanged from today |
| Fix deployed to dev but not prod (or vice versa) | Ladder refuses before AC evidence is collected | `deploy_check` ≠ `deploy_target` in `spec-verifier` Phase 0.5 | Deploy the missing stage; observable: re-run `deploy_check` and confirm it equals `deploy_target` |
| `gh` or the nuke dev/prod cluster unreachable during the ladder | Verification is **inconclusive, not failed** | The probe errors or returns empty rather than a value | Re-run when reachable; never mark the spec completed on an unreachable probe. (Spec 003 in this repo exists because a `gh`-unavailability false result was trusted.) |
| The verification PR draws a genuine `request-changes` | Rung-2/Rung-3 evidence is vacuous — the clean-approve path never ran | Newest review `state` is `CHANGES_REQUESTED` with a `critical`/`major` comment | Author a PR that draws a clean approve; do not weaken the AC to match the run |
| Review Job/pod garbage-collected before the verifier reads its log | Rung-3's log evidence is **inconclusive, not failed** | `kubectl … get pods -l app=pr-reviewer-agent` returns nothing for the run, or `logs` errors | Re-trigger the review and re-collect within the `--since` window; never record a 0-count from an absent pod as a pass |
| `bborbe/nuke` deploy layout drifts (values files renamed, `AGENT_TAG_*` removed) | The procedure written into `docs/releasing-github-pr-review-agent.md` goes stale silently | The doc's `verified against bborbe/nuke <date>` line ages; a deploy step fails against the real repo | Re-check against `~/Documents/workspaces/nuke/github-pr-reviewer/` and update the doc + its date line |

# Security / Abuse

The demotion decision becomes **entirely model-controlled** — one enum field, with no prose corroboration behind it. PR diff content is attacker-influenceable input to that model, so a crafted diff that steers the review into emitting `disposition: not-an-issue` would suppress the fail-closed path.

This is **not a regression**: today's `benignGapPattern` / `mustTierBlockerPattern` are equally steerable by the same input (a diff that induces whitelist-matching prose has the same effect), and the two defences that do not depend on model wording — the mechanical funnel gate (`ReasonFunnelDidNotRun`) and the `critical`/`major` → `request-changes` roll-up — are explicitly untouched by this change.

Declared out of scope. Recorded because this spec deliberately removes the last prose corroboration, so a future reader should know the property was considered rather than overlooked.

# Suggested Decomposition

Prompts should be generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Schema + prompt: object shape with the `disposition` enum in `execution_output-format.md`, mutually-exclusive wording in the time-budget footer | 1, 2 | 1, 2 | — |
| 2 | Gate rewrite: read `disposition`, delete both regexes, fail-safe unknown handling, legacy bare-string fallback | 3, 4, 5, 6 | 3, 8 | prompt 1 (schema defines the shape it parses) |
| 3 | Regression-lock tests + docs: incident fixture, prose-inert table, migrated spec-002 fixtures, legacy-string row; deploy procedure into `docs/releasing-github-pr-review-agent.md` | — | 4, 5, 6, 7, 9 | prompt 2 |
| — | Operator ladder (no prompt — runs on the host after merge) | — | 10, 11 | prompt 3 merged + deployed |

Rationale: the schema defines the contract, the gate consumes it, the tests lock it. Splitting schema from gate keeps each prompt's research surface to one file pair; the ladder is operator-executable and deliberately carries no prompt.
