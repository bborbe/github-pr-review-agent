---
status: completed
spec: [010-bug-review-body-disagrees-with-posted-event]
summary: Rewrote StripJSONVerdict to remove every verdict block via the shared findVerdictBlock locator, deleted the dead line-removal helpers, added the vault-cli#243 fixture plus AC1/AC2/AC8 tests, and documented the demotion contract with an Unreleased changelog entry.
execution_id: c1fb3e45-826f-5fb9-b58a-f8cc73282a59-exec-034-spec-010-strip-verdict-blocks
dark-factory-version: dev
created: "2026-11-04T17:49:28Z"
queued: "2026-11-04T18:01:08Z"
started: "2026-11-04T18:01:10Z"
completed: "2026-11-04T18:20:12Z"
branch: dark-factory/bug-review-body-disagrees-with-posted-event
---

<summary>
- The posted review body stops carrying the raw verdict JSON block.
- Pretty-printed, multi-line verdict blocks are removed whole, not only the compact single-line form.
- A chunked review loses every per-chunk verdict block and the aggregate block, while its chunk headings and prose survive.
- A verdict block the locator cannot cleanly delimit is left in place rather than half-removed, so the posted event stays correct.
- Existing single-line stripping and unrelated JSON examples in a review keep working unchanged.
- The posted-body contract document now states that a gate-demoted body opens with the demotion reason.
- The changelog records the fix under an Unreleased heading.
</summary>

<objective>
Make `StripJSONVerdict` remove every verdict block structurally, reusing the same block locator `ParseVerdict` already uses, so no verdict JSON reaches the posted review body. A clean approve's body is pure prose; a chunked body loses all of its blocks. This is the first of two prompts: this one fixes removal and documentation; the next prompt prepends the demotion line to a gate-demoted body.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Go 1.26, Ginkgo v2/Gomega, Counterfeiter mocks, `github.com/bborbe/errors`, `glog`).

Read these files before changing anything:
- `pkg/verdict.go` — current `StripJSONVerdict`, `findVerdictBlock`, `findFencedJSONVerdictBlock`, `findLastJSONVerdictBlock`, `ParseVerdict`, and the old line-removal helpers.
- `pkg/verdict_test.go` — the five existing `StripJSONVerdict` contexts that must stay unchanged, and the `ParseVerdict` contexts that share the locator.
- `pkg/chunk.go` — `MergeChunkReviews` calls `StripJSONVerdict` per chunk; its merged body ends in a synthesized verdict block.
- `pkg/steps_checkout_execution.go` — `postAndRoute` calls `StripJSONVerdict(reviewBody)`; do not change this file in this prompt.
- `pkg/testdata/review_bborbe_nuke_216_run1.md` — existing testdata fixture pattern.
- `docs/pr-post-back.md` — the posted-body contract.
- `CHANGELOG.md` — note there is no `## Unreleased` heading today.
- `docs/dod.md` — Definition of Done (changelog placement rules).

