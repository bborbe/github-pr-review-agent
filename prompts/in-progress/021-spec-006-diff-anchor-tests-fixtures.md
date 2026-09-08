---
status: approved
spec: [006-diff-anchor-funnel-findings]
created: "2026-09-08T20:14:50Z"
queued: "2026-09-08T20:34:36Z"
branch: dark-factory/diff-anchor-funnel-findings
---

# Diff-anchor the mechanical funnel findings — tests, fixtures, contract rows

<!-- ENVIRONMENT NOTE FOR THE REVIEWER (not an operator step): the spec says the
real-runner-captured fixtures live at /tmp/fixture-debt (built + verified 2026-09-08).
That directory is NOT present in the YOLO container and NOT mounted (extraMounts in
.dark-factory.yaml only cover GOPATH/GOCACHE/golangci-lint-cache), and this generation
host could not clone bborbe/pr-review-fixtures (private, no token). Requirement 6
therefore has a primary path (use /tmp/fixture-debt if present) and a reconstruction
fallback (re-derive from the fixture PRs on GitHub), with hard count verification and a
fail-loud STOP on any divergence — the executor must NOT fabricate fixtures. If you have
the fixture data, ensure it is reachable inside the container (e.g. mount it or copy it
into the worktree) before approving; otherwise the executor will attempt reconstruction. -->

<summary>
- A Ginkgo table covers hunk-header parsing: single and multiple hunks per file, pure additions, deletion-only hunks, empty output, malformed headers, boundary ranges, and rename/binary-only entries — every row calls the real parser and asserts the parsed range or the parse failure
- A second Ginkgo table covers the filter contract: a finding survives only on a basename match plus an in-range line, and drops on an out-of-range line, an absent basename, an empty file, or line 0 — with explicit rows for the 0-based-to-1-based and absolute-vs-relative path traps
- Two Run-level rows prove fail-closed behavior: an unresolvable base ref, and a malformed hunk header forced through the real git path, both asserting `Ran` false with empty findings JSON and a non-empty failure detail
- The two real-runner fixture JSONs (17 findings on a 1-line change, 19 findings on a diff that adds a log-and-return defect) are committed alongside their captured `git diff --unified=0` output, and replay through the real parser and filter: 17 → 0 findings, 19 → exactly the two introduced findings at ast-grep lines 53 and 54 (0-based = git lines 54 and 55)
- A contract row proves the filtered output re-parses as valid JSON with the frozen top-level shape, preserved `stats` fields, recomputed count, and verbatim per-finding fields
- The unexported parser and filter are exposed to the external test package via `export_test.go`, following the codebase's existing wrapper convention
</summary>

<objective>
Prove the diff-anchoring filter shipped in prompt 1 with the spec's Ginkgo table rows, the committed real-runner fixtures, the fail-closed rows, and the contract row — locking the hunk parsing, the two coordinate traps, and the frozen JSON contract.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Ginkgo v2 / Gomega table style, `export_test.go` wrapper pattern, `testdata/` fixture pattern).

Read these files fully:
- `pkg/funnel.go` — the production symbols this prompt's tests exercise, shipped by prompt 1 of this batch (which runs before this prompt and lands the symbols in the tree): `parseHunkHeader(ctx, line) (lineRange, error)`, `parseHunks(ctx, diffOutput) (map[string][]lineRange, error)`, `filterFindings(ctx, findingsJSON string, changedFiles []string, ranges map[string][]lineRange) (string, error)`, `lineRange{Start, Count}`, and the `FunnelRunner.Run` fail-closed wiring. Read it to confirm the exact signatures before writing the wrappers. If any symbol is absent when this prompt runs, report `status: failed` — do not re-implement the filter.
- `pkg/funnel_test.go` — the existing `FunnelRunner` `Describe` block with its `BeforeEach`/`AfterEach` (`ctx`, `tmpDir`), the `writeRunner` helper (installs a fake `ast-grep-runner.sh` under a fake `CLAUDE_CONFIG_DIR`), and the `initWorktree` helper (builds a real git worktree with a `main` base commit and a `feature` commit adding `changed.go`). The new tables and rows are added inside or beside this file.
- `pkg/export_test.go` — the existing convention for exposing unexported functions/types to the external `pkg_test` package (e.g. `LastCharsForTest`, `NormalizeURLForTest`; type aliases like `VerdictPayloadForTest = verdictPayload`).
- `pkg/verdict_extraction_test.go` — the `DescribeTable` + `os.ReadFile("testdata/"+fixture)` pattern for fixture-driven rows.

