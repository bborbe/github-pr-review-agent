---
status: approved
spec: [005-blocking-verdict]
created: "2026-09-08T15:43:03Z"
queued: "2026-09-08T15:57:25Z"
branch: dark-factory/blocking-verdict
---

# Regression-lock the blocking verdict composition and add the changelog entry

<summary>
- A new acceptance table in the verdict tests locks the full verdict decision (verdict parsing plus the blocking override) across all four acceptance cases: approve-with-debt stays approve, approve-plus-blocking-finding flips to request-changes, the missing-field severity fallback works in both directions, and the four unparseable-verdict paths stay request-changes
- The table is the revert-test the spec's acceptance criterion demands: deleting the blocking override flips the blocking-comment row and the two severity-fallback rows while the four unparseable-verdict rows stay green
- The changelog gains an `## Unreleased` entry in the correct position (below the preamble, above the newest release) naming the decoupling as a feature
- The final precommit sweep runs the full batch after the schema, gate, and ai_review re-base are all in place
</summary>

<objective>
Lock the new blocking verdict behavior with the revert-testable acceptance table the spec requires, record the change in the changelog, and run the final precommit sweep over the whole batch.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Ginkgo v2 / Gomega table style, changelog format).

Read these files fully:
- `pkg/verdict_test.go` — the file this prompt extends. Note the existing `DescribeTable("verdict spelling and case normalisation", ...)` (the "spec-030" table, ~line 742) — its row function is `func(reviewText string, expectedVerdict pkg.Verdict)` and it uses bare single-line JSON bodies. The new table goes at the end of the file, after the "ParseVerdict end-anchored window regression" `Describe` (~line 902).
- `pkg/verdict.go` — the symbols the table exercises: `ParseVerdict`, `ApplyBlockingGate`, `Result`, `VerdictApprove`, `VerdictRequestChanges`, `ReasonBlockingFindingPresent`. `ApplyBlockingGate(verdict Result, reviewText string) Result` converts an approve carrying any blocking comment to `request-changes` with `ReasonBlockingFindingPresent`; every other verdict passes through unchanged. This was shipped by prompt 2 of this batch — read it to confirm the exact signature before writing the table.
- `CHANGELOG.md` — currently ends its preamble at `The format is based on [Keep a Changelog]...` and goes straight to `## v0.6.11`. There is NO `## Unreleased` section yet.
- `docs/dod.md` — the changelog placement rule: create `## Unreleased` below the preamble block and above the newest `## vX.Y.Z` section, never between the `# Changelog` title and the preamble.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega table tests
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — entry format and prefix rules
</context>

<requirements>
1. **Add the acceptance table to `pkg/verdict_test.go`** (at the end of the file, after the last `Describe`). The table's row function calls the production composition — `pkg.ApplyBlockingGate(pkg.ParseVerdict(reviewText), reviewText)` — so reverting the blocking gate (spec AC 5 evidence) flips the rows that depend on it:
   ```go
   var _ = Describe("Blocking verdict roll-up (spec-005)", func() {
   	DescribeTable("composes ParseVerdict with the blocking gate",
   		func(reviewText string, expectedVerdict pkg.Verdict, expectedReason string) {
   			result := pkg.ApplyBlockingGate(pkg.ParseVerdict(reviewText), reviewText)
   			Expect(result.Verdict).To(Equal(expectedVerdict))
   			if expectedReason != "" {
   				Expect(result.Reason).To(Equal(expectedReason))
   			}
   		},
   		// (a) approve + critical-severity comment marked blocking:false — pre-existing debt
   		Entry("approve + critical blocking:false (pre-existing debt) → approve",
   			`{"verdict":"approve","reason":"pre-existing debt","comments":[{"file":"main.go","line":5,"severity":"critical","blocking":false,"message":"debt on an untouched line"}]}`,
   			pkg.VerdictApprove,
   			"pre-existing debt",
   		),
   		// (b) comment blocking:true while the model verdict is approve → request-changes
   		Entry("approve + blocking:true → request-changes (ReasonBlockingFindingPresent)",
   			`{"verdict":"approve","reason":"looks good","comments":[{"file":"main.go","line":9,"severity":"nit","blocking":true,"blocking_reason":"isHealthy() is inverted","message":"real defect"}]}`,
   			pkg.VerdictRequestChanges,
   			pkg.ReasonBlockingFindingPresent,
   		),
   		// (c) blocking field absent → severity fallback: critical/major block, nit/minor do not
   		Entry("approve + absent blocking critical → request-changes (severity fallback)",
   			`{"verdict":"approve","reason":"ok","comments":[{"file":"main.go","line":5,"severity":"critical","message":"..."}]}`,
   			pkg.VerdictRequestChanges,
   			pkg.ReasonBlockingFindingPresent,
   		),
   		Entry("approve + absent blocking major → request-changes (severity fallback)",
   			`{"verdict":"approve","reason":"ok","comments":[{"file":"main.go","line":5,"severity":"major","message":"..."}]}`,
   			pkg.VerdictRequestChanges,
   			pkg.ReasonBlockingFindingPresent,
   		),
   		Entry("approve + absent blocking nit → approve",
   			`{"verdict":"approve","reason":"ok","comments":[{"file":"main.go","line":5,"severity":"nit","message":"..."}]}`,
   			pkg.VerdictApprove,
   			"ok",
   		),
   		Entry("approve + absent blocking minor → approve",
   			`{"verdict":"approve","reason":"ok","comments":[{"file":"main.go","line":5,"severity":"minor","message":"..."}]}`,
   			pkg.VerdictApprove,
   			"ok",
   		),
   		// (d) the four fail-closed paths still yield request-changes, blocking gate or not
   		Entry("empty text → request-changes (fail-closed)",
   			"",
   			pkg.VerdictRequestChanges,
   			"empty review text",
   		),
   		Entry("no verdict block → request-changes (fail-closed)",
   			"### Must Fix\n\n- something",
   			pkg.VerdictRequestChanges,
   			"no verdict block",
   		),
   		Entry("malformed JSON → request-changes (fail-closed)",
   			`{"verdict": invalid}`,
   			pkg.VerdictRequestChanges,
   			"",
   		),
   		Entry("unknown verdict → request-changes (fail-closed)",
   			`{"verdict":"comment"}`,
   			pkg.VerdictRequestChanges,
   			"unknown verdict: comment",
   		),
   	)
   })
   ```
   Notes:
   - Use bare single-line JSON bodies (like the existing spec-030 table rows) — `ParseVerdict` finds them via the end-anchored brace walk.
   - The `expectedReason == ""` convention in the row function exists because the malformed-JSON fail-closed reason embeds the `json` error text, which is not stable; that row asserts the verdict only. Every other row asserts both.
   - Do NOT modify the existing spec-030 table or any other existing Describe in the file.

