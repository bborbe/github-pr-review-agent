// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bborbe/errors"
)

// DefaultReviewChunkEngageAdditions is the default total added-line count above
// which a PR is reviewed in chunks. At or below it the partition yields exactly
// one chunk and the review path is unchanged.
const DefaultReviewChunkEngageAdditions = 500

// DefaultReviewChunkMaxAdditions is the default per-chunk added-line bound. A
// chunk closes before adding a unit would push it past this bound; a unit is
// never split, so a chunk always accepts its first unit.
const DefaultReviewChunkMaxAdditions = 300

// DefaultReviewChunkMaxFiles is the default per-chunk changed-file bound.
const DefaultReviewChunkMaxFiles = 15

// ChangedFile is one entry of the funnel's changed-file inventory: a
// repo-relative changed path together with its added-line count.
type ChangedFile struct {
	Path      string
	Additions int
}

// ReviewChunk is one bounded review unit: the chunk's repo-relative file
// paths (in path-sorted order) and the sum of their added lines.
type ReviewChunk struct {
	Files     []string
	Additions int
}

// ReviewChunkConfig carries the three chunking thresholds read from the
// environment. Every value is >= 1; none of them can disable chunking.
type ReviewChunkConfig struct {
	EngageAdditions int
	MaxAdditions    int
	MaxFiles        int
}

// DefaultReviewChunkConfig returns the three default chunking thresholds.
func DefaultReviewChunkConfig() ReviewChunkConfig {
	return ReviewChunkConfig{
		EngageAdditions: DefaultReviewChunkEngageAdditions,
		MaxAdditions:    DefaultReviewChunkMaxAdditions,
		MaxFiles:        DefaultReviewChunkMaxFiles,
	}
}

// ValidateReviewChunkConfig fails fast when any of the three chunking
// thresholds is below 1. Called by both entry points at startup so a malformed
// value cannot silently disable chunking. The returned error names the
// offending environment variable and its value.
func ValidateReviewChunkConfig(ctx context.Context, cfg ReviewChunkConfig) error {
	switch {
	case cfg.EngageAdditions < 1:
		return errors.Errorf(
			ctx,
			"REVIEW_CHUNK_ENGAGE_ADDITIONS must be at least 1, got %d",
			cfg.EngageAdditions,
		)
	case cfg.MaxAdditions < 1:
		return errors.Errorf(
			ctx,
			"REVIEW_CHUNK_MAX_ADDITIONS must be at least 1, got %d",
			cfg.MaxAdditions,
		)
	case cfg.MaxFiles < 1:
		return errors.Errorf(
			ctx,
			"REVIEW_CHUNK_MAX_FILES must be at least 1, got %d",
			cfg.MaxFiles,
		)
	}
	return nil
}

// reviewUnit is the partition's indivisible building block: one file, or a
// non-test `.go` file together with its `_test.go` sibling. A unit is never
// split across chunks.
type reviewUnit struct {
	paths     []string
	additions int
}

// toChunk renders the unit as a ReviewChunk with its paths in sorted order.
func (u reviewUnit) toChunk() ReviewChunk {
	paths := append([]string(nil), u.paths...)
	sort.Strings(paths)
	return ReviewChunk{Files: paths, Additions: u.additions}
}

// PartitionReviewChunks splits the changed-file inventory into bounded
// chunks. It returns a single chunk holding every file when the total added
// lines are at or below cfg.EngageAdditions (chunking does not engage), and
// otherwise partitions path-sorted greedily into chunks that respect
// cfg.MaxAdditions and cfg.MaxFiles. A file is never split; a file and its
// _test.go sibling are never separated. It is pure and deterministic.
//
// Units: a unit is a single file, except that `p/X_test.go` and `p/X.go` form
// one unit when both are present (the non-test sibling is obtained by stripping
// the `_test.go` suffix and appending `.go`). A `_test.go` file whose non-test
// sibling is not changed, and any non-`.go` file, is its own unit.
//
// Ordering: units are sorted by their lexicographically smallest member path
// (for a sibling pair that is the non-test `X.go` path, since ASCII `.` sorts
// before `_`); a unit's members are emitted in sorted order.
//
// Greedy fill: each unit is appended to the current chunk; the chunk closes
// before a unit that would push it past cfg.MaxAdditions or cfg.MaxFiles. A
// chunk always accepts its first unit even when that unit alone exceeds
// cfg.MaxAdditions — a file is never split. The chunks cover the input exactly
// once.
func PartitionReviewChunks(files []ChangedFile, cfg ReviewChunkConfig) []ReviewChunk {
	units := buildReviewUnits(files)

	total := 0
	for _, u := range units {
		total += u.additions
	}
	if total <= cfg.EngageAdditions {
		return []ReviewChunk{allFilesChunk(files, total)}
	}
	return greedyChunks(units, cfg)
}

