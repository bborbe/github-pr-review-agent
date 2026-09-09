---
status: completed
spec: [006-diff-anchor-funnel-findings]
summary: 'Diff-anchored the mechanical funnel: shared base-ref resolution feeds both changed-files and hunk-range computation, filterFindings drops findings outside PR-changed lines fail-closed, and Run stays green under make precommit'
execution_id: github-pr-review-agent-diff-anchor-exec-020-spec-006-diff-anchor-filter
dark-factory-version: dev
created: "2026-09-08T20:14:50Z"
queued: "2026-09-08T20:34:33Z"
started: "2026-09-08T20:34:35Z"
completed: "2026-09-08T20:40:21Z"
branch: dark-factory/diff-anchor-funnel-findings
---

# Diff-anchor the mechanical funnel findings — filter implementation

<summary>
- The mechanical funnel now filters ast-grep findings down to the exact lines a PR changed before they are injected into the execution prompt
- A single shared base-ref resolution feeds both the changed-files scan and the changed-line computation, so the two can never disagree on the base
- The funnel parses `git diff --unified=0` hunk headers into per-file changed-line ranges, with a strict parser that treats any malformed hunk header as a failure
- A finding survives only when its file's basename matches a changed file AND its 0-based ast-grep line + 1 lands inside a changed range
- Findings with an empty file or line 0 always drop, and `stats.findings_count` is recomputed to the number of survivors; every other JSON field passes through untouched
- Any failure to obtain or parse the changed-line data makes the funnel report that it did not run — unfiltered findings can never reach the model
- Existing funnel behavior (fail-closed on runner failure, no-changed-files short-circuit, code-fence neutralization, the result shape) is unchanged
</summary>

<objective>
Restrict the findings the funnel injects into the execution prompt to the lines the PR actually changed, so pre-existing debt on untouched lines can never reach the model or gate a merge, while any failure to compute the changed lines fails closed.
</objective>

<context>
Read `CLAUDE.md` for project conventions (the mechanical funnel lives in `pkg/funnel.go`, `FunnelResult` semantics, `errors.Wrapf` hygiene, glog style).

Read `pkg/funnel.go` in full — this prompt extends it. The current `Run` method and `changedFiles` method are the exact code you are modifying (quoted below in the requirements). Read `pkg/funnel_test.go` in full — the existing tests must stay green under this change (no test additions here; they land in the next prompt of this batch). Read `docs/dod.md` — the DoD checklist this change must satisfy.

Read these coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors` usage: `errors.Wrapf(ctx, err, "msg")`, `errors.Errorf(ctx, "msg")`; never `fmt.Errorf`, never bare `return err`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md` — private struct + exported constructor, doc comments on exported symbols
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — linter limits (funlen 80 lines); `Run` must stay under it, so the post-run filtering is extracted into a helper method

Verified contracts (do not re-derive):
- The runner's findings JSON shape (frozen, emitted by the coding plugin's `ast-grep-runner.sh`): top-level keys `stats` (`yamls_run`, `findings_count`, `elapsed_ms`), `findings_by_owner` (map of owner → array of findings), `errors` (array). Per-finding fields, in emission order: `rule_id`, `rule_level`, `file`, `line`, `column`, `matched_text`, `message`. `file` is an absolute path (ast-grep emits absolute); `line` is `range.start.line` (0-based); `column` is `range.start.column` (0-based).
- git hunk header shape from `git diff --unified=0`: `@@ -a,b +c,d @@` with an optional trailing section heading after the second `@@` (e.g. `@@ -9,2 +10,3 @@ func existing() {`). Git omits a count when it equals 1 (e.g. `@@ -6,0 +7 @@`, `@@ -0,0 +1 @@`). Pure additions carry old count 0 (`-0,0`); deletion-only hunks carry new count 0 (`+0,0`).
- `changedFiles` is called only from `Run` (verified: no other call sites) — its signature may be changed to consume the resolved base.
- `funnelRunner` already imports `bytes`, `context`, `encoding/json`, `os`, `os/exec`, `path/filepath`, `regexp`, `strings`, `time`, `claudelib`, `errors`, `glog`. The new code adds `strconv` to the import block.
- The `FunnelRunner` interface, `NewFunnelRunner`, `FunnelResult`, `neutralizeCodeFences`, `codeFenceRegexp`, `git`, and `gitOutput` are UNCHANGED by this prompt.
- The execution prompt contract files (`pkg/prompts/execution.go`, `pkg/prompts/execution_output-format.md`) are NOT touched by this prompt or any prompt in this batch.
</context>

