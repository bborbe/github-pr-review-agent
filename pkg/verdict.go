// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"encoding/json"
	"strings"
)

// Verdict represents the review verdict type
type Verdict string

const (
	VerdictApprove        Verdict = "approve"
	VerdictRequestChanges Verdict = "request-changes"
)

// Result holds the verdict and reason
type Result struct {
	Verdict Verdict
	Reason  string
}

// jsonVerdict is used for unmarshaling JSON verdict blocks (legacy use by StripJSONVerdict)
type jsonVerdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// findLastJSONVerdictBlock returns the LAST JSON object (string content) in
// reviewText that contains a "verdict" field. Handles single-line objects and
// multi-line fenced ```json blocks. Returns empty + false if none found.
//
// The block is anchored on its CLOSING brace: the last '}' within the trailing
// 50-line window is treated as the end of the verdict block, and we walk back
// (with no distance limit) to its matching '{'. Anchoring on the close — not on
// the "verdict" key — fixes the false-negative where a long, well-formed block
// (e.g. an "approve" with a multi-line "comments" array) put its "verdict" key
// dozens of lines above the close, outside the old key-line window, and the
// whole block was silently dropped → fail-closed to request-changes. The window
// still keeps the verdict block anchored to the END of the review (per the
// execution output-format spec: the JSON fence is last with no trailing prose),
// so JSON examples quoted earlier in prose are still ignored.
//
// Limitation: brace matching is byte-level, not string-aware. Balanced braces
// inside a JSON string value (e.g. "reason": "use {} here") net depth-zero and
// match correctly; only UNbalanced braces inside a string (e.g. "see }") could
// mis-match. That mis-match makes json.Unmarshal of the extracted block fail,
// which fail-closes to request-changes — the safe direction, never a false
// approve. This blind spot pre-dates this change; a string-aware tokenizer is
// unwarranted in front of json.Unmarshal.
func findLastJSONVerdictBlock(reviewText string) (string, bool) {
	lines := strings.Split(reviewText, "\n")
	startIdx := 0
	if len(lines) > 50 {
		startIdx = len(lines) - 50
	}
	closeCh := lastCloseBraceInWindow(lines, startIdx)
	if closeCh.line < 0 {
		return "", false
	}
	startCh := matchingOpenBrace(lines, closeCh)
	if startCh.line < 0 {
		return "", false
	}
	block := extractBlock(lines, startCh, closeCh)
	if !strings.Contains(block, `"verdict"`) {
		return "", false
	}
	return block, true
}

type charPos struct{ line, col int }

// lastCloseBraceInWindow returns the position of the last '}' on or after
// startIdx (the trailing-window floor). Returns {-1,-1} if none.
func lastCloseBraceInWindow(lines []string, startIdx int) charPos {
	for li := len(lines) - 1; li >= startIdx; li-- {
		if ci := strings.LastIndexByte(lines[li], '}'); ci >= 0 {
			return charPos{li, ci}
		}
	}
	return charPos{-1, -1}
}

// matchingOpenBrace walks backward from a '}' position, tracking brace depth,
// and returns the position of its matching '{'. No distance limit: the block
// may span arbitrarily many lines above the closing brace. Returns {-1,-1} if
// the braces are unbalanced.
func matchingOpenBrace(lines []string, closePos charPos) charPos {
	depth := 0
	for li := closePos.line; li >= 0; li-- {
		s := lines[li]
		ci := len(s) - 1
		if li == closePos.line {
			ci = closePos.col
		}
		for ; ci >= 0; ci-- {
			switch s[ci] {
			case '}':
				depth++
			case '{':
				depth--
				if depth == 0 {
					return charPos{li, ci}
				}
			}
		}
	}
	return charPos{-1, -1}
}

func extractBlock(lines []string, start, end charPos) string {
	if start.line == end.line {
		return lines[start.line][start.col : end.col+1]
	}
	var b strings.Builder
	b.WriteString(lines[start.line][start.col:])
	b.WriteByte('\n')
	for i := start.line + 1; i < end.line; i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	b.WriteString(lines[end.line][:end.col+1])
	return b.String()
}

// ParseVerdict analyzes Claude review output and determines the appropriate verdict.
// The verdict is binary: approve or request-changes. No other value is returned.
// Fail-closed: empty or unparseable output returns request-changes.
func ParseVerdict(reviewText string) Result {
	if reviewText == "" {
		return Result{
			Verdict: VerdictRequestChanges,
			Reason:  "empty review text",
		}
	}

	block, ok := findLastJSONVerdictBlock(reviewText)
	if !ok {
		return Result{
			Verdict: VerdictRequestChanges,
			Reason:  "no verdict block",
		}
	}

	var jv jsonVerdict
	if err := json.Unmarshal([]byte(block), &jv); err != nil {
		return Result{
			Verdict: VerdictRequestChanges,
			Reason:  "malformed JSON: " + err.Error(),
		}
	}

	// Normalise: lowercase + replace underscores with hyphens
	// so "request_changes", "REQUEST-CHANGES", "Request-Changes" all parse correctly.
	normalized := strings.ToLower(strings.ReplaceAll(jv.Verdict, "_", "-"))
	switch normalized {
	case "approve":
		return Result{Verdict: VerdictApprove, Reason: jv.Reason}
	case "request-changes":
		return Result{Verdict: VerdictRequestChanges, Reason: jv.Reason}
	default:
		return Result{Verdict: VerdictRequestChanges, Reason: "unknown verdict: " + jv.Verdict}
	}
}