// allFilesChunk returns the single chunk holding every input file (sorted).
func allFilesChunk(files []ChangedFile, total int) ReviewChunk {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	return ReviewChunk{Files: paths, Additions: total}
}

// buildReviewUnits groups the changed files into sibling-paired units in
// path-sorted order.
func buildReviewUnits(files []ChangedFile) []reviewUnit {
	additions := make(map[string]int, len(files))
	present := make(map[string]struct{}, len(files))
	paths := make([]string, 0, len(files))
	for _, f := range files {
		additions[f.Path] = f.Additions
		present[f.Path] = struct{}{}
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)

	units := make([]reviewUnit, 0, len(paths))
	consumed := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if _, done := consumed[p]; done {
			continue
		}
		consumed[p] = struct{}{}
		if sibling, ok := testSibling(p, present); ok {
			consumed[sibling] = struct{}{}
			units = append(units, reviewUnit{
				paths:     []string{p, sibling},
				additions: additions[p] + additions[sibling],
			})
			continue
		}
		units = append(units, reviewUnit{paths: []string{p}, additions: additions[p]})
	}
	return units
}

// testSibling returns the `_test.go` path paired with a non-test `.go` path
// when that sibling is part of the changed-file set.
func testSibling(path string, present map[string]struct{}) (string, bool) {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return "", false
	}
	sibling := strings.TrimSuffix(path, ".go") + "_test.go"
	if _, ok := present[sibling]; !ok {
		return "", false
	}
	return sibling, true
}

// greedyChunks walks the units in order and fills bounded chunks, never
// splitting a unit.
func greedyChunks(units []reviewUnit, cfg ReviewChunkConfig) []ReviewChunk {
	chunks := make([]ReviewChunk, 0, len(units))
	var current reviewUnit
	open := false
	for _, u := range units {
		if open && !fits(current, u, cfg) {
			chunks = append(chunks, current.toChunk())
			current = reviewUnit{}
		}
		current.paths = append(current.paths, u.paths...)
		current.additions += u.additions
		open = true
	}
	if open {
		chunks = append(chunks, current.toChunk())
	}
	return chunks
}

// fits reports whether appending unit u keeps the current chunk within the
// configured additions and file bounds.
func fits(current, u reviewUnit, cfg ReviewChunkConfig) bool {
	if current.additions+u.additions > cfg.MaxAdditions {
		return false
	}
	return len(current.paths)+len(u.paths) <= cfg.MaxFiles
}

// ChunkDeadline returns the deadline for one chunk run: the earlier of
// outerDeadline and now + max(remaining time / remainingChunks, 60s), where
// remaining time is outerDeadline - now. It is pure.
//
// remainingChunks is the number of chunk runs left to start, including the one
// this deadline is for, and must be >= 1; a value <= 0 is defensive-only and
// returns outerDeadline (a zero divisor would panic). The 60-second floor means
// a chunk always gets a usable slice even when the remaining budget is nearly
// exhausted, and the outer-deadline cap means the per-chunk share can never
// extend the whole-review budget.
func ChunkDeadline(now, outerDeadline time.Time, remainingChunks int) time.Time {
	if remainingChunks <= 0 {
		return outerDeadline
	}
	remaining := outerDeadline.Sub(now)
	share := remaining / time.Duration(remainingChunks)
	if share < 60*time.Second {
		share = 60 * time.Second
	}
	candidate := now.Add(share)
	if candidate.After(outerDeadline) {
		return outerDeadline
	}
	return candidate
}

