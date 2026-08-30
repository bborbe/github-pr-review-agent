---
status: approved
spec: [004-bug-benign-gap-whitelist-demotes-clean-approve]
created: "2026-08-30T19:44:12Z"
queued: "2026-08-30T20:06:13Z"
branch: dark-factory/bug-benign-gap-whitelist-demotes-clean-approve
---

# Regression-lock the disposition gate and concretize the release/deploy docs

<summary>
- The exact `discord-assistant#37` run-2 review verdict is stored verbatim as a test fixture with `disposition: "not-an-issue"`: the gate lets the approve through and the verdict parses as `approve`, despite the concern prose containing "not verified"
- The same fixture with the disposition flipped to `not-verified` demotes — the pairs differ only in the enum value
- A prose-inert table proves three distinct organic explanation wordings (including one matching no former whitelist phrase) demote under `not-verified` and pass under `not-an-issue`, differing only in the enum value
- The spec-002 fixtures (the `security: rate-limit not verified` row and the `bborbe/nuke#73` MUST-tier positive control) are migrated to the object shape with `disposition: "not-verified"` and still demote
- The tests also exercise the real posting path end-to-end: an unexamined concern posts as a change request, an examined one posts as an approval
- The release doc gains the concrete deploy procedure (both `agent.tag` version sources, `BRANCH=dev` then `BRANCH=master`, and the `BRANCH=master`-not-`BRANCH=prod` trap) with a dated "verified against bborbe/nuke" pin, and the changelog gains a `fix:` entry naming the false-`CHANGES_REQUESTED` fix
</summary>

<objective>
Lock the disposition-based gate with Ginkgo regression tests (incident fixture, prose-inert table, migrated spec-002 fixtures, posting-boundary rows) so the false-`CHANGES_REQUESTED` bug cannot recur, and concretize the deploy procedure in the release doc plus the changelog entry, so the fix ships end-to-end.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Ginkgo v2 / Gomega, external `pkg_test` packages, testdata fixtures, changelog format).