Read these coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 `DescribeTable`/`Entry`, Gomega assertions, coverage
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors` usage in test wrappers
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md` — package conventions

Verified contracts (do not re-derive):
- Real git hunk headers from `git diff --unified=0` (verified against git 2.x): `@@ -1,3 +1,4 @@`, `@@ -6,0 +7 @@` (count omitted when 1), `@@ -0,0 +1 @@` (new file), `@@ -9,2 +10,3 @@ func existing() {` (trailing section heading is tolerated), `@@ -5,3 +5,0 @@` (deletion-only, new count 0). The `+++ b/<path>` line precedes the hunks of a file; rename and binary-only entries emit no `+++`/`@@` lines.
- Findings JSON schema (frozen): top-level `stats` (`yamls_run`, `findings_count`, `elapsed_ms`), `findings_by_owner` (owner → array of findings), `errors`; per-finding fields in order `rule_id`, `rule_level`, `file`, `line`, `column`, `matched_text`, `message`. `file` is absolute, `line` is ast-grep 0-based.
- The fixture JSONs the spec pins: `funnel_fixture_minimal_diff_debt.json` contains `"findings_count":17` and filters to `findings_count == 0` with empty `findings_by_owner`; `funnel_fixture_diff_introduces_defect.json` contains `"findings_count":19` and filters to exactly 2 survivors — `go-logging/no-log-and-return-error` at `"line":53` and `go-composition/no-package-function-calls-in-business-logic` at `"line":54` (the 0-based ast-grep lines are preserved verbatim in the survivors; they prove the +1 conversion because git lines 54/55 must be in the changed ranges for them to survive).
- The fixture PRs the spec names: `bborbe/pr-review-fixtures#1` (`fix/minimal-diff-on-debt`, head `7f5a15851699da359047a47c2af329474c9d48c8`) and `bborbe/pr-review-fixtures#2` (`fix/real-defect`, head `03448af503d9cf626bf7d4bb4cae23b7ea4e5db1`).
- `t.Setenv` inside Ginkgo v2 specs is done via `GinkgoT().Setenv(...)` (dot-imported) and auto-restores after the spec.
</context>

<requirements>
1. **Add the `export_test.go` wrappers** to `pkg/export_test.go` (append at the end of the file, same doc-comment style as the existing wrappers):

   ```go
   // LineRangeForTest re-exports the unexported lineRange so funnel_test.go (in
   // the pkg_test package) can assert parsed hunk ranges.
   type LineRangeForTest = lineRange

   // ParseHunkHeaderForTest exposes parseHunkHeader for unit testing.
   func ParseHunkHeaderForTest(ctx context.Context, line string) (LineRangeForTest, error) {
   	return parseHunkHeader(ctx, line)
   }

   // ParseHunksForTest exposes parseHunks for unit testing.
   func ParseHunksForTest(ctx context.Context, diffOutput string) (map[string][]LineRangeForTest, error) {
   	return parseHunks(ctx, diffOutput)
   }

   // FilterFindingsForTest exposes filterFindings for unit testing.
   func FilterFindingsForTest(
   	ctx context.Context,
   	findingsJSON string,
   	changedFiles []string,
   	ranges map[string][]LineRangeForTest,
   ) (string, error) {
   	return filterFindings(ctx, findingsJSON, changedFiles, ranges)
   }
   ```

   `LineRangeForTest` is a type ALIAS (`=`), so `map[string][]LineRangeForTest` is exactly `map[string][]lineRange` and the wrappers type-check without conversion.