// isFailClosedReason reports whether a request-changes Result.Reason came from
// ParseVerdict fail-closing (empty / unparseable / no-verdict-block / unknown
// verdict) rather than from a model-authored reason on a genuine request-changes
// verdict. Used to log a diagnostic ONLY for the suspicious cases — a false
// CHANGES_REQUESTED on an otherwise-clean review — without spamming pod logs on
// every legitimate rejection.
//
// Keep the literals/prefixes in sync with the Reason strings ParseVerdict emits.
func isFailClosedReason(reason string) bool {
	return reason == "empty review text" ||
		reason == "no verdict block" ||
		strings.HasPrefix(reason, "malformed JSON:") ||
		strings.HasPrefix(reason, "unknown verdict:")
}

// lastChars returns up to the final n characters of s (rune-safe), for logging
// the tail of a review body the verdict parser saw without dumping the whole
// thing into pod logs. The raw text is otherwise lost once the Job pod is GC'd.
func lastChars(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// StripJSONVerdict removes the JSON verdict line (and surrounding code fence if present)
// from the review text. Returns the cleaned review text for posting as a PR comment.
// If no JSON verdict found, returns the text unchanged.
func StripJSONVerdict(reviewText string) string {
	lines := strings.Split(reviewText, "\n")
	linesToRemove := findVerdictLinesToRemove(lines)

	if len(linesToRemove) == 0 {
		return reviewText
	}

	return buildCleanedText(lines, linesToRemove)
}

// findVerdictLinesToRemove scans lines and returns a map of line indices to remove
func findVerdictLinesToRemove(lines []string) map[int]bool {
	startIdx := calculateStartIndex(lines)
	linesToRemove := make(map[int]bool)
	inCodeFence := false
	fenceStartIdx := -1

	for i := startIdx; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		if handleCodeFenceStart(line, &inCodeFence, &fenceStartIdx, i) {
			continue
		}

		if handleCodeFenceEnd(line, &inCodeFence, &fenceStartIdx) {
			continue
		}

		if containsVerdictJSON(line) {
			processVerdictLine(lines, i, line, inCodeFence, fenceStartIdx, linesToRemove)
		}
	}

	return linesToRemove
}

// calculateStartIndex returns the index to start searching (last 50 lines)
func calculateStartIndex(lines []string) int {
	if len(lines) > 50 {
		return len(lines) - 50
	}
	return 0
}

// handleCodeFenceStart checks for code fence start and updates state
func handleCodeFenceStart(line string, inCodeFence *bool, fenceStartIdx *int, i int) bool {
	if line == "```json" && !*inCodeFence {
		*inCodeFence = true
		*fenceStartIdx = i
		return true
	}
	return false
}

// handleCodeFenceEnd checks for code fence end and updates state
func handleCodeFenceEnd(line string, inCodeFence *bool, fenceStartIdx *int) bool {
	if line == "```" && *inCodeFence {
		*inCodeFence = false
		*fenceStartIdx = -1
		return true
	}
	return false
}

// containsVerdictJSON checks if a line contains verdict JSON markers
func containsVerdictJSON(line string) bool {
	return strings.Contains(line, `"verdict"`) && strings.Contains(line, `"reason"`)
}

// processVerdictLine validates and marks lines for removal if valid verdict found
func processVerdictLine(
	lines []string,
	i int,
	line string,
	inCodeFence bool,
	fenceStartIdx int,
	linesToRemove map[int]bool,
) {
	if !isValidVerdictJSON(line) {
		return
	}

	// Mark verdict line for removal
	linesToRemove[i] = true

	// If inside code fence, mark fence lines too
	if inCodeFence && fenceStartIdx >= 0 {
		markCodeFenceLinesForRemoval(lines, i, fenceStartIdx, linesToRemove)
	}
}

// isValidVerdictJSON checks if the line contains a valid verdict JSON
func isValidVerdictJSON(line string) bool {
	jsonStr := strings.TrimSpace(line)
	jsonStr = strings.TrimPrefix(jsonStr, "```json")
	jsonStr = strings.TrimSuffix(jsonStr, "```")
	jsonStr = strings.TrimSpace(jsonStr)

	var jv jsonVerdict
	if err := json.Unmarshal([]byte(jsonStr), &jv); err != nil {
		return false
	}

	return jv.Verdict != ""
}

// markCodeFenceLinesForRemoval marks fence start and end lines for removal
func markCodeFenceLinesForRemoval(
	lines []string,
	currentIdx int,
	fenceStartIdx int,
	linesToRemove map[int]bool,
) {
	linesToRemove[fenceStartIdx] = true

	// Find and mark the closing fence
	for j := currentIdx + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "```" {
			linesToRemove[j] = true
			break
		}
	}
}

// buildCleanedText constructs the final text without removed lines
func buildCleanedText(lines []string, linesToRemove map[int]bool) string {
	var cleaned []string
	for i, line := range lines {
		if !linesToRemove[i] {
			cleaned = append(cleaned, line)
		}
	}

	result := strings.Join(cleaned, "\n")
	return strings.TrimRight(result, "\n")
}
