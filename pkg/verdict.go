// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
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

// jsonVerdict is the minimal shape ParseVerdict unmarshals from a located
// verdict block: the verdict value and its optional reason.
type jsonVerdict struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// verdictFieldRegexp extracts the literal value of a "verdict" field from a
// JSON-ish block. Used as a last-resort recovery when a FENCED verdict block is
// well-delimited but not valid JSON (e.g. the model emitted unescaped
// double-quotes inside a string value). It surfaces only a value the model
// actually wrote — it can never invent one — so it cannot turn a genuine
// request-changes into an approve.
var verdictFieldRegexp = regexp.MustCompile(`"verdict"\s*:\s*"([^"]*)"`)

// findVerdictBlock locates the verdict JSON block, preferring a fenced ```json
// block (fence-delimited, so immune to brace/quote content) and falling back to
// the end-anchored brace walk for bare, unfenced JSON. The bool `fenced` reports
// whether the block came from a fence, which gates malformed-JSON recovery in
// ParseVerdict — a fence is a strong "this is THE verdict block" signal; a
// brace-matched blob is not.
func findVerdictBlock(reviewText string) (block string, fenced, ok bool) {
	if b, found := findFencedJSONVerdictBlock(reviewText); found {
		return b, true, true
	}
	if b, found := findLastJSONVerdictBlock(reviewText); found {
		return b, false, true
	}
	return "", false, false
}

// findFencedJSONVerdictBlock returns the body of the LAST ```json fenced block
// that contains a "verdict" field. Because the block is delimited by the fence
// markers — not by brace matching — stray/unbalanced braces and unescaped quotes
// inside string values do not corrupt extraction. This is exactly what the
// byte-level brace walker in findLastJSONVerdictBlock cannot do: a lone '}' in a
// string value inflates its depth count and makes it latch onto a '{' up in the
// prose, extracting a non-JSON blob that then fail-closes to request-changes
// (observed on bborbe/github-update-go-agent#5, 2026-07-21). Returns empty +
// false when no such fenced block exists (bare JSON → brace-walk fallback).
func findFencedJSONVerdictBlock(reviewText string) (string, bool) {
	lines := strings.Split(reviewText, "\n")
	var best string
	found := false
	inFence := false
	var buf []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case !inFence && trimmed == "```json":
			inFence = true
			buf = buf[:0]
		case inFence && trimmed == "```":
			inFence = false
			if block := strings.Join(buf, "\n"); strings.Contains(block, `"verdict"`) {
				best = block
				found = true
			}
		case inFence:
			buf = append(buf, line)
		}
	}
	return best, found
}

// findLastJSONVerdictBlock is the fallback extractor for BARE (unfenced) verdict
// JSON, used only when findFencedJSONVerdictBlock finds no fenced block.
//
// It returns the LAST JSON object (string content) in reviewText that contains a
// "verdict" field. The block is anchored on its CLOSING brace: the last '}'
// within the trailing 50-line window is treated as the end of the verdict block,
// and we walk back (with no distance limit) to its matching '{'. Anchoring on
// the close — not on the "verdict" key — fixes the false-negative where a long,
// well-formed block put its "verdict" key dozens of lines above the close,
// outside the old key-line window. The window keeps the block anchored to the
// END of the review, so JSON examples quoted earlier in prose are still ignored.
//
// Limitation (now covered by the fenced path for fenced blocks): brace matching
// is byte-level, not string-aware. Balanced braces inside a JSON string value
// (e.g. "reason": "use {} here") net depth-zero and match correctly; only
// UNbalanced braces inside a string (e.g. "see }") could mis-match and fail the
// subsequent json.Unmarshal. Since real reviews emit the verdict inside a ```json
// fence, that blind spot no longer reaches production output — this fallback
// only runs for bare JSON, where such blocks are short single-line objects.
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