2. **Add the `## Unreleased` changelog entry to `CHANGELOG.md`.** Create the section below the preamble (`...adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).`) and above `## v0.6.11`, exactly:
   ```markdown
   ## Unreleased

   - feat: decouple the merge verdict from severity — each review comment now carries a `blocking` flag (with `blocking_reason` when set); the execution prompt marks a finding blocking only on concrete evidence (build/test breakage, production regression, security defect, correctness defect, merge-gate requirement, functional no-op), and the Go side fail-closes an `approve` that carries any blocking comment to `request-changes` (`ReasonBlockingFindingPresent`), with comments missing the new field falling back to the old severity roll-up so pre-existing debt on lines a PR did not touch no longer blocks it; ai_review's consistency check is re-keyed from severity to blocking and its hallucination check now keys on the cited file's presence in the diff's changed files
   ```
   Final section order must be: `# Changelog` → preamble → `## Unreleased` → `## v0.6.11` (newest first). Do not rename or reorder any existing `## vX.Y.Z` section.

3. **Self-check before finishing:** re-run `<verification>` and confirm it passes; walk AC 5 (all table rows green, and the revert logic: reverting `ApplyBlockingGate` to `return verdict` flips the blocking-true row and the absent-blocking critical/major rows while the four fail-closed rows stay green), AC 6 (the chain-precedence rows already in place from prompt 2), and AC 7 (the changelog grep).
</requirements>

<constraints>
- This prompt is confined to `pkg/verdict_test.go` and `CHANGELOG.md`. Do NOT touch `pkg/verdict.go`, `pkg/steps_checkout_execution.go`, `pkg/prompts/*`, or `docs/architecture.md` — the gate, the schema, and the ai_review re-base were shipped by prompts 1-3 of this batch; this prompt only locks them.
- The table must exercise the PRODUCTION `ApplyBlockingGate` (not an inlined copy of the gate logic) — the spec AC 5 revert-test evidence depends on the rows going through the real function.
- Do NOT change the `ReasonBlockingFindingPresent` string literal, the gate's behavior, or any ParseVerdict fail-closed reason.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

- AC 5 evidence: `go test -mod=mod ./pkg/... -count=1` exits 0 with the new `Blocking verdict roll-up (spec-005)` table green. Sanity-check the revert-test property: temporarily change `ApplyBlockingGate` to `return verdict`, re-run `go test -mod=mod ./pkg/... -count=1`, confirm the "approve + blocking:true" row and the two "absent blocking critical/major" rows FAIL while the four fail-closed rows still pass, then REVERT the temporary change and confirm the full suite is green again. (Do not leave the reverted gate in the tree.)
- AC 7 evidence: `sed -n '/## Unreleased/,/## v/p' CHANGELOG.md | grep -ci 'blocking'` returns ≥ 1.
- `grep -n 'Blocking verdict roll-up (spec-005)' pkg/verdict_test.go` — shows the new table.
- `grep -n '## Unreleased' CHANGELOG.md` — shows the new section in the correct position (verify it sits between the preamble and `## v0.6.11`).
</verification>

<!-- AUDITOR NOTES
1. AC 5's "reverting the blocking gate flips rows (b) and the (c)-critical row while rows (d) stay green" is exactly why the table row function calls `pkg.ApplyBlockingGate(pkg.ParseVerdict(reviewText), reviewText)` and why prompt 2 shipped the gate as a single exported pure function — the table is the revert-test. The temporary-revert check in `<verification>` is the auditor's manual confirmation; leave the production gate intact.
2. The malformed-JSON fail-closed row asserts the verdict only (expectedReason "") because the reason embeds the json error text and is not stable; the verdict assertion is what the AC's "still yield request-changes" requires.
3. The changelog prefix is `feat:` (new feature → minor bump) per changelog-guide.md; the entry names the observable behavior, not file paths.
-->
