// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// codeFenceRegexp matches a run of three or more backticks — a markdown code
// fence — used to neutralise fence sequences in PR-author-controlled finding text.
var codeFenceRegexp = regexp.MustCompile("`{3,}")

// FunnelResult is the outcome of running the ast-grep mechanical funnel.
type FunnelResult struct {
	// Ran is true when the runner executed cleanly and produced findings JSON.
	Ran bool
	// FindingsJSON is the runner's stdout (a single JSON object) when Ran is true.
	FindingsJSON string
	// FailDetail explains why the funnel could not run when Ran is false. The
	// execution prompt surfaces it as a fail-closed condition so a review can
	// never silently approve as though the mechanical pass had succeeded.
	FailDetail string
}

//counterfeiter:generate -o ../mocks/funnel-runner.go --fake-name FunnelRunner . FunnelRunner

// FunnelRunner runs the deterministic ast-grep mechanical funnel over the
// changed files of a checked-out worktree and returns its JSON findings.
//
// The agent runs the funnel itself rather than steering the review model to
// invoke it. A weak review model (e.g. MiniMax) wraps the runner in forms the
// execution allowlist cannot match (`> redirect`, `bash -c`, `$RUNNER`), gets
// denied, and silently falls back to a judgment-only review — dropping the
// entire MUST-tier mechanical pass. Running it in Go removes both the
// allowlist-vs-invocation-form fragility and the model's ability to skip it.
type FunnelRunner interface {
	Run(ctx context.Context, worktreePath string, baseRef string) (FunnelResult, error)
}

// funnelRunner is the production FunnelRunner. It resolves the operator-shipped
// ast-grep-runner.sh from the trusted CLAUDE_CONFIG_DIR and diff-scopes the scan
// to the PR's changed files.
type funnelRunner struct {
	claudeConfigDir claudelib.ClaudeConfigDir
}

// NewFunnelRunner constructs a FunnelRunner bound to a Claude config dir; the
// runner script is resolved beneath it (container: /home/claude/.claude,
// local cmd/run-task: ~/.claude).
func NewFunnelRunner(claudeConfigDir claudelib.ClaudeConfigDir) FunnelRunner {
	return &funnelRunner{claudeConfigDir: claudeConfigDir}
}

// runnerPath returns the resolved literal path of the coding plugin's funnel runner.
func (r *funnelRunner) runnerPath() string {
	return filepath.Join(
		string(r.claudeConfigDir),
		"plugins", "marketplaces", "coding", "scripts", "ast-grep-runner.sh",
	)
}

// Run diff-scopes to the PR's changed files and executes the funnel runner.
// It returns FunnelResult (never a bare error) for the two expected non-fatal
// outcomes — runner missing / non-zero tooling exit — so the caller can surface
// them as a fail-closed prompt condition. A non-nil error is reserved for
// unexpected Go-level failures (process could not be started, context cancel).
func (r *funnelRunner) Run(
	ctx context.Context,
	worktreePath string,
	baseRef string,
) (FunnelResult, error) {
	runner := r.runnerPath()
	if _, statErr := os.Stat(runner); statErr != nil {
		return FunnelResult{
			Ran:        false,
			FailDetail: "ast-grep runner script not found at " + runner,
		}, nil
	}

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

	args := append([]string{worktreePath}, files...)
	start := time.Now()
	// #nosec G204 -- runner is a fixed operator-shipped script beneath the trusted
	// CLAUDE_CONFIG_DIR; args are the target dir plus repo-relative changed-file
	// paths derived from git diff, never raw PR-author input.
	cmd := exec.CommandContext(ctx, runner, args...)
	cmd.Dir = worktreePath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	elapsedMs := time.Since(start).Milliseconds()

	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			glog.Warningf(
				"exec ast-grep-runner start-failed elapsed_ms=%d stderr=%q",
				elapsedMs, lastChars(stderr.String(), 300),
			)
			return FunnelResult{}, errors.Wrapf(ctx, runErr, "exec ast-grep runner %s", runner)
		}
		// Runner documents exit 2 = ast-grep/sg binary missing or usage error.
		glog.Warningf(
			"exec ast-grep-runner exit=%d elapsed_ms=%d files=%d stderr=%q",
			exitErr.ExitCode(), elapsedMs, len(files), lastChars(stderr.String(), 300),
		)
		return FunnelResult{
			Ran: false,
			FailDetail: "ast-grep runner exited non-zero (" +
				strings.TrimSpace(lastChars(stderr.String(), 200)) + ")",
		}, nil
	}

	glog.Infof("exec ast-grep-runner exit=0 elapsed_ms=%d files=%d", elapsedMs, len(files))

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
}