Reference these coding guides (they live in the YOLO container, not on the host):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>
1. **Replace the body of `StripJSONVerdict` in `pkg/verdict.go` with a locator-driven removal loop.** It must iterate `findVerdictBlock` until the locator reports no block remains, so every block in the body is removed — not only the last one. The locator is the reference implementation and must not be forked; reuse `findVerdictBlock` (which internally prefers a fenced ```` ```json ```` block and falls back to the brace walk).

   Reference implementation (valid Go; the tests are the contract if an edge case needs adjusting):

   ```go
   var blankRunRegexp = regexp.MustCompile(`\n{3,}`)

   // StripJSONVerdict removes every JSON verdict block (with its surrounding
   // ```json fence when present) from the review text. It iterates
   // findVerdictBlock until no block remains. A block the locator cannot
   // delimit is left in place (fail-open).
   func StripJSONVerdict(reviewText string) string {
   	result := reviewText
   	removed := false
   	for {
   		block, fenced, ok := findVerdictBlock(result)
   		if !ok || block == "" {
   			break
   		}
   		next, ok := removeVerdictBlock(result, block, fenced)
   		if !ok {
   			break
   		}
   		result = next
   		removed = true
   	}
   	if !removed {
   		return reviewText
   	}
   	return blankRunRegexp.ReplaceAllString(strings.TrimRight(result, "\n"), "\n\n")
   }

   // removeVerdictBlock removes one located block. For a bare block it removes
   // the block substring; for a fenced block it removes the opening ```json
   // line, the block lines, and the closing ``` line. Returns ok=false when the
   // fence cannot be delimited, so the caller fails open.
   func removeVerdictBlock(text, block string, fenced bool) (string, bool) {
   	idx := strings.LastIndex(text, block)
   	if idx < 0 {
   		return text, false
   	}
   	if !fenced {
   		return text[:idx] + text[idx+len(block):], true
   	}
   	lines := strings.Split(text, "\n")
   	startLine := strings.Count(text[:idx], "\n")
   	endLine := startLine + strings.Count(block, "\n")
   	if startLine == 0 || strings.TrimSpace(lines[startLine-1]) != "```json" {
   		return text, false
   	}
   	closeLine := endLine + 1
   	if closeLine >= len(lines) || strings.TrimSpace(lines[closeLine]) != "```" {
   		return text, false
   	}
   	remaining := make([]string, 0, len(lines)-(closeLine-startLine+2))
   	remaining = append(remaining, lines[:startLine-1]...)
   	remaining = append(remaining, lines[closeLine+1:]...)
   	return strings.Join(remaining, "\n"), true
   }
   ```

2. **Fail open, never half-remove.** If `findVerdictBlock` returns `ok=false`, or the located block's fence cannot be delimited, return the current text unchanged and stop. The posted event is derived from the verdict alone and stays correct either way. Do not validate the block's JSON in the removal path — removal is structural; a malformed-but-delimited block is still removed.

3. **Preserve the no-block contract.** A body with no verdict block must be returned byte-identical to its input. Do not trim or collapse anything when `removed == false`.

4. **Seam hygiene.** After at least one removal, collapse runs of three or more newlines to two (`\n{3,}` → `\n\n`) and trim trailing newlines. The existing test asserts the result does not end with `\n\n\n`; there must be no dangling ```` ``` ```` fence marker at any seam.

5. **Delete the now-dead line-removal helpers** from `pkg/verdict.go` so the `unused` linter stays green: `findVerdictLinesToRemove`, `calculateStartIndex`, `handleCodeFenceStart`, `handleCodeFenceEnd`, `containsVerdictJSON`, `processVerdictLine`, `isValidVerdictJSON`, `markCodeFenceLinesForRemoval`, `buildCleanedText`. Keep `jsonVerdict`, `verdictFieldRegexp`, `findVerdictBlock`, and its two finders — `ParseVerdict` still uses them. Update the stale `jsonVerdict` doc comment (`// jsonVerdict is used for unmarshaling JSON verdict blocks (legacy use by StripJSONVerdict)`) because `StripJSONVerdict` no longer uses it. After the change `grep -c 'containsVerdictJSON(' pkg/verdict.go` must print `0`.

6. **Add the verbatim reproduction fixture.** Create `pkg/testdata/review_bborbe_vault_cli_243.md` containing exactly the body below (save it as a normal text file with a trailing newline). This is the body of `bborbe/vault-cli#243` review `5363310206`; the container has no GitHub access, so the fixture is the reproduction. The three-space indent below is prompt formatting — strip it so every line starts at column 0 in the file.

   ````text
   The mock has 9 parameters matching the interface (actor as arg8, force as arg9). Let me now compile the findings and issue the verdict.

   Based on my review:

   1. **`--by` enforcement in cli.go:2537** — Uses `Changed("by")` (not the string value), so the "unknown" default doesn't trigger false positives on non-flag keys. ✅ Solid.

   2. **Atomicity of flag+provenance write** — `writeFlagProvenance` runs before any mutation (line 131), and if it returns error the whole function returns without calling `WriteTask`. The single `WriteTask` at line 164 persists all three keys together. ✅ Correct.

   3. **`flag_set_at` as bare `time.Time`** — Stored via `o.currentDateTime.Now().Time()` (line 234), which is a `time.Time` value. The test at line 718-732 verifies yaml.v3 renders it unquoted and round-trips correctly. ✅ Good.

   4. **`ClearFlag` removes all three keys** — `ClearFlag()` at task_frontmatter.go:194-198 deletes flag, flag_set_by, and flag_set_at. `ClearField` also handles this at line 213-216. ✅ Correct.

   5. **`flag_set_by`/`flag_set_at` direct write refusal** — `flagProvenanceSetRefusal` runs before any mutation, rejects the keys absolutely (--force doesn't bypass), writes nothing on refusal. ✅ Solid.

   6. **`NewFrontmatterSetOperation` now requires `currentDateTime`** — All callers updated (cli.go, tests, mocks). The mock was regenerated with the new signature. ✅ Correct.

   ```json
   {
     "verdict": "approve",
     "summary": "Flag provenance feature is well-implemented: --by enforcement uses Changed() so defaults don't break non-flag keys; flag+provenance are written atomically in a single WriteTask with no read window; blank actor is refused before mutation; direct provenance writes are refused absolutely; ClearFlag/ClearField remove all three keys together; the mock is regenerated with the new 9-param signature. All concerns from the plan are addressed.",
     "comments": [],
     "concerns_addressed": [
       {
         "concern": "security: new --by flag accepted only for flag key; blank actor refused - verify enforcement is solid",
         "disposition": "addressed",
         "detail": "cli.go:2537 uses cmd.Flags().Changed('by') (not the value), so --by default 'unknown' only applies when explicitly passed. Blank actor refusal is in writeFlagProvenance at frontmatter.go:226-231, before any mutation."
       },
       {
         "concern": "correctness: flag_set_at uses bare time.Time - timezone handling and YAML serialization need verification",
         "disposition": "addressed",
         "detail": "Stored as time.Time (frontmatter.go:234), test at line 718-732 verifies yaml.v3 renders it unquoted as '2026-09-30T08:00:00Z' and round-trips correctly."
       },
       {
         "concern": "correctness: WriteTaskCallCount must be exactly 1 on success, 0 on refusal - verify atomicity of flag+provenance write",
         "disposition": "addressed",
         "detail": "writeFlagProvenance runs before any mutation (line 131) and returns error without WriteTask on refusal. The single WriteTask at line 164 persists all three keys atomically. Tests verify call count 1 on success, 0 on refusal."
       },
       {
         "concern": "correctness: ClearField/ClearFlag removing all three keys - ensure no orphan provenance on partial clears",
         "disposition": "addressed",
         "detail": "ClearFlag (task_frontmatter.go:194-198) and ClearField (line 213-216) both delete flag, flag_set_by, and flag_set_at together. Tests verify all three are nil after clear."
       },
       {
         "concern": "security: direct task set flag_set_by rejected - prevents provenance injection attacks",
         "disposition": "addressed",
         "detail": "flagProvenanceSetRefusal (frontmatter.go:252-258) runs before any mutation and refuses flag_set_by and flag_set_at keys with nothing written. --force does not bypass."
       }
     ]
   }
   ```
   ````

7. **Add the AC1 test in `pkg/verdict_test.go`** (package `pkg_test`; add `os` to the imports). Read the fixture with `os.ReadFile("testdata/review_bborbe_vault_cli_243.md")` and assert on `pkg.StripJSONVerdict(string(body))`:
   - `NotTo(ContainSubstring("\"verdict\""))`
   - `NotTo(ContainSubstring("\"summary\""))`
   - `NotTo(ContainSubstring("\"comments\""))`
   - `NotTo(ContainSubstring("\"concerns_addressed\""))`
   - `NotTo(ContainSubstring("```"))`
   - each of the six numbered prose items still appears — at minimum assert `ContainSubstring("1. **`--by` enforcement")`, `ContainSubstring("3. **`flag_set_at`")`, and `ContainSubstring("6. **`NewFrontmatterSetOperation`")`.

   The four quoted-key negatives are the point: they forbid a line-deletion implementation that leaves orphan JSON in the posted body.

8. **Add the AC2 multi-block test in `pkg/verdict_test.go`.** Build a two-block chunked body with a per-chunk block followed by the aggregate, for example:

   ```go
   reviewText := "### Chunk 1/3\n\nFirst chunk prose.\n\n```json\n" +
   	`{"verdict":"approve","summary":"a","comments":[],"concerns_addressed":[]}` +
   	"\n```\n\n### Chunk 2/3\n\nSecond chunk prose.\n\n```json\n" +
   	`{"verdict":"request-changes","summary":"b","comments":[],"concerns_addressed":[]}` +
   	"\n```\n\n### Chunk 3/3\n\nThird chunk prose.\n\n```json\n" +
   	`{"verdict":"request-changes","reason":"chunked review: at least one chunk requested changes"}` +
   	"\n```\n"
   ```

   Assert `strings.Count(stripped, "\"verdict\"") == 0`, likewise `"summary"`, `"comments"`, `"concerns_addressed"`, ` ```json `, while `### Chunk 1/3`, `### Chunk 2/3`, `### Chunk 3/3` still appear.

9. **Add the AC8 fail-open test in `pkg/verdict_test.go`.** Feed a body whose verdict-looking block cannot be delimited, e.g. `"# Review\n\nSome prose.\n\n```json\n{\"verdict\": \"approve\""` (opening fence, no closing fence, no closing brace). Assert `pkg.StripJSONVerdict(reviewText)` equals `reviewText` exactly, and `pkg.ParseVerdict(reviewText).Verdict == pkg.VerdictRequestChanges` (the locator cannot find a block either, so the parser fail-closes deterministically — the posted event is still correct).

   <!-- Reviewer note: AC8 says "the posted verdict still parses". With a truly undelimitable block the locator cannot parse it either, so the only consistent reading is that the posting path still yields a deterministic (fail-closed) verdict. This test asserts that. -->

10. **Do not modify the five existing `StripJSONVerdict` contexts** in `pkg/verdict_test.go`. In particular, leave the "preserves other JSON in review" context intact — it is the guard against a naive fence-truncation implementation. Only add new contexts (and the `os` import).

11. **Update `docs/pr-post-back.md`.** In the "What Gets Posted" section, add a short subsection (or paragraph) stating the demotion contract. It must contain the word `demotion`. Suggested text:

    ```markdown
    ### Demoted bodies

    The posted event is derived from the verdict alone (spec 060). When a Go-side gate demotes an `approve` to `request-changes` — the mechanical funnel did not run, a `## Plan` concern is unverified at budget, a blocking finding is present, or the chunked fold saw a chunk request changes — the posted body **opens with the demotion reason on its first line**, followed by the stripped review body. The full review prose survives beneath the reason; the demotion line is never substituted for the review. A model-authored `request-changes` gains no demotion line: its body is exactly the review with the verdict block removed.
    ```

    <!-- Reviewer note: the demotion line itself is implemented by prompt 2 of this spec. This prompt documents the complete contract so AC7 lands atomically; both prompts ship together. -->

12. **Update `CHANGELOG.md`.** There is no `## Unreleased` heading today. Create it below the preamble block and above the newest `## v0.16.1` section (order: `# Changelog` → preamble → `## Unreleased` → `## v0.16.1`). Add a bullet that contains the exact phrase `removes every verdict block`, for example:

    ```markdown
    ## Unreleased

    - fix: **a posted review's body now agrees with its GitHub event.** `StripJSONVerdict` removes every verdict block — not only a single-line `"verdict"`+`"reason"` line — so the pretty-printed block the reviewer actually emits, and every per-chunk block in a chunked review, no longer reaches the posted body; and a gate-demoted `approve` now opens its body with the demotion reason instead of posting `CHANGES_REQUESTED` beneath an `approve` body.
    ```

13. **Keep the change scoped.** Do not touch `pkg/steps_checkout_execution.go`, `pkg/githubposter/poster.go`, or `pkg/chunk.go`. Do not change `ParseVerdict`'s parsing behaviour. Do not change the 65,536-character truncation path. Do not add a README section — this is internal body formatting, not a run-mode or configuration change.
</requirements>

<constraints>
- The fail-closed gates keep their exact precedence: funnel, then concerns, then blocking. This prompt does not touch the gates.
- `mapVerdictAndSummary` keeps mapping verdict → event only. No new posting path, no new `COMMENT` event (spec 060). Do not touch `PostLGTM` or `postHallucinationComment`.
- `ParseVerdict`'s block locator is the reference implementation — reuse `findVerdictBlock` rather than forking a second locator that can drift.
- Existing behaviour for a body with no verdict block stays: returned unchanged.
- The 65,536-character truncation path (`pkg/githubposter/poster.go`) keeps working after the block is removed; this prompt does not change it.
- `docs/pr-post-back.md` must state that a gate-demoted body opens with the demotion reason; `CHANGELOG.md` carries the change under `## Unreleased`.
- Go 1.26, Ginkgo v2/Gomega, Counterfeiter mocks, errors via `github.com/bborbe/errors`, logging via `glog`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).
- `go test ./pkg/... -count=1` — must pass, including every `ParseVerdict` context and the five pre-existing `StripJSONVerdict` contexts.
- `grep -c 'containsVerdictJSON(' pkg/verdict.go` — prints `0`.
- `git diff pkg/verdict_test.go` — inspection: the five existing contexts are untouched; only new contexts and the `os` import were added. (`.dark-factory.yaml` uses `workflow: direct`, so `.git` is present.)
- `grep -c 'demotion' docs/pr-post-back.md` — prints a number greater than or equal to `1`.
- `awk '/^## /{sec=$0} /removes every verdict block/{print sec}' CHANGELOG.md` — prints `## Unreleased`.
</verification>