Read these files fully:
- `pkg/verdict_unverified_test.go` — the table the previous prompt in this batch left behind: converted benign rows (object shape, `disposition: "not-an-issue"`), the kept legacy bare-string rows (including the MUST-tier `nuke#73` string row and the lowercase `security: rate-limit not verified` row), the new gate-behavior rows, and the `fence` helper. This prompt adds the incident rows, the prose-inert paired table, and migrates two rows to the object shape.
- `pkg/verdict_extraction_test.go` — the fixture-reading pattern to mirror: `body, err := os.ReadFile("testdata/"+fixture)` + `pkg.ParseVerdict(string(body))`.
- `pkg/testdata/review_pr6_clean_approve.md` — the fixture file format to match (prose header + trailing fenced ```json verdict block). The new incident fixture goes in the same directory.
- `pkg/steps_checkout_execution_test.go` — the `Describe("posting behavior")` block: the `buildMD` / `fixedTime` helpers and the two existing `Context("fail-closed gate when a concern is flagged not verified")` `It`s (~lines 514-564) that show the exact `pkg.PostAndRouteForTest(ctx, fakePoster, md, prURL, "", fixedTime, true)` + `_, req := fakePoster.PostArgsForCall(0)` / `req.Verdict` assertion pattern. The new object-shape rows mirror those.
- `docs/releasing-github-pr-review-agent.md` — the "## Deploy (image → cluster)" section. The paragraph that punts deploy specifics to the out-of-repo "Development Instructions page" (currently ~lines 54-57) is the one this prompt replaces.
- `docs/dod.md` — § Documentation: the `## Unreleased` section placement rule (below the preamble, above the newest `## vX.Y.Z`).
- `CHANGELOG.md` — there is currently NO `## Unreleased` section; the newest section is `## v0.6.5`.
- `docs/architecture.md` and `docs/pr-post-back.md` — read only to confirm (requirement 6) that neither describes the demotion rule or the verdict schema's `concerns_addressed` shape.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega table tests
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` entry format and prefix rules
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md`

Verified contracts (do not re-derive):
- `pkg.HasUnverifiedConcerns(reviewText string) bool` and `pkg.ParseVerdict(reviewText string) pkg.Result` — both already shipped by the previous prompts in this batch; `pkg.VerdictApprove` / `pkg.VerdictRequestChanges` are the binary verdict constants; `pkg.PostAndRouteForTest(ctx, prPoster, md, prURLStr, worktreePath, jobRunTime, funnelRan)` is the test-only posting entry point.
- The verbatim `discord-assistant#37` run-2 demoting entry (2026-08-30) and the examined-judgment prose above it (from the spec Reproduction section) are the load-bearing strings; quote them exactly (requirement 1).
- The existing posting gate rows use `funnelRan: true` so the funnel gate does not interfere; the object-shape rows must do the same.
- The existing `verdict_unverified_test.go` currently imports only `pkg`, `ginkgo/v2`, `gomega`; the incident rows need `os` and `strings` — add those imports.
</context>

<requirements>
1. **Create the incident fixture `pkg/testdata/review_discord_assistant_37_run2.md`.** Follow the structure of `pkg/testdata/review_pr6_clean_approve.md`: a short prose header (2-4 lines explaining it is the reconstructed run-2 verdict from the 2026-08-30 `bborbe/discord-assistant#37` false-`CHANGES_REQUESTED` incident, that the `concern`/`detail` text is verbatim from the incident, and that the surrounding review body is not reproduced because the gate only reads the verdict JSON) followed by a trailing fenced ```json block:
   ```json
   {
     "verdict": "approve",
     "summary": "The model examined every concern; the only flagged gap is a reasonable, explained one.",
     "comments": [],
     "concerns_addressed": [
       {
         "concern": "tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested",
         "disposition": "not-an-issue",
         "detail": "True: no test creates 200+ transcript files to verify the cap is respected. The test covers the age-window and label preference. This is a reasonable gap."
       }
     ]
   }
   ```
   The `concern` string MUST be exactly the verbatim run-2 entry (it deliberately contains the substring `not verified` — that is what makes this a regression lock: the disposition field must win over the prose). `comments`/`severity` play no part in the gate and may stay empty (spec Constraint). Do not paraphrase the quoted strings.

2. **Incident regression rows in `pkg/verdict_unverified_test.go`.** Add a new `DescribeTable` "incident regression (bborbe/discord-assistant#37 run 2)" that reads the fixture via `os.ReadFile("testdata/review_discord_assistant_37_run2.md")` and asserts `pkg.HasUnverifiedConcerns` on it, with two rows (AC 4):
   - the fixture as-is (`disposition: "not-an-issue"`) → `false`
   - the same body with the disposition flipped via `strings.ReplaceAll(string(body), "\"disposition\": \"not-an-issue\"", "\"disposition\": \"not-verified\"")` → `true`. Target the exact JSON field token, not the bare enum value — the fixture's prose header may itself mention `not-an-issue`, and a first-occurrence replace would flip the header instead of the JSON. Assert the substitution actually fired (`Expect(flipped).NotTo(Equal(string(body)))`) so a header/format drift fails loudly rather than silently testing the unflipped body.
   Also add one `It` asserting `pkg.ParseVerdict(string(body))` yields `pkg.VerdictApprove` on the as-is fixture (AC 4). Add the `os` and `strings` imports to the file.

3. **Prose-inert paired table in `pkg/verdict_unverified_test.go` (AC 5).** Add a `DescribeTable` over ≥ 3 distinct organic explanation wordings; each wording appears under BOTH `disposition: "not-an-issue"` (expected `false`) and `disposition: "not-verified"` (expected `true`) — the pairs differ ONLY in the enum value, never in the prose. Use the `fence` helper with `{"verdict":"approve","concerns_addressed":[{"concern":"<prose>","disposition":"<value>"}]}`. The wordings:
   - the incident run-2 gap phrasing: `tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested`
   - the nuke#68 cross-check phrasing (the 2026-08-24 whitelist escape): `correctness: could not be cross-checked against the actual controller code. Not verified.`
   - at least one invented phrasing matching no former whitelist entry, e.g. `performance: I inspected the vendored copy and the mutex is uncontended in this call graph` (none of the deleted whitelist phrases match it)

4. **Migrate the two spec-002 fixtures to the object shape (AC 6).** In `pkg/verdict_unverified_test.go`:
   - Rewrite the lowercase legacy row `{"verdict":"approve","concerns_addressed":["security: rate-limit not verified"]}` to the object shape `{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit not verified","disposition":"not-verified"}]}` — keep the FULL original prose in the `concern` field and keep the expectation `true`. Do not truncate the concern to `security: rate-limit`: the previous prompt already added a row with that exact truncated text, and shortening this one would collapse the spec-002 `:26` fixture into an indistinguishable duplicate. Keeping the prose also strengthens the lock — the row proves that prose reading "not verified" AND `disposition: not-verified` still demotes. Carry over the row's spec-002 regression comment.
   - Rewrite the MUST-tier blocker string row (`bborbe/nuke#73` positive control, the `correctness: expression references github_build_watcher_rate_limit_remaining — not verified: build-watcher source not present in this monorepo, metric existence cannot be confirmed; without it the alerts will never fire. Must verify metric is exported before deploying.` string) to the object shape with the full verbatim text as `concern` and `"disposition":"not-verified"` — expected stays `true`.
   Keep the rows' regression comments (nuke#73 = MUST-tier positive control must still fail-close). Legacy coverage does not disappear: the uppercase `NOT VERIFIED` and `unverified` variant rows remain bare strings and cover AC 8 after this migration.

5. **Posting-boundary rows in `pkg/steps_checkout_execution_test.go`.** Inside the existing `Context("fail-closed gate when a concern is flagged not verified")` (or a new sibling `Context` with the same `buildMD`/`fixedTime`/`funnelRan: true` pattern), add two `It`s mirroring the existing rows:
   - Object-shape `not-verified` demotes: a `## Review` whose fenced JSON is `{"verdict":"approve","reason":"looks ok","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not-verified"}]}` → `fakePoster.PostArgsForCall(0)` `req.Verdict` equals `pkg.VerdictRequestChanges`.
   - Object-shape `not-an-issue` passes (the incident shape): a `## Review` whose fenced JSON is `{"verdict":"approve","reason":"looks ok","concerns_addressed":[{"concern":"tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested","disposition":"not-an-issue"}]}` → `req.Verdict` equals `pkg.VerdictApprove`. This row is the posting-level regression lock for the incident.

6. **Reference-doc check (spec Constraint).** Grep `docs/architecture.md` and `docs/pr-post-back.md` for the demotion rule and the verdict schema's `concerns_addressed` shape. The only `concerns_addressed` reference is `docs/architecture.md:55` (the ai_review consistency check: "appears in `concerns_addressed` as explicitly non-issue"), which remains accurate for object entries with `disposition: "not-an-issue"` — it describes neither the demotion rule nor the entry shape. Also grep `README.md` for `concerns_addressed` / the demotion rule (`docs/dod.md` § Documentation requires a README update when review/verdict behavior changes). `README.md` documents only the simplified `{"verdict", "reason"}` shape and never mentions `concerns_addressed` or the fail-closed rule, so no change is needed there either. Make NO changes to `docs/architecture.md`, `docs/pr-post-back.md`, or `README.md`; note all three conclusions in the completion report.

7. **Update `CHANGELOG.md` (AC 7).** There is currently no `## Unreleased` section. Create it below the preamble and above `## v0.6.5` (per `docs/dod.md`; final order `# Changelog` → preamble → `## Unreleased` → `## v0.6.5`). Add exactly one bullet:
   ```markdown
   - fix: make the `concerns_addressed` disposition a structured field (`addressed` | `not-an-issue` | `not-verified`) and read it directly in `HasUnverifiedConcerns` — an `approve` whose concerns were all examined now posts as `APPROVED` no matter how the model worded its explanations, ending the recurring false `CHANGES_REQUESTED` on clean approves (observed 2026-08-24 bborbe/nuke#68 and 2026-08-30 bborbe/discord-assistant#37); legacy bare-string entries still demote on a `not verified` substring
   ```
   The bullet must contain `CHANGES_REQUESTED` (the AC 7 evidence greps for it, case-insensitive). `fix:` classifies as a patch bump.

8. **Concretize the deploy procedure in `docs/releasing-github-pr-review-agent.md` (AC 9).** Replace the paragraph under "## Deploy (image → cluster)" that punts to the out-of-repo "Development Instructions page" (the text "Deploy specifics (version pin, dev vs prod, mirrored-agent apply) live on the service's **Development Instructions** page — read it before deploying; recalled paths go stale after monorepo → standalone splits. See the Development Guide ...") with the following content, written as normal prose + a fenced bash block (no nested fences):
   - Paragraph: "Deploy specifics — both version sources live in `~/Documents/workspaces/nuke/github-pr-reviewer/`: (1) `values-dev.yaml` and `values-prod.yaml` — the `agent.tag` field in each; (2) `Makefile` — the `AGENT_TAG_dev` and `AGENT_TAG_prod` variables. Bump BOTH sources in lockstep on every deploy, then mirror + apply dev first, then prod:"
   - Fenced bash block:
     ```bash
     cd ~/Documents/workspaces/nuke/github-pr-reviewer
     BRANCH=dev make mirror && BRANCH=dev make apply
     BRANCH=master make mirror && BRANCH=master make apply
     ```
   - Paragraph: "**Prod is `BRANCH=master`, not `BRANCH=prod`.** Using `BRANCH=prod` silently targets the wrong channel."
   - Blockquote: "Facts in this section last verified against bborbe/nuke 2026-08-30. Re-check against the real repo on each deploy and update this date; if the values files are renamed or `AGENT_TAG_*` removed, refresh this procedure before deploying."
   Keep the surrounding content intact (the `VERSION=vX.Y.Z make buca` image-build line, the tag/release flow, the "Do NOT rename `## Unreleased`" / "Do NOT create a local tag" warnings). The three AC 9 greps must hold: `BRANCH=master`, `values-prod.yaml`, and `verified against bborbe/nuke`.

9. **Self-check before finishing:** re-run `<verification>` and confirm it passes; walk AC 4, 5, 6, 7, 9 against the change. In particular confirm the incident `not-an-issue` fixture row fails if `HasUnverifiedConcerns` regressed to the old regex gate (its `concern` prose contains `not verified`), and the posting-boundary `not-an-issue` row posts `VerdictApprove`.
</requirements>

<constraints>
- Tests use Ginkgo v2 / Gomega with the existing table-driven pattern in `pkg/verdict_unverified_test.go` and the existing `PostAndRouteForTest` pattern in `pkg/steps_checkout_execution_test.go` — no live Claude, no network (spec Constraint).
- The incident body is stored verbatim as a testdata fixture, not paraphrased inline (spec Constraint). Only the disposition value is transformed for the negative row.
- Do NOT modify production code in this prompt: `pkg/verdict.go`, `pkg/steps_checkout_execution.go`, and all `pkg/prompts/*` are already shipped by the earlier prompts of this batch and stay untouched. The only non-test files touched are `CHANGELOG.md` and `docs/releasing-github-pr-review-agent.md`.
- Do NOT touch `pkg/prompts/review_output-format.md`, `pkg/prompts/review_workflow.md`, or `pkg/prompts/planning_*` (the ai_review/planning phases are separate and unchanged — spec Constraint).
- `docs/architecture.md` and `docs/pr-post-back.md` are only checked (requirement 6), not edited.
- The spec-002 fixtures are migrated to the object shape, not inverted; their expectations stay `true` (spec Constraint).
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- AC 4 evidence: `test -f pkg/testdata/review_discord_assistant_37_run2.md` exits 0 AND `grep -c 'gap is reasonable to leave untested' pkg/testdata/review_discord_assistant_37_run2.md` returns 1 (the verbatim concern) AND `grep -c 'review_discord_assistant_37_run2.md' pkg/verdict_unverified_test.go` returns ≥ 1 (the rows read it).
- AC 5 evidence: `grep -c 'gap is reasonable to leave untested' pkg/verdict_unverified_test.go` returns ≥ 2 (both polarities of wording 1) AND `grep -c 'could not be cross-checked against the actual controller code' pkg/verdict_unverified_test.go` returns ≥ 2 (wording 2) AND `grep -c 'mutex is uncontended in this call graph' pkg/verdict_unverified_test.go` returns 2 (wording 3, the invented phrasing, one row per disposition) — each wording appears exactly twice, differing only in the enum value.
- AC 6 evidence: `grep -c 'Must verify metric is exported before deploying\.","disposition":"not-verified"' pkg/verdict_unverified_test.go` returns 1 — the nuke#73 positive control is now object-shaped and still demotes. AND `grep -c 'security: rate-limit not verified","disposition":"not-verified"' pkg/verdict_unverified_test.go` returns 1 — the spec-002 `:26` fixture is migrated, not dropped.
- AC 7 evidence: `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'changes_requested'` returns ≥ 1.
- AC 9 evidence: `grep -c 'BRANCH=master' docs/releasing-github-pr-review-agent.md` returns ≥ 1 AND `grep -c 'values-prod.yaml' docs/releasing-github-pr-review-agent.md` returns ≥ 1 AND `grep -c 'verified against bborbe/nuke' docs/releasing-github-pr-review-agent.md` returns ≥ 1.
- `grep -n 'disposition' pkg/steps_checkout_execution_test.go` — shows the two posting-boundary object rows.
- `go test -mod=mod ./pkg/... -count=1` — must pass, including the incident rows, the prose-inert table, the migrated spec-002 rows, and the posting-boundary rows.
</verification>

<!-- AUDITOR NOTES
1. AC 4's regression-lock property: the incident fixture's `concern` prose contains the literal "not verified" substring. Under the old regex gate that prose demoted the approve; with `disposition: "not-an-issue"` the gate must pass it. Requirement 9 asks the agent to confirm this property holds (a regression of `HasUnverifiedConcerns` to prose matching flips the `false` row to fail).
2. The fixture `comments` array is empty even though the real run-2 review carried one `nit` comment — `comments`/`severity` play no part in the demotion decision (spec Constraint), and the comment's file/line/message are not in the spec, so they are intentionally not fabricated.
3. AC 8 (legacy-string row) continues to be covered after the migration in requirement 4 by the rows that remain bare strings (uppercase `NOT VERIFIED`, `unverified` variant) plus the posting-boundary legacy rows in `pkg/steps_checkout_execution_test.go`.
-->