// neutralizeCodeFences replaces any run of three-or-more backticks with a
// visually-similar sequence that cannot close a markdown code fence, so
// PR-author-controlled finding text embedded in the execution prompt cannot
// escape its ```json block.
func neutralizeCodeFences(s string) string {
	return codeFenceRegexp.ReplaceAllString(s, "[code-fence]")
}

// resolvedBaseRef resolves the PR diff base, preferring origin/<baseRef>
// (the worktree is a --local clone of a mirror that carries all branches; a
// best-effort fetch covers the case where it is missing) with a bare-ref
// fallback, and returns the ref string to pass to git diff. Both the
// changed-files scan and the hunk computation consume this single resolution
// so they can never disagree on the base.
func (r *funnelRunner) resolvedBaseRef(
	ctx context.Context,
	worktreePath string,
	baseRef string,
) (string, error) {
	// Best-effort: make sure origin/<baseRef> exists locally. Ignore failure —
	// the diff below falls back to the bare ref name.
	_ = r.git(ctx, worktreePath, "fetch", "origin", baseRef+":refs/remotes/origin/"+baseRef)
	if _, err := r.gitOutput(ctx, worktreePath, "diff", "--name-only", "origin/"+baseRef+"...HEAD"); err == nil {
		return "origin/" + baseRef, nil
	}
	_, err := r.gitOutput(ctx, worktreePath, "diff", "--name-only", baseRef+"...HEAD")
	if err == nil {
		return baseRef, nil
	}
	// Wrap the last failed git diff error (gitOutput already surfaces stderr)
	// rather than discarding it — the surfaced detail is identical, but the
	// underlying cause stays in the error chain per DoD error hygiene.
	return "", errors.Wrapf(ctx, err, "git diff --name-only base_ref=%s", baseRef)
}

// changedFiles returns the PR's changed file paths (relative to
// worktreePath) by diffing HEAD against the resolved base ref.
func (r *funnelRunner) changedFiles(
	ctx context.Context,
	worktreePath string,
	resolvedBase string,
) ([]string, error) {
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

// git runs a git subcommand in worktreePath, discarding stdout.
func (r *funnelRunner) git(ctx context.Context, worktreePath string, args ...string) error {
	// #nosec G204 -- git subcommand args are hardcoded verbs plus a validated base ref.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", worktreePath}, args...)...)
	if err := cmd.Run(); err != nil {
		return errors.Wrapf(ctx, err, "git %s", args[0])
	}
	return nil
}

// gitOutput runs a git subcommand in worktreePath and returns trimmed stdout.
func (r *funnelRunner) gitOutput(
	ctx context.Context,
	worktreePath string,
	args ...string,
) (string, error) {
	// #nosec G204 -- git subcommand args are hardcoded verbs plus a validated base ref.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", worktreePath}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.Errorf(ctx, "git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

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
	Stats           funnelStats                `json:"stats"`
	FindingsByOwner map[string][]funnelFinding `json:"findings_by_owner"`
	Errors          []json.RawMessage          `json:"errors"`
}

// findingSurvives reports whether a finding is anchored to a line the PR
// changed: its file's basename must be in changedBases and its 1-based line
// (0-based ast-grep line + 1) must fall inside one of the changed ranges.
// Findings with an empty file or line == 0 never survive.
func findingSurvives(
	f funnelFinding,
	changedBases map[string]struct{},
	ranges map[string][]lineRange,
) bool {
	if f.File == "" {
		return false
	}
	if f.Line == 0 {
		return false
	}
	base := filepath.Base(f.File)
	if _, ok := changedBases[base]; !ok {
		return false
	}
	return lineInRanges(f.Line+1, ranges[base])
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
			if !findingSurvives(f, changedBases, ranges) {
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

// changedLineRanges returns the PR's per-file changed-line ranges by
// diffing HEAD against the resolved base ref with --unified=0, where only
// the new-file "+" lines count as changed. Any failure to obtain or parse
// the hunk data is returned as an error so the caller can fail closed.
func (r *funnelRunner) changedLineRanges(
	ctx context.Context,
	worktreePath string,
	resolvedBase string,
) (map[string][]lineRange, error) {
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