2. **Add the hunk-parsing `DescribeTable`s to `pkg/funnel_test.go`** (AC 1). Append a new `var _ = Describe("Funnel diff-anchoring (spec-006)", ...)` block at the end of the file. Its first two `Describe`s are the hunk-parsing tables; every `Entry` row MUST call the parser and assert the parsed range or the malformed-header failure:

   ```go
   var _ = Describe("Funnel diff-anchoring (spec-006)", func() {
   	Describe("hunk header parsing", func() {
   		DescribeTable("parseHunkHeader yields the new-file range or fails on a malformed header",
   			func(line string, wantStart int, wantCount int, wantErr bool) {
   				r, err := pkg.ParseHunkHeaderForTest(context.Background(), line)
   				if wantErr {
   					Expect(err).To(HaveOccurred())
   					return
   				}
   				Expect(err).NotTo(HaveOccurred())
   				Expect(r.Start).To(Equal(wantStart))
   				Expect(r.Count).To(Equal(wantCount))
   			},
   			Entry("single hunk with explicit counts", "@@ -1,3 +1,4 @@", 1, 4, false),
   			Entry("counts omitted when they equal 1", "@@ -6,0 +7 @@", 7, 1, false),
   			Entry("pure addition of a new file", "@@ -0,0 +1 @@", 1, 1, false),
   			Entry("pure addition mid-file", "@@ -5,0 +6,3 @@", 6, 3, false),
   			Entry("deletion-only hunk (+0,0)", "@@ -5,3 +5,0 @@", 5, 0, false),
   			Entry("deletion-only hunk at file start (+0,0)", "@@ -1,3 +0,0 @@", 0, 0, false),
   			Entry("hunk-boundary range [10,13)", "@@ -10,3 +10,3 @@", 10, 3, false),
   			Entry("trailing section heading is tolerated", "@@ -9,2 +10,3 @@ func existing() {", 10, 3, false),
   			Entry("malformed: non-numeric positions", "@@ -a,b +c,d @@", 0, 0, true),
   			Entry("malformed: missing trailing @@", "@@ -5 +5", 0, 0, true),
   			Entry("malformed: garbage", "@@ garbage hunk @@", 0, 0, true),
   		)
   	})

   	Describe("hunk parsing over a full diff", func() {
   		DescribeTable("parseHunks attributes changed ranges to file basenames",
   			func(diff string, want map[string][]pkg.LineRangeForTest, wantErr bool) {
   				got, err := pkg.ParseHunksForTest(context.Background(), diff)
   				if wantErr {
   					Expect(err).To(HaveOccurred())
   					return
   				}
   				Expect(err).NotTo(HaveOccurred())
   				Expect(got).To(Equal(want))
   			},
   			Entry("empty hunk output → no ranges, no error", "", map[string][]pkg.LineRangeForTest{}, false),
   			Entry("multiple hunks in one file",
   				"diff --git a/m.go b/m.go\n"+
   					"--- a/m.go\n"+
   					"+++ b/m.go\n"+
   					"@@ -6,0 +7 @@ func existing() {\n"+
   					"+\tprintln(\"d\")\n"+
   					"@@ -9,2 +10,3 @@ func existing() {\n"+
   					"-func bar() {\n"+
   					"-println(\"x\")\n"+
   					"+func added() {\n"+
   					"+\tlog.Println(\"hi\")\n"+
   					"+\treturn\n",
   				map[string][]pkg.LineRangeForTest{"m.go": {{Start: 7, Count: 1}, {Start: 10, Count: 3}}}, false),
   			Entry("multiple files",
   				"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,4 @@\n+\tmore\n"+
   					"diff --git b/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1,2 @@\n+\tmore\n",
   				map[string][]pkg.LineRangeForTest{
   					"a.go": {{Start: 1, Count: 4}},
   					"b.go": {{Start: 1, Count: 2}},
   				}, false),
   			Entry("rename-only entry contributes no ranges and does not break parsing",
   				"diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n"+
   					"diff --git a/m.go b/m.go\n--- a/m.go\n+++ b/m.go\n@@ -1,3 +1,4 @@\n+\tmore\n",
   				map[string][]pkg.LineRangeForTest{"m.go": {{Start: 1, Count: 4}}}, false),
   			Entry("binary-only entry contributes no ranges and does not break parsing",
   				"diff --git a/img.png b/img.png\nindex 0000000..1111111 100644\nBinary files /dev/null and b/img.png differ\n"+
   					"diff --git a/m.go b/m.go\n--- a/m.go\n+++ b/m.go\n@@ -1,3 +1,4 @@\n+\tmore\n",
   				map[string][]pkg.LineRangeForTest{"m.go": {{Start: 1, Count: 4}}}, false),
   			Entry("a malformed header fails the whole parse",
   				"diff --git a/m.go b/m.go\n--- a/m.go\n+++ b/m.go\n@@ nonsense @@\n",
   				nil, true),
   		)
   	})
   ```

   Note the composite-literal shorthand `{Start: 7, Count: 1}` inside the `map[string][]pkg.LineRangeForTest` values — this is valid Go (the element type is inferred from the map value type). If your linter flags it, write the full `pkg.LineRangeForTest{Start: 7, Count: 1}` form.