<requirements>
1. **Add the changed-line range type and the hunk-header parser to `pkg/funnel.go`.** Add a new `strconv` import, then append these declarations to the file (below the existing `changedFiles`/`git`/`gitOutput` methods):

   ```go
   // lineRange is a half-open interval [Start, Start+Count) of new-file line
   // numbers (1-based) that a PR changed within one hunk.
   type lineRange struct {
   	Start int
   	Count int
   }

   // hunkHeaderRegexp matches the prefix of a git unified-diff hunk header
   // "@@ -a,b +c,d @@" and captures the old-side and new-side starting lines
   // and counts. Git omits a count when it equals 1 and appends the enclosing
   // section heading after the trailing "@@"; both are tolerated (the regex is
   // not end-anchored after the second "@@").
   var hunkHeaderRegexp = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

   // parseHunkHeader parses one git hunk header line and returns the new-file
   // changed range [c, c+d). A line that begins with "@@" but does not match
   // the "@@ -a,b +c,d @@" shape is malformed and fails the parse — a broken
   // parser must never silently un-block a diff.
   func parseHunkHeader(ctx context.Context, line string) (lineRange, error) {
   	m := hunkHeaderRegexp.FindStringSubmatch(line)
   	if m == nil {
   		return lineRange{}, errors.Errorf(ctx, "malformed hunk header: %q", line)
   	}
   	newStart, err := strconv.Atoi(m[3])
   	if err != nil {
   		return lineRange{}, errors.Wrapf(ctx, err, "parse hunk new-start %q", m[3])
   	}
   	newCount := 1
   	if m[4] != "" {
   		newCount, err = strconv.Atoi(m[4])
   		if err != nil {
   			return lineRange{}, errors.Wrapf(ctx, err, "parse hunk new-count %q", m[4])
   		}
   	}
   	return lineRange{Start: newStart, Count: newCount}, nil
   }

   // parseHunks walks unified-diff output (git diff --unified=0) and returns
   // the changed-line ranges keyed by the changed file's basename. Only the
   // new-file "+" lines in the hunk ranges count as changed lines; context
   // lines never count. A "+++ b/<path>" line sets the current file and every
   // following hunk header belongs to it. Rename/binary/mode-only entries
   // contribute no hunks and therefore no ranges — the correct outcome for
   // such a diff. A hunk header that is not in the "@@ -a,b +c,d @@" shape
   // fails the whole parse.
   func parseHunks(ctx context.Context, diffOutput string) (map[string][]lineRange, error) {
   	ranges := map[string][]lineRange{}
   	current := ""
   	for line := range strings.SplitSeq(diffOutput, "\n") {
   		switch {
   		case strings.HasPrefix(line, "+++ b/"):
   			current = strings.TrimPrefix(line, "+++ b/")
   		case strings.HasPrefix(line, "@@"):
   			hunk, err := parseHunkHeader(ctx, line)
   			if err != nil {
   				return nil, err
   			}
   			if current != "" {
   				ranges[filepath.Base(current)] = append(ranges[filepath.Base(current)], hunk)
   			}
   		}
   	}
   	return ranges, nil
   }

   // lineInRanges reports whether the 1-based line falls inside any of the
   // half-open changed ranges.
   func lineInRanges(line int, ranges []lineRange) bool {
   	for _, r := range ranges {
   		if line >= r.Start && line < r.Start+r.Count {
   			return true
   		}
   	}
   	return false
   }
   ```