// recoverFencedVerdict salvages the literal "verdict" field from a block that
// failed json.Unmarshal, but ONLY for a fenced block — a fence is a strong
// "this is THE verdict block" signal, so recovering an approve the model clearly
// stated (past unescaped quotes / unbalanced braces in a string value) beats
// fail-closing it to request-changes. It reads the stated value verbatim and can
// never invent one, so it cannot flip a genuine request-changes to approve.
// Returns the raw verdict value + true on success.
func recoverFencedVerdict(block string, fenced bool) (string, bool) {
	if !fenced {
		return "", false
	}
	m := verdictFieldRegexp.FindStringSubmatch(block)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ParseVerdict analyzes Claude review output and determines the appropriate verdict.
// The verdict is binary: approve or request-changes. No other value is returned.
// Fail-closed: empty or unparseable output returns request-changes.
func ParseVerdict(reviewText string) Result {
	if reviewText == "" {
		return Result{
			Verdict: VerdictRequestChanges,
			Reason:  ReasonEmptyReviewText,
		}
	}

	block, fenced, ok := findVerdictBlock(reviewText)
	if !ok {
		return Result{
			Verdict: VerdictRequestChanges,
			Reason:  ReasonNoVerdictBlock,
		}
	}

	var jv jsonVerdict
	if err := json.Unmarshal([]byte(block), &jv); err != nil {
		recovered, ok := recoverFencedVerdict(block, fenced)
		if !ok {
			return Result{
				Verdict: VerdictRequestChanges,
				Reason:  "malformed JSON: " + err.Error(),
			}
		}
		jv.Verdict = recovered
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

// ReasonNoVerdictBlock and ReasonEmptyReviewText are the two ParseVerdict
// fail-closed reasons that mean the run produced NO review at all. They are the
// crash signature: a run whose result carries no verdict block (for example a
// tool-error marker such as `(tool 'WebFetch' unavailable)`) fail-closes to
// request-changes, and posting that would publish a blocking review the model
// never wrote — indistinguishable from a real change request, and it blocks the
// merge exactly like one (bborbe/nuke#446).
//
// They are deliberately separated from the other fail-closed reasons: those are
// legitimate code-side demotions whose review MUST still post. The string values
// are compared literally by ParseVerdict and isFailClosedReason and must not change.
const (
	ReasonNoVerdictBlock  = "no verdict block"
	ReasonEmptyReviewText = "empty review text"
)

// IsNoReviewReason reports whether a ParseVerdict fail-closed result means the
// run produced no review at all — the only case in which the post may be
// suppressed.
//
// `empty review text` always qualifies: there is no review.
//
// `no verdict block` does NOT qualify on its own, and this is the subtle half.
// ParseVerdict returns that reason both for a tool-error marker (the crash
// signature, `(tool 'WebFetch' unavailable)`) AND for a real review whose model
// omitted the JSON verdict block — for example a body whose only content is a
// `### Must Fix` section. The second is a review the model DID write: before
// this gate existed it posted its fail-closed request-changes, which is the
// correct outcome for a review that found must-fix issues. Suppressing it would
// hide real blocking findings, a worse failure than the one being fixed. So the
// body is consulted: only a body with no review content at all is suppressed.
//
// The discriminating marker is a markdown heading. The review rubric is
// heading-structured (`## Review`, `### Must Fix`, `## Blocking`) and the
// documented fallback verdict path is a section-header scan, so a heading is
// what a real review has and a tool-error marker does not.
func IsNoReviewReason(reason, reviewBody string) bool {
	if reason == ReasonEmptyReviewText {
		return true
	}
	if reason != ReasonNoVerdictBlock {
		return false
	}
	return !hasReviewContent(reviewBody)
}

// hasReviewContent reports whether a review body carries anything a reader would
// recognise as a review, i.e. at least one ATX markdown heading. A bare
// tool-error marker such as `(tool 'WebFetch' unavailable)` has none.
func hasReviewContent(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		hashes := 0
		for hashes < len(trimmed) && trimmed[hashes] == '#' {
			hashes++
		}
		// ATX heading: 1-6 '#', then whitespace or end of line. A longer run of
		// '#' is not a heading, and '#'-prefixed prose without a space is not
		// one either — neither should count as review content.
		if hashes >= 1 && hashes <= 6 &&
			(hashes == len(trimmed) || trimmed[hashes] == ' ' || trimmed[hashes] == '\t') {
			return true
		}
	}
	return false
}

// ReasonFunnelDidNotRun is the fail-closed Result.Reason set when the Go-side
// mechanical funnel could not run and the model nonetheless produced `approve`.
// The verdict is overridden to request-changes so an unverified MUST-tier pass
// never posts as a clean approve. Recognized by isFailClosedReason for logging.
const ReasonFunnelDidNotRun = "mechanical funnel did not run"

// ReasonConcernsNotVerified is the fail-closed Result.Reason set when the review
// emits approve while one or more ## Plan concerns carry an unexamined
// `not-verified` disposition (or an absent/unrecognised one, treated fail-safe)
// AND the run consumed at least 0.8 of its soft time budget — a budget-heavy
// run that stopped before examining its concerns. The verdict is overridden to
// request-changes so an incomplete review can never green-light a PR; a
// `not-verified` on a run that finished well inside the budget is a mislabel
// and does not demote (see DemotesUnverifiedConcerns). The string value is
// compared literally by isFailClosedReason and must not change.
const ReasonConcernsNotVerified = "one or more ## Plan concerns not verified"

// ReasonBlockingFindingPresent is the fail-closed Result.Reason set when the
// Go-side blocking gate sees an `approve` verdict whose verdict block carries
// a comment marked `blocking: true` (or whose severity fallback says a
// comment blocks) — the model contradicted itself, or its severity roll-up
// contradicts the approve. The verdict is overridden to request-changes so
// an approve never posts while a blocking finding exists. Recognized by
// isFailClosedReason for logging.
const ReasonBlockingFindingPresent = "blocking finding present"

// HasUnverifiedConcerns reports whether the review body's verdict JSON marks any
// ## Plan concern as an unexamined admission. An object entry whose `disposition`
// is neither `addressed` nor `not-an-issue` — including absent and unrecognised
// values, which stay fail-safe — is an admission (spec 004 Desired Behavior 3);
// a legacy bare-string entry whose lowercased text contains `not verified` or
// `unverified` is an admission (spec 004 Desired Behavior 5, implemented with
// strings.Contains, never a regex). Every other entry is skipped and the scan
// continues over the whole list. Returns false for a missing/malformed verdict
// block, an empty concerns_addressed, or a concerns_addressed value that is not
// a list (no over-trigger). Concern prose is never inspected on the object path:
// only the disposition field is read, so no code path can pattern-match wording.
func HasUnverifiedConcerns(reviewText string) bool {
	block, _, ok := findVerdictBlock(reviewText)
	if !ok {
		return false
	}
	var payload struct {
		ConcernsAddressed []json.RawMessage `json:"concerns_addressed"`
	}
	if err := json.Unmarshal([]byte(block), &payload); err != nil {
		return false
	}
	for _, raw := range payload.ConcernsAddressed {
		// Legacy bare-string entry: the flag wording is the admission (spec 004
		// Desired Behavior 5 — a task file written by an older binary is genuinely
		// re-parsed after an upgrade).
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			lower := strings.ToLower(s)
			if strings.Contains(lower, "not verified") || strings.Contains(lower, "unverified") {
				return true
			}
			continue
		}
		// Object entry: only the disposition is read — concern/detail prose is
		// never inspected. `addressed` and `not-an-issue` pass; `not-verified`,
		// absent, and unrecognised values stay fail-safe admissions.
		var obj struct {
			Disposition string `json:"disposition"`
		}
		if err := json.Unmarshal(raw, &obj); err == nil {
			switch obj.Disposition {
			case "addressed", "not-an-issue":
				continue
			default: // not-verified, absent, or unrecognised -> admission
				return true
			}
		}
		// Neither a string nor an object (number, nested array): uninterpretable —
		// skip it and evaluate the remaining entries.
	}
	return false
}

// unverifiedConcernsBudgetNumerator and unverifiedConcernsBudgetDenominator
// express the soft-budget fraction (0.8) at which a `not-verified` disposition
// becomes credible enough to fail-close an approve. The threshold is compared in
// integer form (elapsed*denominator >= budget*numerator): it is exact at the
// boundary, and it never divides by the budget — a zero or unknown budget
// therefore demotes (fail-safe) instead of producing a NaN comparison that
// would silently let the approve through.
const (
	unverifiedConcernsBudgetNumerator   = 4
	unverifiedConcernsBudgetDenominator = 5
)

// DemotesUnverifiedConcerns reports whether an `approve` verdict must be
// fail-closed because the review flags an unexamined concern AND the run
// consumed at least 0.8 of its soft budget. A `not-verified` disposition on a
// run that finished well inside the budget is provably a mislabel — the model
// had budget left and examined the concern — so the approve stands; on a run
// that consumed the budget the disposition is credible and the approve
// fail-closes with ReasonConcernsNotVerified. The predicate reads no clock of
// its own: elapsed is measured by the caller (runWithSoftBudget) with the same
// start := time.Now() / time.Since(start) shape pkg/funnel.go uses. Concern
// prose is never inspected — only HasUnverifiedConcerns' disposition /
// bare-string admission signal feeds the decision, so no wording can argue its
// way past the gate in either direction.
func DemotesUnverifiedConcerns(reviewText string, elapsed, budget time.Duration) bool {
	if !HasUnverifiedConcerns(reviewText) {
		return false
	}
	return elapsed*unverifiedConcernsBudgetDenominator >= budget*unverifiedConcernsBudgetNumerator
}

// HasBlockingFinding reports whether the review body's verdict JSON carries
// any comment that blocks the merge. A comment's `blocking` field is
// authoritative when present, regardless of severity and regardless of
// whether its `blocking_reason` is empty (the reason is a model-quality
// requirement, not a gate condition). When `blocking` is absent, severity
// falls back: `critical` and `major` block, `nit` and `minor` do not —
// reproducing today's severity roll-up exactly for a model that omits the
// new field. A comment that is unparseable, or carries neither `blocking`
// nor `severity`, is skipped and does not block; a missing or malformed
// `comments` list never over-triggers. Returns false for a missing or
// malformed verdict block.
func HasBlockingFinding(reviewText string) bool {
	block, _, ok := findVerdictBlock(reviewText)
	if !ok {
		return false
	}
	var payload struct {
		Comments []json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal([]byte(block), &payload); err != nil {
		return false
	}
	for _, raw := range payload.Comments {
		var comment struct {
			Blocking *bool  `json:"blocking"`
			Severity string `json:"severity"`
		}
		if err := json.Unmarshal(raw, &comment); err != nil {
			continue // unparseable comment: skip, never over-trigger
		}
		if comment.Blocking != nil {
			if *comment.Blocking {
				return true // explicit blocking: true wins, regardless of severity/reason
			}
			continue // explicit blocking: false: this comment does not block — keep scanning
		}
		if comment.Severity == "critical" || comment.Severity == "major" {
			return true // severity fallback for a comment missing `blocking`
		}
		// nit/minor, absent severity, or unrecognised severity: does not block
	}
	return false
}

// ApplyBlockingGate is the Go-side blocking override gate: an `approve`
// verdict whose verdict block carries any blocking comment is overridden to
// `request-changes` with ReasonBlockingFindingPresent. Every other verdict
// passes through unchanged, so it only ever converts approve →
// request-changes (never the reverse). Composed after the funnel and
// concerns gates in postAndRoute, it never rewrites an already-fail-closed
// verdict, so the earlier, coarser gates keep their reasons (funnel first).
func ApplyBlockingGate(verdict Result, reviewText string) Result {
	if verdict.Verdict == VerdictApprove && HasBlockingFinding(reviewText) {
		return Result{Verdict: VerdictRequestChanges, Reason: ReasonBlockingFindingPresent}
	}
	return verdict
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
	return reason == ReasonEmptyReviewText ||
		reason == ReasonNoVerdictBlock ||
		reason == ReasonFunnelDidNotRun ||
		reason == ReasonConcernsNotVerified ||
		reason == ReasonBlockingFindingPresent ||
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

// blankRunRegexp collapses a run of three or more newlines to a blank line. It
// only runs after at least one block was removed, so a body with no verdict
// block is returned byte-identical to its input.
var blankRunRegexp = regexp.MustCompile(`\n{3,}`)

// StripJSONVerdict removes every JSON verdict block (with its surrounding
// ```json fence when present) from the review text. It iterates
// findVerdictBlock until no block remains, so a chunked review loses every
// per-chunk block and the aggregate block. A block the locator cannot delimit
// is left in place (fail-open): the posted event is derived from the verdict
// alone and stays correct either way. If no block is found, the text is
// returned unchanged.
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

// VerdictSummary returns the "summary" field of the located verdict block, or ""
// when there is no block or the field is absent/unparseable.
//
// It exists because StripJSONVerdict can legitimately leave nothing behind: a
// review whose entire body is the fenced verdict block strips to an empty
// string, and the poster then substitutes "automated review — no summary
// produced" for a summary the model did write. That substitution is a body
// disagreeing with the review — the exact class of defect this package exists to
// remove — so the summary has to be read from the block before the stripped body
// is trusted to be the body.
//
// For a chunked review the locator returns the LAST block, which is the aggregate
// roll-up: the one whose summary describes the whole review.
func VerdictSummary(reviewText string) string {
	block, _, ok := findVerdictBlock(reviewText)
	if !ok || block == "" {
		return ""
	}
	var v struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(block), &v); err != nil {
		return ""
	}
	return strings.TrimSpace(v.Summary)
}

// ReviewBody returns the review text a reader should see: the body with every
// verdict block stripped, falling back to the located block's own "summary"
// field when the strip leaves nothing behind. A review whose entire body is the
// verdict block would otherwise post empty — or, one layer up, the poster's
// "no summary produced" placeholder, which asserts the model wrote no summary
// when it did.
func ReviewBody(reviewText string) string {
	body := StripJSONVerdict(reviewText)
	if strings.TrimSpace(body) != "" {
		return body
	}
	return VerdictSummary(reviewText)
}

// removeVerdictBlock removes one located block. For a bare block it removes the
// block substring; for a fenced block it removes the opening ```json line, the
// block lines, and the closing ``` line. Returns ok=false when the fence cannot
// be delimited, so the caller fails open and leaves the text untouched.
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