3. **Add the filter `DescribeTable` and the contract `It`** inside the same new `Describe` block. First add two small test helpers near the top of the block (after the `Describe("Funnel diff-anchoring (spec-006)", func() {` opener):

   ```go
   	// singleFindingJSON builds a findings object with one owner holding one
   	// finding at the given 0-based line.
   	singleFindingJSON := func(file string, line int) string {
   		return `{"stats":{"yamls_run":1,"findings_count":1,"elapsed_ms":0},` +
   			`"findings_by_owner":{"owner-a":[{"rule_id":"r1","rule_level":"MUST","file":"` + file + `","line":` +
   			fmt.Sprint(line) + `,"column":1,"matched_text":"t","message":"m"}]},"errors":[]}`
   	}
   	// rangeOf builds a ranges map with one single-line range [start, start+1).
   	rangeOf := func(base string, start int) map[string][]pkg.LineRangeForTest {
   		return map[string][]pkg.LineRangeForTest{base: {{Start: start, Count: 1}}}
   	}
   ```

   These need the `fmt` import added to `pkg/funnel_test.go`'s import block. Then add:

   ```go
   	Describe("finding filtering", func() {
   		DescribeTable("filterFindings keeps only findings on changed lines",
   			func(findingsJSON string, changedFiles []string, ranges map[string][]pkg.LineRangeForTest, wantCount int) {
   				filtered, err := pkg.FilterFindingsForTest(context.Background(), findingsJSON, changedFiles, ranges)
   				Expect(err).NotTo(HaveOccurred())
   				var report struct {
   					Stats struct {
   						FindingsCount int `json:"findings_count"`
   					} `json:"stats"`
   					FindingsByOwner map[string][]struct {
   						RuleID string `json:"rule_id"`
   						Line   int    `json:"line"`
   					} `json:"findings_by_owner"`
   				}
   				Expect(json.Unmarshal([]byte(filtered), &report)).To(Succeed())
   				Expect(report.Stats.FindingsCount).To(Equal(wantCount))
   			},
   			Entry("survives when basename matches and line+1 is in range",
   				singleFindingJSON("/tmp/x/metrics.go", 3), []string{"metrics.go"}, rangeOf("metrics.go", 4), 1),
   			Entry("0-based → 1-based: ast-grep line 3 maps to git line 4",
   				singleFindingJSON("/tmp/x/metrics.go", 3), []string{"metrics.go"}, rangeOf("metrics.go", 4), 1),
   			Entry("absolute finding path + relative changed path share a basename",
   				singleFindingJSON("/tmp/x/sub/metrics.go", 3), []string{"sub/metrics.go"}, rangeOf("metrics.go", 4), 1),
   			Entry("matching basename but out-of-range line drops",
   				singleFindingJSON("/tmp/x/metrics.go", 5), []string{"metrics.go"}, rangeOf("metrics.go", 4), 0),
   			Entry("basename absent from the changed set drops",
   				singleFindingJSON("/tmp/x/other.go", 3), []string{"metrics.go"}, rangeOf("metrics.go", 4), 0),
   			Entry("empty file drops",
   				singleFindingJSON("", 3), []string{"metrics.go"}, rangeOf("metrics.go", 4), 0),
   			Entry("line 0 drops even when git line 1 is changed",
   				singleFindingJSON("/tmp/x/metrics.go", 0), []string{"metrics.go"}, rangeOf("metrics.go", 1), 0),
   			Entry("hunk boundary: first line of a range is kept",
   				singleFindingJSON("/tmp/x/metrics.go", 9), []string{"metrics.go"}, map[string][]pkg.LineRangeForTest{"metrics.go": {{Start: 10, Count: 3}}}, 1),
   			Entry("hunk boundary: last line of a range is kept",
   				singleFindingJSON("/tmp/x/metrics.go", 11), []string{"metrics.go"}, map[string][]pkg.LineRangeForTest{"metrics.go": {{Start: 10, Count: 3}}}, 1),
   			Entry("hunk boundary: one past the end is dropped",
   				singleFindingJSON("/tmp/x/metrics.go", 12), []string{"metrics.go"}, map[string][]pkg.LineRangeForTest{"metrics.go": {{Start: 10, Count: 3}}}, 0),
   		)

   		It("preserves the frozen JSON contract when filtering", func() {
   			input := `{"stats":{"yamls_run":12,"findings_count":3,"elapsed_ms":7},` +
   				`"findings_by_owner":{"owner-a":[{"rule_id":"r1","rule_level":"MUST","file":"/tmp/x/metrics.go","line":3,"column":2,"matched_text":"t","message":"m"}]},` +
   				`"errors":[{"kind":"missing-yaml","rule_id":"r9"}]}`
   			filtered, err := pkg.FilterFindingsForTest(context.Background(), input, []string{"metrics.go"},
   				map[string][]pkg.LineRangeForTest{"metrics.go": {{Start: 4, Count: 1}}})
   			Expect(err).NotTo(HaveOccurred())
   			Expect(json.Valid([]byte(filtered))).To(BeTrue())
   			var report map[string]interface{}
   			Expect(json.Unmarshal([]byte(filtered), &report)).To(Succeed())
   			Expect(report).To(HaveKey("stats"))
   			Expect(report).To(HaveKey("findings_by_owner"))
   			Expect(report).To(HaveKey("errors"))
   			var typed struct {
   				Stats struct {
   					YamlsRun      int `json:"yamls_run"`
   					FindingsCount int `json:"findings_count"`
   					ElapsedMs     int `json:"elapsed_ms"`
   				} `json:"stats"`
   				FindingsByOwner map[string][]struct {
   					RuleID      string `json:"rule_id"`
   					RuleLevel   string `json:"rule_level"`
   					File        string `json:"file"`
   					Line        int    `json:"line"`
   					Column      int    `json:"column"`
   					MatchedText string `json:"matched_text"`
   					Message     string `json:"message"`
   				} `json:"findings_by_owner"`
   			}
   			Expect(json.Unmarshal([]byte(filtered), &typed)).To(Succeed())
   			Expect(typed.Stats.YamlsRun).To(Equal(12))
   			Expect(typed.Stats.ElapsedMs).To(Equal(7))
   			Expect(typed.Stats.FindingsCount).To(Equal(1))
			survivors := typed.FindingsByOwner["owner-a"]
			Expect(survivors).To(HaveLen(1))
			Expect(survivors[0].RuleID).To(Equal("r1"))
			Expect(survivors[0].RuleLevel).To(Equal("MUST"))
			Expect(survivors[0].File).To(Equal("/tmp/x/metrics.go"))
			Expect(survivors[0].Line).To(Equal(3))
			Expect(survivors[0].Column).To(Equal(2))
			Expect(survivors[0].MatchedText).To(Equal("t"))
			Expect(survivors[0].Message).To(Equal("m"))
   		})
   	})
   ```

   Add `encoding/json` and `fmt` to `pkg/funnel_test.go`'s import block (it currently imports `context`, `os`, `os/exec`, `path/filepath`, `claudelib`, the `pkg` package, ginkgo, gomega).