2. **Add the frozen-schema JSON types and the filter function to `pkg/funnel.go`.** Append:

   ```go
   // funnelStats mirrors the runner's stats object; the filter recomputes only
   // FindingsCount and passes YamlsRun and ElapsedMs through untouched.
   type funnelStats struct {
   	YamlsRun      int `json:"yamls_run"`
   	FindingsCount int `json:"findings_count"`
   	ElapsedMs     int `json:"elapsed_ms"`
   }

   // funnelFinding mirrors one ast-grep finding; the filter keeps surviving
   // findings verbatim.
   type funnelFinding struct {
   	RuleID      string `json:"rule_id"`
   	RuleLevel   string `json:"rule_level"`
   	File        string `json:"file"`
   	Line        int    `json:"line"`
   	Column      int    `json:"column"`
   	MatchedText string `json:"matched_text"`
   	Message     string `json:"message"`
   }

   // funnelReport mirrors the runner's top-level JSON object.
   type funnelReport struct {
   	Stats           funnelStats              `json:"stats"`
   	FindingsByOwner map[string][]funnelFinding `json:"findings_by_owner"`
   	Errors          []json.RawMessage        `json:"errors"`
   }

   // filterFindings rewrites the runner's findings JSON so that only findings
   // on lines the PR changed survive. A finding survives only when its file's
   // basename matches a changed file AND its 0-based ast-grep line + 1 falls
   // inside a changed range [newStart, newStart+newCount). Findings with an
   // empty file or line == 0 drop. stats.findings_count is recomputed to the
   // surviving count; every other field and the top-level shape are untouched.
   func filterFindings(
   	ctx context.Context,
   	findingsJSON string,
   	changedFiles []string,
   	ranges map[string][]lineRange,
   ) (string, error) {
   	changedBases := make(map[string]struct{}, len(changedFiles))
   	for _, cf := range changedFiles {
   		changedBases[filepath.Base(cf)] = struct{}{}
   	}
   	var report funnelReport
   	if err := json.Unmarshal([]byte(findingsJSON), &report); err != nil {
   		return "", errors.Wrapf(ctx, err, "unmarshal funnel findings")
   	}
   	surviving := map[string][]funnelFinding{}
   	count := 0
   	for owner, findings := range report.FindingsByOwner {
   		kept := make([]funnelFinding, 0, len(findings))
   		for _, f := range findings {
   			if f.File == "" {
   				continue
   			}
   			if f.Line == 0 {
   				continue
   			}
   			base := filepath.Base(f.File)
   			if _, ok := changedBases[base]; !ok {
   				continue
   			}
   			if !lineInRanges(f.Line+1, ranges[base]) {
   				continue
   			}
   			kept = append(kept, f)
   		}
   		if len(kept) > 0 {
   			surviving[owner] = kept
   			count += len(kept)
   		}
   	}
   	report.FindingsByOwner = surviving
   	if report.Errors == nil {
   		report.Errors = []json.RawMessage{}
   	}
   	report.Stats.FindingsCount = count
   	out, err := json.Marshal(report)
   	if err != nil {
   		return "", errors.Wrapf(ctx, err, "marshal filtered findings")
   	}
   	return string(out), nil
   }
   ```