// FilterFindingsByBasenames rewrites the funnel findings JSON so that only
// findings whose file's basename is in files survive, recomputing
// stats.findings_count to the surviving count. The top-level shape and every
// other field are untouched. It uses the same basename match the diff-anchor
// filter uses.
//
// A chunk with no matching findings is not an error: it returns valid JSON with
// findings_count 0. Findings with an empty file never survive. errors are
// returned wrapped when the input is not valid JSON or the result cannot be
// marshaled.
func FilterFindingsByBasenames(
	ctx context.Context,
	findingsJSON string,
	files []string,
) (string, error) {
	bases := make(map[string]struct{}, len(files))
	for _, f := range files {
		bases[filepath.Base(f)] = struct{}{}
	}
	var report funnelReport
	if err := json.Unmarshal([]byte(findingsJSON), &report); err != nil {
		return "", errors.Wrapf(ctx, err, "unmarshal chunk funnel findings")
	}
	surviving := map[string][]funnelFinding{}
	count := 0
	for owner, findings := range report.FindingsByOwner {
		kept := make([]funnelFinding, 0, len(findings))
		for _, f := range findings {
			if f.File == "" {
				continue
			}
			if _, ok := bases[filepath.Base(f.File)]; !ok {
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
		return "", errors.Wrapf(ctx, err, "marshal chunk filtered findings")
	}
	return string(out), nil
}

// chunkReviewApprovedReason and chunkReviewChangesReason are the deterministic,
// quote-free merged reasons. They are the literal values in the merged verdict
// block, so they must never contain a double quote.
const (
	chunkReviewApprovedReason = "chunked review: all %d chunks approved"
	chunkReviewChangesReason  = "chunked review: at least one chunk requested changes"
)

// MergeChunkReviews merges the per-chunk review outputs into one review body
// and one verdict. Each chunk contributes a section labelled
// "### Chunk <i>/<n>" followed by that chunk's body with its verdict block
// removed; the merged body ends in exactly one fenced JSON verdict block. The
// merged verdict is worst-wins: request-changes if any chunk parsed to
// request-changes, approve only if every chunk parsed to approve. A chunk
// whose output carries no parseable verdict contributes request-changes
// (ParseVerdict fail-closes).
//
// Each chunk is passed through ApplyBlockingGate before the fold, so a chunk
// whose verdict block carries a blocking comment contributes request-changes
// even when its parsed verdict is approve — the synthesized comment-free
// verdict block must not silently disable that gate. The synthesized block
// also carries the union of the per-chunk concerns_addressed entries (worst
// disposition wins: any not-verified entry is carried through), so the
// downstream unverified-concerns gate stays live on the chunked path.
//
// MergeChunkReviews is pure and deterministic. The caller always passes at
// least one chunk; an empty outputs slice is not a supported input.
func MergeChunkReviews(outputs []string) (string, Result) {
	n := len(outputs)
	sections := make([]string, 0, n)
	concerns := make([]json.RawMessage, 0)
	seen := make(map[string]struct{})
	worst := VerdictApprove

	for i, out := range outputs {
		if ApplyBlockingGate(ParseVerdict(out), out).Verdict != VerdictApprove {
			worst = VerdictRequestChanges
		}
		sections = append(
			sections,
			fmt.Sprintf("### Chunk %d/%d\n\n%s", i+1, n, StripJSONVerdict(out)),
		)
		for _, c := range verdictConcerns(out) {
			key := string(c)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			concerns = append(concerns, c)
		}
	}

	reason := chunkReviewChangesReason
	if worst == VerdictApprove {
		reason = fmt.Sprintf(chunkReviewApprovedReason, n)
	}
	body := strings.Join(sections, "\n\n") + "\n\n" + mergedVerdictBlock(worst, reason, concerns)
	return body, Result{Verdict: worst, Reason: reason}
}

// verdictConcerns returns the raw concerns_addressed entries of the review's
// verdict block, or nil when there is no parseable block.
func verdictConcerns(reviewText string) []json.RawMessage {
	block, _, ok := findVerdictBlock(reviewText)
	if !ok {
		return nil
	}
	var payload struct {
		ConcernsAddressed []json.RawMessage `json:"concerns_addressed"`
	}
	if err := json.Unmarshal([]byte(block), &payload); err != nil {
		return nil
	}
	return payload.ConcernsAddressed
}

// mergedVerdictBlock renders the single fenced JSON verdict block that ends
// the merged body. The concern entries are carried verbatim (worst disposition
// wins because a not-verified entry is never dropped). It is built with
// fmt.Sprintf so it cannot fail.
func mergedVerdictBlock(verdict Verdict, reason string, concerns []json.RawMessage) string {
	if len(concerns) == 0 {
		return fmt.Sprintf(
			"```json\n{\"verdict\":\"%s\",\"reason\":\"%s\"}\n```\n",
			verdict,
			reason,
		)
	}
	joined := make([]string, 0, len(concerns))
	for _, c := range concerns {
		joined = append(joined, string(c))
	}
	return fmt.Sprintf(
		"```json\n{\"verdict\":\"%s\",\"reason\":\"%s\",\"concerns_addressed\":[%s]}\n```\n",
		verdict,
		reason,
		strings.Join(joined, ","),
	)
}

// parseNumstatAdditions parses the added-line count of one
// `git diff --numstat` line. The first tab-separated field is the additions
// count; `-` (a binary entry) and any non-numeric value count 0. The path is
// the remaining fields rejoined, so a path containing a tab still resolves.
func parseNumstatAdditions(line string) (string, int) {
	fields := strings.Split(line, "\t")
	if len(fields) < 3 {
		return "", 0
	}
	additions := 0
	if fields[0] != "-" {
		if n, err := strconv.Atoi(fields[0]); err == nil {
			additions = n
		}
	}
	return strings.Join(fields[2:], "\t"), additions
}