4. **Add the two fail-closed Run-level rows (AC 5)** inside the EXISTING `Describe("FunnelRunner", ...)` block in `pkg/funnel_test.go` (they reuse the block's `ctx`/`tmpDir`/`writeRunner`/`initWorktree` helpers). Append a new `Describe("diff-anchoring fail-closed", ...)` at the end of that block:

   ```go
   	Describe("diff-anchoring fail-closed", func() {
   		It("fail-closes when the base ref cannot be resolved (git diff fails)", func() {
   			cfg := writeRunner("#!/usr/bin/env bash\necho '{\"stats\":{\"findings_count\":1},\"errors\":[]}'\n")
   			work := initWorktree()
   			result, err := pkg.NewFunnelRunner(cfg).Run(ctx, work, "no-such-ref")
   			Expect(err).NotTo(HaveOccurred())
   			Expect(result.Ran).To(BeFalse())
   			Expect(result.FindingsJSON).To(BeEmpty())
   			Expect(result.FailDetail).NotTo(BeEmpty())
   		})

   		It("fail-closes on a malformed hunk header and never injects unfiltered findings", func() {
   			// GIT_EXTERNAL_DIFF makes git's content diff invoke a fake driver whose
   			// stdout replaces the hunk output, exercising the real hunk-parse path
   			// inside Run with real git. git diff --name-only (used for the changed
   			// files and the base resolution) is unaffected by GIT_EXTERNAL_DIFF.
   			ext := filepath.Join(tmpDir, "fake-ext-diff.sh")
   			Expect(os.WriteFile(ext, []byte("#!/usr/bin/env bash\necho '@@ garbage hunk @@'\n"), 0700)).To(Succeed())
   			GinkgoT().Setenv("GIT_EXTERNAL_DIFF", ext)
   			cfg := writeRunner("#!/usr/bin/env bash\necho '{\"stats\":{\"findings_count\":1},\"errors\":[]}'\n")
   			work := initWorktree()
   			result, err := pkg.NewFunnelRunner(cfg).Run(ctx, work, "main")
   			Expect(err).NotTo(HaveOccurred())
   			Expect(result.Ran).To(BeFalse())
   			Expect(result.FindingsJSON).To(BeEmpty())
   			Expect(result.FailDetail).To(ContainSubstring("malformed hunk header"))
   		})
   	})
   ```

   These two rows assert, for both the git-failure and the malformed-header triggers, `Ran` false, `FindingsJSON` empty, and `FailDetail` non-empty — the spec's fail-closed contract.

5. **Prepare and commit the two real-runner fixture files (AC 3, AC 4).** The fixture data consists of, for each of the two fixture diffs: the findings JSON captured from the real ast-grep runner, and the `git diff --unified=0 <base>...<head>` output whose hunks the filter consumes. Do this in this order:

   a. **Primary path — `/tmp/fixture-debt` is present** (the spec's documented location; it is a git repo with `metrics.go` plus `findings.json` (=17) and `findings2.json` (=19)). Verify the directory and the expected files exist first:
      - Copy `/tmp/fixture-debt/findings.json` → `pkg/testdata/funnel_fixture_minimal_diff_debt.json`
      - Copy `/tmp/fixture-debt/findings2.json` → `pkg/testdata/funnel_fixture_diff_introduces_defect.json`
      - Capture the two diffs from the fixture git repo by commit SHA (verified 2026-09-08: there are NO `base`/`feature`/`feature2` refs — the only branch is `feature`; the commits are `1a2b5ff` = base, `742b4fc` = feature (1-line pin, unreferenced), `c1f8d65` = feature2 (Ping added, branch `feature`)). Use:
        - `git -C /tmp/fixture-debt diff --unified=0 1a2b5ff 742b4fc > pkg/testdata/funnel_fixture_minimal_diff_debt.diff`
        - `git -C /tmp/fixture-debt diff --unified=0 1a2b5ff c1f8d65 > pkg/testdata/funnel_fixture_diff_introduces_defect.diff`
      - Verify the JSONs and diffs before proceeding: `grep -c '"findings_count":17' pkg/testdata/funnel_fixture_minimal_diff_debt.json` returns ≥ 1 AND `grep -c '"findings_count":19' pkg/testdata/funnel_fixture_diff_introduces_defect.json` returns ≥ 1 AND `grep -c '^@@' pkg/testdata/funnel_fixture_minimal_diff_debt.diff` returns ≥ 1 AND `grep -c '^@@' pkg/testdata/funnel_fixture_diff_introduces_defect.diff` returns ≥ 1 (the diff must carry real hunks — the 17→0 fixture must not pass vacuously on an empty diff).

   b. **Fallback path — reconstruction from the fixture PRs** (ONLY if `/tmp/fixture-debt` is absent AND the four files above do not already exist in `pkg/testdata/`):
      - Clone `https://github.com/bborbe/pr-review-fixtures.git` into a temp dir (the container has GitHub access; if the clone fails for lack of credentials, STOP and report `status: failed` with that reason — do not fabricate fixtures).
      - PR #1 (minimal-diff-on-debt): check out head `7f5a15851699da359047a47c2af329474c9d48c8` (branch `fix/minimal-diff-on-debt`). PR #2 (real-defect): check out head `03448af503d9cf626bf7d4bb4cae23b7ea4e5db1` (branch `fix/real-defect`). For each, determine the base branch (`git merge-base <base> <head>`; it is the PR's target branch, typically `main` or `master`).
      - For each PR: capture `git diff --name-only <base>...<head>` (the changed files) and `git diff --unified=0 <base>...<head> > <name>.diff`, then run the REAL ast-grep funnel over the changed files at the head worktree: `<coding-plugin>/scripts/ast-grep-runner.sh <head-worktree> <changed-file...>` where `<coding-plugin>` = `/home/node/.claude/plugins/marketplaces/coding` (the container's coding plugin). Capture stdout as the findings JSON.
      - Save as `pkg/testdata/funnel_fixture_minimal_diff_debt.json`/`.diff` (PR #1) and `pkg/testdata/funnel_fixture_diff_introduces_defect.json`/`.diff` (PR #2).
      - VERIFY the captured counts match the spec exactly: `grep -c '"findings_count":17'` on the minimal JSON ≥ 1 AND `grep -c '"findings_count":19'` on the defect JSON ≥ 1. If either count differs (e.g. a different ast-grep/coding-plugin version changed the findings), STOP and report `status: failed` with the actual counts — do NOT commit fixtures that contradict the spec's ACs, and do NOT hand-edit the JSON to force the counts.

   c. If neither path yields the four files, STOP and report `status: failed` with a clear message naming the missing fixture source (do not proceed with incomplete fixtures).

6. **Add the two fixture rows to `pkg/funnel_test.go`** (AC 3, AC 4), inside the new `Describe("Funnel diff-anchoring (spec-006)", ...)` block. Add a small helper `changedPathsFromDiff` inside the block and a `survivor` type at package scope (or inside the block), then the `DescribeTable`:

   ```go
   	// changedPathsFromDiff extracts the new-side repo-relative paths from the
   	// "+++ b/<path>" lines of a unified diff.
   	changedPathsFromDiff := func(diffOutput string) []string {
   		var paths []string
   		for line := range strings.SplitSeq(diffOutput, "\n") {
   			if strings.HasPrefix(line, "+++ b/") {
   				paths = append(paths, strings.TrimPrefix(line, "+++ b/"))
   			}
   		}
   		return paths
   	}

   	type survivor struct {
   		ruleID string
   		line   int
   	}

   	Describe("real-runner fixtures", func() {
   		DescribeTable("replays the committed real-runner fixtures through the parser and filter",
   			func(jsonFile string, diffFile string, wantCount int, wantSurvivors []survivor) {
   				rawJSON, err := os.ReadFile("testdata/" + jsonFile)
   				Expect(err).NotTo(HaveOccurred())
   				rawDiff, err := os.ReadFile("testdata/" + diffFile)
   				Expect(err).NotTo(HaveOccurred())
   				ranges, err := pkg.ParseHunksForTest(context.Background(), string(rawDiff))
   				Expect(err).NotTo(HaveOccurred())
   				filtered, err := pkg.FilterFindingsForTest(
   					context.Background(), string(rawJSON), changedPathsFromDiff(string(rawDiff)), ranges)
   				Expect(err).NotTo(HaveOccurred())
   				var report struct {
   					Stats struct {
   						FindingsCount int `json:"findings_count"`
   					} `json:"stats"`
   					FindingsByOwner map[string][]struct {
   						RuleID string `json:"rule_id"`
   						Line   int    `json:"line"`
   					} `json:"findings_by_owner"`
   				}
   				Expect(json.Unmarshal([]byte(filtered), &report)).To(Succeed())
   				Expect(report.Stats.FindingsCount).To(Equal(wantCount))
   				var got []survivor
   				for _, findings := range report.FindingsByOwner {
   					for _, f := range findings {
   						got = append(got, survivor{ruleID: f.RuleID, line: f.Line})
   					}
   				}
   				if wantCount == 0 {
   					Expect(report.FindingsByOwner).To(BeEmpty())
   					return
   				}
   				Expect(got).To(ConsistOf(wantSurvivors))
   			},
   			Entry("minimal-diff-on-debt: 17 findings → 0 survivors, empty findings_by_owner",
   				"funnel_fixture_minimal_diff_debt.json", "funnel_fixture_minimal_diff_debt.diff", 0, nil),
   			Entry("diff-introduces-defect: 19 findings → exactly the 2 introduced findings (lines 53, 54)",
   				"funnel_fixture_diff_introduces_defect.json", "funnel_fixture_diff_introduces_defect.diff", 2,
   				[]survivor{
   					{ruleID: "go-logging/no-log-and-return-error", line: 53},
   					{ruleID: "go-composition/no-package-function-calls-in-business-logic", line: 54},
   				}),
   		)
   	})
   ```

   The `strings` import must be added to `pkg/funnel_test.go`'s import block. These rows replay from committed data: they read the committed JSON + committed diff, run the production `parseHunks` and `filterFindings`, and assert the spec's exact outcomes (0 survivors / the two survivors at ast-grep lines 53 and 54). If a fixture row FAILS, do NOT weaken the assertion — the fixture data or the filter is wrong; investigate and fix, or report.

7. **Self-check before finishing:** walk AC 1 through AC 6 against the new tests: every hunk-parsing `Entry` calls the parser and asserts; the filter rows cover all five drop/survive conditions plus both coordinate traps; the two fail-closed rows assert `Ran` false + empty `FindingsJSON` + non-empty `FailDetail`; the fixture rows replay the committed 17 and 19 findings to 0 and 2; the contract row asserts the frozen shape. Confirm `grep -c 'Entry(' pkg/funnel_test.go` returns ≥ 1.
</requirements>

<constraints>
- This prompt is confined to `pkg/funnel_test.go`, `pkg/export_test.go`, and the four new files in `pkg/testdata/` (`funnel_fixture_minimal_diff_debt.json` + `.diff`, `funnel_fixture_diff_introduces_defect.json` + `.diff`). Do NOT touch `pkg/funnel.go` — the filter shipped in prompt 1 of this batch and is already in the tree. Do NOT touch `pkg/prompts/*`, `pkg/steps_checkout_execution.go`, `pkg/verdict.go`, `CHANGELOG.md`, or any other file.
- The fixtures must be REAL-runner-captured artifacts (from `/tmp/fixture-debt` or reconstructed from the real runner against the pinned fixture heads). Do NOT synthesize or hand-edit findings to hit the AC counts — a fixture that does not match `"findings_count":17` / `"findings_count":19` (and the 53/54 survivors) is a spec contradiction and must be reported, not forced.
- Findings JSON shape is frozen: the contract row must prove the filtered output re-parses with top-level `stats`/`findings_by_owner`/`errors`, `stats.yamls_run`/`stats.elapsed_ms` pass through, `stats.findings_count` equals the survivors, and surviving per-finding fields are verbatim.
- The execution prompt files (`funnelInjectSteerTemplate`, the verdict-translation footer, `execution_output-format.md`) are byte-identical — this prompt does not touch them, and the AC 6 evidence `git diff pkg/prompts/execution.go pkg/prompts/execution_output-format.md` is empty by construction.
- `FunnelResult` semantics are unchanged; the fail-closed rows must assert the spec's contract (`Ran` false, empty `FindingsJSON`, non-empty `FailDetail`) and must exercise the PRODUCTION `Run` (not a reimplementation of the wiring).
- Tests use Ginkgo v2 / Gomega; LLM-dependent steps use the fake runner — no live Claude calls in tests. The only git subprocesses are the existing worktree builders and the git calls the production `Run`/parser make.
- No config flag, opt-out knob, or tunable threshold is added.
- Do NOT touch `CHANGELOG.md` — the changelog entry for this spec lands in the final prompt of this batch. Do NOT report the missing CHANGELOG entry as a blocker.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `go test -mod=mod ./pkg/... -count=1` — must exit 0 with ALL new rows green: the hunk-parsing `DescribeTable`s, the filter `DescribeTable`, the contract `It`, the two fail-closed `It`s, and the two fixture rows. All pre-existing rows stay green.
- AC 1 evidence: `grep -c 'Entry(' pkg/funnel_test.go` returns ≥ 1.
- AC 3 evidence: `grep -c '"findings_count":17' pkg/testdata/funnel_fixture_minimal_diff_debt.json` returns ≥ 1 AND the `minimal-diff-on-debt` row is green under the `go test` above.
- AC 4 evidence: `grep -c '"findings_count":19' pkg/testdata/funnel_fixture_diff_introduces_defect.json` returns ≥ 1 AND the `diff-introduces-defect` row is green under the `go test` above.
- AC 5 evidence: the two fail-closed rows (`git failure`, `malformed header`) are green under the `go test` above.
- AC 6 evidence: the contract row is green under the `go test` above; `pkg/prompts/execution.go` and `pkg/prompts/execution_output-format.md` are unchanged (this prompt does not modify them — verified by construction; the host-side `git diff` check is an operator/audit step).
- `gofmt -l pkg/funnel_test.go pkg/export_test.go` — must print nothing.
</verification>

<!-- AUDITOR NOTES
1. FIXTURE SOURCE DEPENDENCY (open question for the reviewer): the spec's ACs 3/4 require committed real-runner-captured JSONs containing exactly 17 and 19 findings. The spec points at /tmp/fixture-debt, which is NOT present in this generation environment and NOT in the container's mount set; reconstruction from the private bborbe/pr-review-fixtures repo needs GitHub credentials the container may or may not have. Requirement 5 therefore checks /tmp/fixture-debt first, falls back to reconstruction with hard count verification, and STOPS with status:failed rather than fabricating. Before approving, ensure the fixture data will be reachable in the container (mount or copy into the worktree), or confirm the reconstruction path is viable.
2. The committed `.diff` files (one per fixture) are the spec's "fixture git repo ... committed into pkg/testdata/" rendered as the hunk output the filter consumes; they make the fixture rows replay from committed data through the REAL parseHunks/filterFindings (AC 1's "call the parser and assert"). This is an extension beyond the two JSON files the ACs grep — flag it if you prefer ranges hardcoded instead.
3. The malformed-header fail-closed row uses GIT_EXTERNAL_DIFF (verified on this host: git's content diff then emits only the fake driver's stdout, while `git diff --name-only` is unaffected) so the production Run → changedLineRanges → parseHunks path is exercised with real git and a real malformed header, satisfying AC 5's "asserting Ran false, FindingsJSON empty, FailDetail non-empty" for the malformed case without a production test hook.
4. The contract row (AC 6) asserts the frozen shape on the filtered OUTPUT; the prompt-files byte-identity is by construction (no prompt in this batch touches pkg/prompts/*) — the spec's `git diff` evidence is an operator/audit-side check since the container's .git is masked (hideGit).
5. `strings` and `encoding/json` and `fmt` imports are added to funnel_test.go; the existing suite must stay green (the prompt-1 `Run` rewire already made the fake-runner tests pass through the filter).
-->