3. **Extract the shared base-ref resolution and rework `changedFiles` to consume it.** Replace the current `changedFiles` method (the whole method, quoted in the Context section above) with these two methods:

   ```go
   // resolvedBaseRef resolves the PR diff base, preferring origin/<baseRef>
   // (the worktree is a --local clone of a mirror that carries all branches; a
   // best-effort fetch covers the case where it is missing) with a bare-ref
   // fallback, and returns the ref string to pass to git diff. Both the
   // changed-files scan and the hunk computation consume this single resolution
   // so they can never disagree on the base.
   func (r *funnelRunner) resolvedBaseRef(ctx context.Context, worktreePath string, baseRef string) (string, error) {
   	// Best-effort: make sure origin/<baseRef> exists locally. Ignore failure —
   	// the diff below falls back to the bare ref name.
   	_ = r.git(ctx, worktreePath, "fetch", "origin", baseRef+":refs/remotes/origin/"+baseRef)
   	if _, err := r.gitOutput(ctx, worktreePath, "diff", "--name-only", "origin/"+baseRef+"...HEAD"); err == nil {
   		return "origin/" + baseRef, nil
   	}
   	lastErr, err := r.gitOutput(ctx, worktreePath, "diff", "--name-only", baseRef+"...HEAD")
   	if err == nil {
   		return baseRef, nil
   	}
   	// Wrap the last failed git diff error (gitOutput already surfaces stderr)
   	// rather than discarding it — the surfaced detail is identical, but the
   	// underlying cause stays in the error chain per DoD error hygiene.
   	return "", errors.Wrapf(ctx, lastErr, "git diff --name-only base_ref=%s", baseRef)
   }

   // changedFiles returns the PR's changed file paths (relative to
   // worktreePath) by diffing HEAD against the resolved base ref.
   func (r *funnelRunner) changedFiles(ctx context.Context, worktreePath string, resolvedBase string) ([]string, error) {
   	out, err := r.gitOutput(ctx, worktreePath, "diff", "--name-only", resolvedBase+"...HEAD")
   	if err != nil {
   		return nil, errors.Wrapf(ctx, err, "git diff --name-only base_ref=%s", resolvedBase)
   	}

   	var files []string
   	for line := range strings.SplitSeq(out, "\n") {
   		line = strings.TrimSpace(line)
   		if line == "" {
   			continue
   		}
   		if strings.HasPrefix(line, ".git/") || strings.Contains(line, "/.git/") {
   			continue
   		}
   		files = append(files, line)
   	}
   	return files, nil
   }

   // resolveAndCollectChangedFiles resolves the PR diff base and returns the
   // changed file paths. A non-empty detail means resolution or the diff failed
   // and the caller must fail closed (Ran: false). Extracting this preamble
   // keeps Run under the funlen 80-line limit.
   func (r *funnelRunner) resolveAndCollectChangedFiles(
   	ctx context.Context,
   	worktreePath string,
   	baseRef string,
   ) (resolved string, files []string, detail string) {
   	resolved, resolveErr := r.resolvedBaseRef(ctx, worktreePath, baseRef)
   	if resolveErr != nil {
   		return "", nil, "could not compute changed files for base_ref " + baseRef
   	}
   	files, filesErr := r.changedFiles(ctx, worktreePath, resolved)
   	if filesErr != nil {
   		return "", nil, "could not compute changed files for base_ref " + baseRef
   	}
   	return resolved, files, ""
   }
   ```

4. **Add the hunk-computation method and the fail-closed filtering helper.** Append:

   ```go
   // changedLineRanges returns the PR's per-file changed-line ranges by
   // diffing HEAD against the resolved base ref with --unified=0, where only
   // the new-file "+" lines count as changed. Any failure to obtain or parse
   // the hunk data is returned as an error so the caller can fail closed.
   func (r *funnelRunner) changedLineRanges(ctx context.Context, worktreePath string, resolvedBase string) (map[string][]lineRange, error) {
   	out, err := r.gitOutput(ctx, worktreePath, "diff", "--unified=0", resolvedBase+"...HEAD")
   	if err != nil {
   		return nil, errors.Wrapf(ctx, err, "git diff --unified=0 base_ref=%s", resolvedBase)
   	}
   	return parseHunks(ctx, out)
   }

   // filterToChangedLines returns the runner's findings JSON restricted to the
   // lines the PR changed and an empty detail string on success. A non-empty
   // detail means the hunk data could not be obtained or parsed and the caller
   // must fail closed (Ran: false) — unfiltered findings must never be
   // injected into the execution prompt.
   func (r *funnelRunner) filterToChangedLines(
   	ctx context.Context,
   	worktreePath string,
   	baseRef string,
   	resolvedBase string,
   	files []string,
   	findingsJSON string,
   ) (filtered string, detail string) {
   	ranges, hunkErr := r.changedLineRanges(ctx, worktreePath, resolvedBase)
   	if hunkErr != nil {
   		return "", "could not compute changed lines for base_ref " + baseRef + ": " + hunkErr.Error()
   	}
   	filteredJSON, filterErr := filterFindings(ctx, findingsJSON, files, ranges)
   	if filterErr != nil {
   		return "", "could not filter funnel findings: " + filterErr.Error()
   	}
   	return filteredJSON, ""
   }
   ```

5. **Rewire `Run` in `pkg/funnel.go`.** Make these two edits to the existing `Run` method:

   a. Replace the changed-files block (the current lines `files, filesErr := r.changedFiles(ctx, worktreePath, baseRef)` ... the closing brace of the `if len(files) == 0` block) with:

   ```go
   	resolved, files, preambleDetail := r.resolveAndCollectChangedFiles(ctx, worktreePath, baseRef)
   	if preambleDetail != "" {
   		return FunnelResult{Ran: false, FailDetail: preambleDetail}, nil
   	}
   	if len(files) == 0 {
   		return FunnelResult{
   			Ran:          true,
   			FindingsJSON: `{"stats":{"yamls_run":0,"findings_count":0,"elapsed_ms":0},"findings_by_owner":{},"errors":[]}`,
   		}, nil
   	}
   ```

   b. Replace the tail of `Run` — the current block from `out := strings.TrimSpace(stdout.String())` through the final `return FunnelResult{Ran: true, FindingsJSON: neutralizeCodeFences(out)}, nil` — with:

   ```go
   	out := strings.TrimSpace(stdout.String())
   	if out == "" {
   		return FunnelResult{Ran: false, FailDetail: "ast-grep runner produced no output"}, nil
   	}
   	// Defense-in-depth: the runner's findings carry PR-author-controlled strings
   	// (matched_text / message copied from the diff), and the caller embeds this
   	// JSON into the review prompt. Reject non-JSON output (a compromised or broken
   	// runner) fail-closed, and neutralise code-fence sequences so a crafted PR
   	// cannot break out of the prompt's ```json block and inject directives.
   	if !json.Valid([]byte(out)) {
   		return FunnelResult{
   			Ran:        false,
   			FailDetail: "ast-grep runner output was not valid JSON",
   		}, nil
   	}

   	// Diff-anchor the findings to the lines the PR actually changed before they
   	// reach the review model: pre-existing debt on untouched lines is removed, so
   	// it can never gate a merge and costs no model tokens. Any failure to obtain
   	// or parse the hunk data fail-closes the funnel — a broken filter must never
   	// silently un-block a diff.
   	filtered, detail := r.filterToChangedLines(ctx, worktreePath, baseRef, resolved, files, out)
   	if detail != "" {
   		glog.Warningf("funnel diff-anchor fail-closed base_ref=%s detail=%s", baseRef, detail)
   		return FunnelResult{Ran: false, FailDetail: detail}, nil
   	}
   	glog.Infof("funnel diff-anchor kept findings over files=%d", len(files))
   	return FunnelResult{Ran: true, FindingsJSON: neutralizeCodeFences(filtered)}, nil
   ```

   The `elapsedMs`/`glog` block for the runner exit and the `runErr != nil` fail-closed branches stay exactly as they are today — do not reorder them. Do not touch the `if _, statErr := os.Stat(runner); statErr != nil` guard, the `args := append([]string{worktreePath}, files...)` setup, the `cmd` construction, or the `glog.Infof("exec ast-grep-runner exit=0 ...")` line.

6. **Self-check the wiring before finishing:** walk the new `Run` top to bottom and confirm:
   - the fail-closed detail for an unresolvable base is exactly `could not compute changed files for base_ref <baseRef>` (unchanged from today's behavior, so the existing tests' expectations on that detail — if any — still hold);
   - the hunk computation and the filter run ONLY after the runner produced valid JSON output, and their failure returns `Ran: false` with a non-empty `FailDetail` and an empty `FindingsJSON` (the `FunnelResult{}` literal leaves `FindingsJSON` empty by default);
   - the code-fence neutralization still runs on the FILTERED output, so surviving findings' PR-author-controlled `matched_text`/`message` remain neutralized;
   - `Run` stays under the funlen 80-line limit (the heavy lifting is in `filterToChangedLines`).
</requirements>

<constraints>
- This prompt is confined to `pkg/funnel.go`. Do NOT touch `pkg/funnel_test.go` (tests land in the next prompt of this batch), `pkg/prompts/*`, `pkg/steps_checkout_execution.go`, `pkg/verdict.go`, or any other file. No new test files here.
- `FunnelResult` semantics are unchanged: `Ran` / `FindingsJSON` / `FailDetail`, and the no-changed-files short-circuit (`Ran: true` with an empty findings object) stay exactly as today.
- Findings JSON shape is frozen: top-level `stats` (`yamls_run`, `findings_count`, `elapsed_ms`), `findings_by_owner`, `errors`; per-finding fields `rule_id`, `rule_level`, `file`, `line`, `column`, `matched_text`, `message`. Only `stats.findings_count` is recomputed; everything else passes through.
- The execution prompt files (`funnelInjectSteerTemplate`, the verdict-translation footer, `execution_output-format.md`) are byte-identical; the verifier preamble's full-PR-diff injection is unchanged. The verdict chain and every existing gate behave exactly as before on the surviving findings.
- Line conversion is a fixed invariant: ast-grep `range.start.line` is 0-based, git hunks are 1-based, the filter adds +1. A finding with `line == 0` drops by decision.
- Path matching is a fixed invariant: the runner emits absolute paths, `git diff --name-only` emits repo-relative paths, the filter matches on basename.
- Changed lines are the new-file `+` lines only in `git diff --unified=0`; context lines never count; a hunk range is `[newStart, newStart+newCount)`.
- The hunk computation uses the same base-ref resolution order as the changed-files scan (`origin/<base>` three-dot first, bare-ref fallback) via the single shared `resolvedBaseRef`, so the changed-file set and the hunk ranges always refer to the same base; any failure in either path fail-closes.
- The shared `ast-grep-runner.sh` script is unchanged; filtering happens in this consumer after the runner returns.
- DoD rules apply: errors via `github.com/bborbe/errors` (`errors.Wrapf`/`errors.Errorf` with context — no `fmt.Errorf`, no bare `return err`), no debug output, `context.Context` threaded through IO calls, doc comments on all exported symbols (this prompt adds none, but keep existing ones accurate), `make precommit` clean.
- No config flag, opt-out knob, or tunable threshold is added to this feature. Do NOT add any Prometheus metric — the spec specifies log lines only.
- The posted review event mapping, the allowlist, and all override gates are unchanged.
- Do NOT touch `CHANGELOG.md` — the changelog entry for this spec lands in the final prompt of this batch. `docs/dod.md` requires an `## Unreleased` entry; for this three-prompt batch that criterion is satisfied by prompt 3, which lands on the same branch. Do NOT add the entry here and do NOT report the missing CHANGELOG entry as a blocker.
- Between this prompt and prompt 2, the new filter has no dedicated tests yet — that is the prescribed order (prompt 2 adds them). All three prompts land on the same branch before any release — do not cut a release from an intermediate state.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass (the existing `pkg/funnel_test.go` suite must stay green under the `Run` rewire; the fake-runner tests with partial JSON such as `{"stats":{"findings_count":1},"errors":[]}` pass through the filter and still return `Ran: true` with a `findings_count` key).
</constraints>

<verification>
- `go test -mod=mod ./pkg/... -count=1` — must exit 0 with the existing suite green under the new `Run` rewire (the filter path, the shared base resolution, and the new pure functions are exercised only indirectly here; the dedicated rows land in prompt 2 of this batch).
- `go vet -mod=mod ./pkg/...` — must exit 0.
- `gofmt -l pkg/funnel.go` — must print nothing (file already formatted; `make precommit` runs fmt).
- Confirm `pkg/prompts/execution.go` and `pkg/prompts/execution_output-format.md` were not modified by this change (their content is unchanged by construction — this prompt touches only `pkg/funnel.go`).
</verification>

<!-- AUDITOR NOTES
1. The shared base resolution (requirement 3) is the spec's DB 3 guard: `Run` calls `resolvedBaseRef` exactly once and both `changedFiles` and `changedLineRanges` consume that single resolved ref, so the changed-file set and the hunk ranges cannot diverge. Do not re-resolve inside `changedLineRanges`.
2. Fail-closed semantics (DB 1, DB 2, AC 5): a hunk computation or parse error returns `Ran: false` with a non-empty `FailDetail` and empty `FindingsJSON` — never `Ran: true` with unfiltered findings. A legitimately empty hunk set is NOT an error (empty ranges → all findings for that file drop), which is the correct outcome for mode-only/binary-only/pure-rename diffs (DB 4).
3. The 0-based → 1-based conversion and the `line == 0` drop are the spec's fixed invariants: `filterFindings` uses `f.Line + 1` against `[newStart, newStart+newCount)` and drops `f.Line == 0` before the range check (a first-line finding drops by decision — the model's whole-file judgment pass still covers it).
4. `filterToChangedLines` returns `(filtered, detail)` rather than `(string, error)` so `Run` stays under the funlen 80-line limit; the FailDetail string is the funnel's existing non-fatal-outcome idiom.
5. The `errors` array is preserved via `json.RawMessage` and normalized to `[]` when absent, keeping the top-level shape frozen.
-->
