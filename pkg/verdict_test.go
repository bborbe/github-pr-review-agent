// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"os"
	"strings"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Parse", func() {
	var (
		reviewText string
		result     pkg.Result
	)

	JustBeforeEach(func() {
		result = pkg.ParseVerdict(reviewText)
	})

	Context("empty review text", func() {
		BeforeEach(func() {
			reviewText = ""
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'empty review text'", func() {
			Expect(result.Reason).To(Equal("empty review text"))
		})
	})

	Context("review with no recognizable sections", func() {
		BeforeEach(func() {
			reviewText = "This is just some random text without any sections."
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section containing items", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

- Security issue in authentication
- SQL injection vulnerability`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section saying *None*", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

*None*

### Should Fix (Important)

- Add error handling`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section saying None identified", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

None identified.

### Should Fix (Important)

- Improve error messages`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section saying none (lowercase)", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

none

### Nice to Have (Optional)

- Add docstrings`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix section that is empty", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)


### Should Fix (Important)

- Add tests`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with only Should Fix and Nice to Have sections", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

- Add error handling
- Improve logging

### Nice to Have (Optional)

- Add docstrings
- Refactor for clarity`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with only Should Fix section", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

- Add error handling`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with only Nice to Have section", func() {
		BeforeEach(func() {
			reviewText = `### Nice to Have (Optional)

- Add comments`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with h2 Must Fix instead of h3", func() {
		BeforeEach(func() {
			reviewText = `## Must Fix (Critical)

- Critical security flaw`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with h2 Must Fix saying None", func() {
		BeforeEach(func() {
			reviewText = `## Must Fix (Critical)

None

## Should Fix (Important)

- Add tests`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with case variations in Must Fix header", func() {
		BeforeEach(func() {
			reviewText = `### MUST FIX (Critical)

- Issue found`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})
	})

	Context("review with Must Fix containing 'No issues found' text", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix (Critical)

No issues found.

### Should Fix (Important)

- Improve code structure`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix at end of document", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

- Add tests

### Must Fix (Critical)

- Security vulnerability`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("review with Must Fix None separated by horizontal rules", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix

None.

---

### Should Fix

None.

---

### Nice to Have

- Minor style improvement`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("complex review with multiple sections", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

## Summary

This PR adds new features.

### Must Fix (Critical)

*None*

### Should Fix (Important)

- Add error handling in main.go:45
- Missing input validation

### Nice to Have (Optional)

- Add docstrings
- Refactor for clarity

## Conclusion

Overall good work!`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("JSON verdict on bare line", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content here.

{"verdict": "approve", "reason": "all checks passed"}`
		})

		It("returns VerdictApprove from JSON", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		})

		It("returns reason from JSON", func() {
			Expect(result.Reason).To(Equal("all checks passed"))
		})
	})

	Context("JSON verdict inside code fence", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content here.

` + "```json" + `
{"verdict": "request-changes", "reason": "critical security issues"}
` + "```" + ``
		})

		It("returns VerdictRequestChanges from JSON", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason from JSON", func() {
			Expect(result.Reason).To(Equal("critical security issues"))
		})
	})

	Context("JSON verdict with extra whitespace", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some content.

   {"verdict": "comment", "reason": "informational only"}   `
		})

		It("returns VerdictRequestChanges (JSON comment rejected)", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns unknown verdict reason from JSON", func() {
			Expect(result.Reason).To(Equal("unknown verdict: comment"))
		})
	})

	Context("invalid JSON falls back to heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix

- Security issue

{"verdict": "approve", "reason": invalid json}`
		})

		It("returns VerdictRequestChanges from heuristic", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns malformed JSON reason", func() {
			Expect(result.Reason).To(HavePrefix("malformed JSON:"))
		})
	})

	Context("missing verdict field falls back to heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix

- Add tests

{"reason": "just a reason"}`
		})

		It("returns VerdictRequestChanges from heuristic", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("unknown verdict value falls back to heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Nice to Have

- Refactor code

{"verdict": "unknown-verdict", "reason": "some reason"}`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns unknown verdict reason", func() {
			Expect(result.Reason).To(Equal("unknown verdict: unknown-verdict"))
		})
	})

	Context("no JSON at all uses heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Must Fix

None

### Should Fix

- Add error handling`
		})

		It("returns VerdictRequestChanges from heuristic", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("JSON verdict in middle of review is ignored", func() {
		BeforeEach(func() {
			// Build a review with >50 lines so JSON in middle is outside the search window
			var lines []string
			lines = append(lines, "# Code Review")
			lines = append(lines, "")
			lines = append(lines, `{"verdict": "approve", "reason": "this should be ignored"}`)
			lines = append(lines, "")

			// Add 60 more lines to push JSON out of the last-50 window
			for i := 0; i < 60; i++ {
				lines = append(lines, "Some review content line.")
			}

			lines = append(lines, "### Must Fix")
			lines = append(lines, "- Critical issue")

			reviewText = strings.Join(lines, "\n")
		})

		It("ignores JSON in middle and uses heuristic", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("JSON verdict with 'comment' value is rejected — falls to heuristic", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix

- Add error handling

{"verdict": "comment", "reason": "informational only"}`
		})

		It("returns VerdictRequestChanges (not comment)", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns unknown verdict reason from JSON", func() {
			Expect(result.Reason).To(Equal("unknown verdict: comment"))
		})
	})

	Context("Should Fix only — non-empty content triggers request-changes", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

- Improve error handling in pkg/server.go:42`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("Should Fix present but empty — approve", func() {
		BeforeEach(func() {
			reviewText = `### Should Fix (Important)

None.

### Nice to Have

- Add docstrings`
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("empty input returns request-changes (fail-closed)", func() {
		BeforeEach(func() {
			reviewText = ""
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'empty review text'", func() {
			Expect(result.Reason).To(Equal("empty review text"))
		})
	})

	Context("unparseable input (no sections) returns request-changes (fail-closed)", func() {
		BeforeEach(func() {
			reviewText = "Just some random prose without any review sections."
		})

		It("returns VerdictRequestChanges", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("returns reason 'no verdict block'", func() {
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})

	Context("multi-line fenced JSON with new spec-025 schema (no reason field) — approve", func() {
		BeforeEach(func() {
			reviewText = "# Code Review\n\nThe PR adds an HTML comment to README.md.\n\n" +
				"```json\n" +
				"{\n" +
				"  \"verdict\": \"approve\",\n" +
				"  \"summary\": \"Trivial doc-only change, no findings.\",\n" +
				"  \"comments\": [],\n" +
				"  \"concerns_addressed\": [\n" +
				"    \"correctness: no logic changes\"\n" +
				"  ]\n" +
				"}\n" +
				"```"
		})

		It("returns VerdictApprove from the multi-line block", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		})
	})

	Context("multi-line fenced JSON with new spec-025 schema — request-changes", func() {
		BeforeEach(func() {
			reviewText = "# Code Review\n\nThe PR has critical issues.\n\n" +
				"```json\n" +
				"{\n" +
				"  \"verdict\": \"request-changes\",\n" +
				"  \"summary\": \"Critical issues found.\",\n" +
				"  \"comments\": []\n" +
				"}\n" +
				"```"
		})

		It("returns VerdictRequestChanges from the multi-line block", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		})
	})

	Context("malformed JSON in fenced block falls back to heuristic", func() {
		BeforeEach(func() {
			reviewText = "```json\n{verdict: invalid no quotes\n```\n## Must Fix\n- problem"
		})

		It("returns VerdictRequestChanges from heuristic must-fix items", func() {
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
			Expect(result.Reason).To(Equal("no verdict block"))
		})
	})
})

var _ = Describe("StripJSONVerdict", func() {
	var (
		reviewText string
		stripped   string
	)

	JustBeforeEach(func() {
		stripped = pkg.StripJSONVerdict(reviewText)
	})

	Context("removes JSON verdict on bare line", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content here.

{"verdict": "approve", "reason": "all checks passed"}`
		})

		It("removes the JSON line", func() {
			Expect(stripped).NotTo(ContainSubstring(`"verdict"`))
			Expect(stripped).To(ContainSubstring("Some review content here."))
		})
	})

	Context("removes JSON verdict inside code fence", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content.

` + "```json" + `
{"verdict": "approve", "reason": "looks good"}
` + "```" + `

End of review.`
		})

		It("removes the JSON and code fence", func() {
			Expect(stripped).NotTo(ContainSubstring(`"verdict"`))
			Expect(stripped).NotTo(ContainSubstring("```json"))
			Expect(stripped).To(ContainSubstring("Some review content."))
			Expect(stripped).To(ContainSubstring("End of review."))
		})
	})

	Context("preserves text when no JSON found", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some review content without JSON verdict.`
		})

		It("returns unchanged text", func() {
			Expect(stripped).To(Equal(reviewText))
		})
	})

	Context("handles multiple trailing newlines", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Some content.

{"verdict": "approve", "reason": "good"}


`
		})

		It("collapses trailing blank lines", func() {
			Expect(stripped).NotTo(ContainSubstring(`"verdict"`))
			Expect(stripped).NotTo(HaveSuffix("\n\n\n"))
			Expect(stripped).To(ContainSubstring("Some content."))
		})
	})

	Context("preserves other JSON in review", func() {
		BeforeEach(func() {
			reviewText = `# Code Review

Here's an example:
` + "```json" + `
{"config": "value"}
` + "```" + `

{"verdict": "approve", "reason": "ok"}`
		})

		It("only removes verdict JSON", func() {
			Expect(stripped).NotTo(ContainSubstring(`"verdict"`))
			Expect(stripped).To(ContainSubstring(`"config"`))
		})
	})

	Context("strips a pretty-printed multi-line verdict block (vault-cli#243)", func() {
		BeforeEach(func() {
			body, err := os.ReadFile("testdata/review_bborbe_vault_cli_243.md")
			Expect(err).NotTo(HaveOccurred())
			reviewText = string(body)
		})

		It("removes the whole block so no verdict JSON reaches the body", func() {
			Expect(stripped).NotTo(ContainSubstring(`"verdict"`))
			Expect(stripped).NotTo(ContainSubstring(`"summary"`))
			Expect(stripped).NotTo(ContainSubstring(`"comments"`))
			Expect(stripped).NotTo(ContainSubstring(`"concerns_addressed"`))
			Expect(stripped).NotTo(ContainSubstring("```"))
		})

		It("keeps every numbered prose item", func() {
			Expect(stripped).To(ContainSubstring("1. **`--by` enforcement"))
			Expect(stripped).To(ContainSubstring("2. **Atomicity of flag+provenance write"))
			Expect(stripped).To(ContainSubstring("3. **`flag_set_at`"))
			Expect(stripped).To(ContainSubstring("4. **`ClearFlag` removes all three keys"))
			Expect(stripped).To(ContainSubstring("5. **`flag_set_by`/`flag_set_at`"))
			Expect(stripped).To(ContainSubstring("6. **`NewFrontmatterSetOperation`"))
		})
	})

	Context("strips every verdict block in a chunked review", func() {
		BeforeEach(func() {
			reviewText = "### Chunk 1/3\n\nFirst chunk prose.\n\n```json\n" +
				`{"verdict":"approve","summary":"a","comments":[],"concerns_addressed":[]}` +
				"\n```\n\n### Chunk 2/3\n\nSecond chunk prose.\n\n```json\n" +
				`{"verdict":"request-changes","summary":"b","comments":[],"concerns_addressed":[]}` +
				"\n```\n\n### Chunk 3/3\n\nThird chunk prose.\n\n```json\n" +
				`{"verdict":"request-changes","reason":"chunked review: at least one chunk requested changes"}` +
				"\n```\n"
		})

		It("removes the per-chunk and aggregate blocks while keeping the prose", func() {
			Expect(strings.Count(stripped, `"verdict"`)).To(Equal(0))
			Expect(strings.Count(stripped, `"summary"`)).To(Equal(0))
			Expect(strings.Count(stripped, `"comments"`)).To(Equal(0))
			Expect(strings.Count(stripped, `"concerns_addressed"`)).To(Equal(0))
			Expect(strings.Count(stripped, "```json")).To(Equal(0))
			Expect(stripped).To(ContainSubstring("### Chunk 1/3"))
			Expect(stripped).To(ContainSubstring("### Chunk 2/3"))
			Expect(stripped).To(ContainSubstring("### Chunk 3/3"))
		})
	})

	Context("fails open when the verdict block cannot be delimited", func() {
		BeforeEach(func() {
			reviewText = "# Review\n\nSome prose.\n\n```json\n{\"verdict\": \"approve\""
		})

		It("returns the text unchanged and parses fail-closed", func() {
			Expect(stripped).To(Equal(reviewText))
			Expect(pkg.ParseVerdict(reviewText).Verdict).To(Equal(pkg.VerdictRequestChanges))
		})
	})
})

var _ = Describe("VerdictSummary", func() {
	var reviewText string

	// The regression this covers (measured on bborbe/pr-review-fixtures#20,
	// 2026-11-05): the model emitted ONLY the fenced verdict block, so
	// StripJSONVerdict left an empty body and the poster substituted
	// "automated review — no summary produced" — discarding a summary the model
	// had written. That placeholder is itself a body/review disagreement.
	Context("a review body that is only the fenced verdict block", func() {
		const summaryText = "Clean fix: start() now threads its caller's context into loadConfig instead of context.Background(), so a cancelled run aborts the config load. No other changes."

		BeforeEach(func() {
			reviewText = "```json\n" +
				`{"verdict":"approve","summary":"` + summaryText + `","comments":[],"concerns_addressed":[]}` +
				"\n```\n"
		})

		It("strips the body to empty", func() {
			Expect(strings.TrimSpace(pkg.StripJSONVerdict(reviewText))).To(BeEmpty())
		})

		It("returns the summary the model wrote, not a placeholder", func() {
			Expect(pkg.VerdictSummary(reviewText)).To(Equal(summaryText))
		})
	})

	Context("a chunked review", func() {
		BeforeEach(func() {
			reviewText = "### Chunk 1/2\n\nProse.\n\n```json\n" +
				`{"verdict":"approve","summary":"chunk one summary"}` +
				"\n```\n\n### Chunk 2/2\n\nProse.\n\n```json\n" +
				`{"verdict":"approve","summary":"aggregate summary"}` +
				"\n```\n"
		})

		It("returns the last (aggregate) block's summary", func() {
			Expect(pkg.VerdictSummary(reviewText)).To(Equal("aggregate summary"))
		})
	})

	Context("a body with prose and a verdict block", func() {
		BeforeEach(func() {
			reviewText = "## Review\n\nSome prose.\n\n```json\n" +
				`{"verdict":"approve","summary":"block summary"}` +
				"\n```\n"
		})

		It("still returns the block summary — the caller decides when to use it", func() {
			Expect(pkg.VerdictSummary(reviewText)).To(Equal("block summary"))
			Expect(pkg.StripJSONVerdict(reviewText)).To(ContainSubstring("Some prose."))
		})
	})

	DescribeTable(
		"returns empty when there is nothing to read",
		func(input string) {
			Expect(pkg.VerdictSummary(input)).To(BeEmpty())
		},
		Entry("no verdict block", "## Review\n\nJust prose, no JSON."),
		Entry("empty input", ""),
		Entry("block without a summary field", "```json\n{\"verdict\":\"approve\"}\n```"),
		Entry(
			"block with an empty summary",
			"```json\n{\"verdict\":\"approve\",\"summary\":\"\"}\n```",
		),
	)

	// ParseVerdict recovers a malformed block only when it is fenced; a bare one
	// fail-closes to request-changes. VerdictSummary must mirror that, or the
	// body would carry a summary under an event the parser refused.
	Context("a bare (unfenced) block that is not valid JSON", func() {
		BeforeEach(func() {
			reviewText = `{"verdict": "approve", "summary": "a"b"}`
		})

		It("is located but fail-closes in ParseVerdict", func() {
			result := pkg.ParseVerdict(reviewText)
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
			Expect(result.Reason).To(HavePrefix("malformed JSON:"))
		})

		It("is not recovered by VerdictSummary", func() {
			Expect(pkg.VerdictSummary(reviewText)).To(BeEmpty())
		})
	})

	// The summary must never outlive a verdict the parser refused: a fenced
	// block with an unquoted verdict value fail-closes in ParseVerdict, so
	// recovering its summary would post the model's text beneath a
	// REQUEST_CHANGES event it never asked for.
	Context("a fenced block whose verdict value is not a quoted string", func() {
		BeforeEach(func() {
			reviewText = "```json\n" +
				`{"verdict": approve, "summary": "should not be posted"}` +
				"\n```"
		})

		It("fail-closes in ParseVerdict", func() {
			Expect(pkg.ParseVerdict(reviewText).Verdict).To(Equal(pkg.VerdictRequestChanges))
		})

		It("does not recover the summary", func() {
			Expect(pkg.VerdictSummary(reviewText)).To(BeEmpty())
		})
	})

	// ParseVerdict recovers a verdict from a fenced block that is not valid
	// JSON (recoverFencedVerdict), so the summary must survive that same input —
	// otherwise a block whose verdict parses fine still posts the placeholder.
	Context("a fenced block that is not valid JSON", func() {
		BeforeEach(func() {
			reviewText = "```json\n{\"verdict\": \"approve\", \"summary\": \"recovered text\"\n```"
		})

		It("is accepted by ParseVerdict", func() {
			Expect(pkg.ParseVerdict(reviewText).Verdict).To(Equal(pkg.VerdictApprove))
		})

		It("recovers the summary too, rather than dropping it", func() {
			Expect(pkg.VerdictSummary(reviewText)).To(Equal("recovered text"))
		})
	})

	Context("a fenced block that is not valid JSON, with escaped quotes in the summary", func() {
		BeforeEach(func() {
			// Invalid JSON overall (the unterminated comments array), but the
			// summary itself is a well-formed JSON string with escaped quotes.
			reviewText = "```json\n" +
				`{"verdict":"approve","summary":"The \"fix\" is correct","comments":[}` +
				"\n```"
		})

		It("decodes the escapes instead of truncating at the first quote", func() {
			Expect(pkg.VerdictSummary(reviewText)).To(Equal(`The "fix" is correct`))
		})
	})

	Context("a bare (unfenced) JSON-only body", func() {
		BeforeEach(func() {
			reviewText = `{"verdict":"approve","summary":"bare summary"}`
		})

		It("strips to empty and falls back to the summary", func() {
			Expect(strings.TrimSpace(pkg.StripJSONVerdict(reviewText))).To(BeEmpty())
			Expect(pkg.ReviewBody(reviewText)).To(Equal("bare summary"))
		})
	})
})

var _ = Describe("ReviewBody", func() {
	DescribeTable("returns a body a reader can read",
		func(input, expected string) {
			Expect(pkg.ReviewBody(input)).To(Equal(expected))
		},
		Entry(
			"prose survives the strip",
			"## Review\n\nSome prose.\n\n```json\n{\"verdict\":\"approve\",\"summary\":\"s\"}\n```\n",
			"## Review\n\nSome prose.",
		),
		Entry("JSON-only body falls back to its summary",
			"```json\n{\"verdict\":\"approve\",\"summary\":\"the model's own summary\"}\n```\n",
			"the model's own summary"),
		Entry("JSON-only body with no summary stays empty rather than inventing one",
			"```json\n{\"verdict\":\"approve\"}\n```\n",
			""),
	)
})

var _ = Describe("ParseVerdict normalisation regression (spec-030)", func() {
	DescribeTable("verdict spelling and case normalisation",
		func(reviewText string, expectedVerdict pkg.Verdict) {
			result := pkg.ParseVerdict(reviewText)
			Expect(result.Verdict).To(Equal(expectedVerdict))
		},
		// (a) canonical hyphen spelling — the parser must always accept this
		Entry("request-changes hyphen → RequestChanges",
			`{"verdict": "request-changes"}`,
			pkg.VerdictRequestChanges,
		),
		// (b) underscore drift — THE SMOKING-GUN ROW.
		// Pre-fix `ParseVerdict` returned VerdictApprove for this input via the
		// deleted heuristic; this row must remain RequestChanges to prove the
		// normalisation switch is load-bearing. The spec-030 revert-test AC
		// requires this row to fail when `strings.ReplaceAll(_, "_", "-")` is
		// removed from the parser.
		Entry("request_changes underscore → RequestChanges (normalised)",
			`{"verdict": "request_changes"}`,
			pkg.VerdictRequestChanges,
		),
		// (c) ALL-CAPS hyphen
		Entry("REQUEST-CHANGES caps → RequestChanges (normalised)",
			`{"verdict": "REQUEST-CHANGES"}`,
			pkg.VerdictRequestChanges,
		),
		// (d) approve canonical
		Entry("approve → Approve",
			`{"verdict": "approve"}`,
			pkg.VerdictApprove,
		),
		// (e) mixed-case approve
		Entry("Approve mixed-case → Approve (normalised)",
			`{"verdict": "Approve"}`,
			pkg.VerdictApprove,
		),
		// (f) unknown value fails closed
		Entry("comment → RequestChanges (fail-closed, unknown verdict)",
			`{"verdict": "comment"}`,
			pkg.VerdictRequestChanges,
		),
		// (g) empty review text fails closed
		Entry("empty text → RequestChanges (fail-closed)",
			``,
			pkg.VerdictRequestChanges,
		),
		// (h) malformed JSON containing quoted "verdict" key — block found, unmarshal fails
		Entry("malformed JSON → RequestChanges (fail-closed)",
			`{"verdict": invalid}`,
			pkg.VerdictRequestChanges,
		),
		// (i) multi-line fenced block with ≥3 prose lines before and ≥1 after
		Entry(
			"multi-line fenced request-changes amid prose → RequestChanges",
			"Line of prose 1.\nLine of prose 2.\nLine of prose 3.\n\n```json\n{\n  \"verdict\": \"request-changes\",\n  \"summary\": \"Issues found.\",\n  \"comments\": []\n}\n```\nTrailing prose line.",
			pkg.VerdictRequestChanges,
		),
	)

	It("unknown verdict reason names the raw value", func() {
		result := pkg.ParseVerdict(`{"verdict": "block"}`)
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(result.Reason).To(Equal("unknown verdict: block"))
	})

	It("request_changes reason is preserved from JSON reason field", func() {
		result := pkg.ParseVerdict(`{"verdict": "request_changes", "reason": "security issue"}`)
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(result.Reason).To(Equal("security issue"))
	})

	It("no verdict block reason is 'no verdict block'", func() {
		result := pkg.ParseVerdict("### Must Fix\n\n- critical item")
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(result.Reason).To(Equal("no verdict block"))
	})
})

var _ = Describe("ParseVerdict end-anchored window regression (long verdict block)", func() {
	// Reproduces the maintainer PR #29 inversion (2026-05-30): the reviewer
	// emitted a well-formed `"verdict": "approve"` block, but the block's long
	// `comments` array pushed the "verdict" KEY ~60 lines above the closing
	// brace — outside the old key-line 50-line window — so the parser returned
	// "no verdict block", fail-closed to request-changes, and posted a false
	// CHANGES_REQUESTED on a PR the bot actually approved. The fix anchors the
	// window on the block's CLOSING brace (which sits at the end of the review
	// per the execution output-format spec) and walks back unbounded to the
	// matching open brace, so block length no longer drops a present verdict.

	// longVerdictBlock builds a fenced JSON verdict block whose "verdict" key is
	// pushed `commentCount * 5` lines above the closing brace by the comments
	// array, with ≥3 prose lines before the fence.
	longVerdictBlock := func(verdict string, commentCount int) string {
		var b strings.Builder
		b.WriteString(
			"# Code Review\n\nNarrative line 1.\nNarrative line 2.\nNarrative line 3.\n\n",
		)
		b.WriteString("```json\n{\n")
		b.WriteString(`  "verdict": "` + verdict + `",` + "\n")
		b.WriteString(`  "summary": "Well-scoped change; only nits flagged.",` + "\n")
		b.WriteString(`  "comments": [` + "\n")
		for i := 0; i < commentCount; i++ {
			comma := ","
			if i == commentCount-1 {
				comma = ""
			}
			b.WriteString("    {\n")
			b.WriteString(`      "file": "prompts/completed/stale.md",` + "\n")
			b.WriteString(`      "line": 1,` + "\n")
			b.WriteString(`      "severity": "nit",` + "\n")
			b.WriteString(`      "message": "stale prompt file"` + "\n")
			b.WriteString("    }" + comma + "\n")
		}
		b.WriteString("  ]\n}\n```")
		return b.String()
	}

	It("parses approve when the verdict key is >50 lines above a near-end closing brace", func() {
		reviewText := longVerdictBlock("approve", 20)
		// Sanity: the verdict key really is outside the trailing 50-line window.
		lines := strings.Split(reviewText, "\n")
		verdictLine := -1
		for i, l := range lines {
			if strings.Contains(l, `"verdict"`) {
				verdictLine = i
			}
		}
		Expect(verdictLine).NotTo(Equal(-1), "generated block must contain a verdict key")
		Expect(len(lines)-verdictLine).To(BeNumerically(">", 50),
			"test setup must push the verdict key beyond the 50-line window")

		result := pkg.ParseVerdict(reviewText)
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		// Pin the exact regression symptom: the block was FOUND and parsed, not
		// silently dropped and fail-closed (which would read "no verdict block").
		Expect(result.Reason).NotTo(Equal("no verdict block"))
	})

	It("finds a verdict block sitting just past the 50-line boundary", func() {
		// commentCount=11 → ~55 comment lines: closing brace within the window,
		// verdict key just outside the old 50-line cap.
		result := pkg.ParseVerdict(longVerdictBlock("approve", 11))
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		Expect(result.Reason).NotTo(Equal("no verdict block"))
	})

	It("still maps a long request-changes block to RequestChanges via the block", func() {
		result := pkg.ParseVerdict(longVerdictBlock("request-changes", 20))
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		// Must come from the parsed block, not from fail-closed "no verdict block".
		Expect(result.Reason).NotTo(Equal("no verdict block"))
	})

	It("matches the outer brace despite balanced braces inside a string value", func() {
		reviewText := "# Review\n\nProse line.\n\n```json\n{\n" +
			`  "verdict": "approve",` + "\n" +
			`  "reason": "use map[string]struct{}{} and {} sparingly"` + "\n" +
			"}\n```"
		result := pkg.ParseVerdict(reviewText)
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
	})
})

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
		Entry(
			"approve + critical blocking:false (pre-existing debt) → approve",
			`{"verdict":"approve","reason":"pre-existing debt","comments":[{"file":"main.go","line":5,"severity":"critical","blocking":false,"message":"debt on an untouched line"}]}`,
			pkg.VerdictApprove,
			"pre-existing debt",
		),
		// (b) comment blocking:true while the model verdict is approve → request-changes
		Entry(
			"approve + blocking:true → request-changes (ReasonBlockingFindingPresent)",
			`{"verdict":"approve","reason":"looks good","comments":[{"file":"main.go","line":9,"severity":"nit","blocking":true,"blocking_reason":"isHealthy() is inverted","message":"real defect"}]}`,
			pkg.VerdictRequestChanges,
			pkg.ReasonBlockingFindingPresent,
		),
		// (c) blocking field absent → severity fallback: critical/major block, nit/minor do not
		Entry(
			"approve + absent blocking critical → request-changes (severity fallback)",
			`{"verdict":"approve","reason":"ok","comments":[{"file":"main.go","line":5,"severity":"critical","message":"..."}]}`,
			pkg.VerdictRequestChanges,
			pkg.ReasonBlockingFindingPresent,
		),
		Entry(
			"approve + absent blocking major → request-changes (severity fallback)",
			`{"verdict":"approve","reason":"ok","comments":[{"file":"main.go","line":5,"severity":"major","message":"..."}]}`,
			pkg.VerdictRequestChanges,
			pkg.ReasonBlockingFindingPresent,
		),
		Entry(
			"approve + absent blocking nit → approve",
			`{"verdict":"approve","reason":"ok","comments":[{"file":"main.go","line":5,"severity":"nit","message":"..."}]}`,
			pkg.VerdictApprove,
			"ok",
		),
		Entry(
			"approve + absent blocking minor → approve",
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

var _ = Describe("IsNoReviewReason", func() {
	// The no-review gate suppresses the post only when the run produced no review
	// at all. Every other fail-closed reason is a legitimate code-side demotion of
	// a review the model DID write and must keep posting.
	//
	// `no verdict block` is the subtle one: ParseVerdict returns it BOTH for a
	// tool-error marker and for a real review whose model omitted the JSON block.
	// Only the former may be suppressed — suppressing the latter hides real
	// blocking findings, which is worse than the bug the gate fixes.
	DescribeTable("classifies a fail-closed reason against its body",
		func(reason, body string, expected bool) {
			Expect(pkg.IsNoReviewReason(reason, body)).To(Equal(expected))
		},
		Entry("empty review text → true", pkg.ReasonEmptyReviewText, "", true),
		Entry("no verdict block + tool-error marker body → true",
			pkg.ReasonNoVerdictBlock, "(tool 'WebFetch' unavailable)", true),
		Entry("no verdict block + whitespace-only body → true",
			pkg.ReasonNoVerdictBlock, "   \n\t\n", true),
		Entry("no verdict block + a Must Fix heading → false (regression guard)",
			pkg.ReasonNoVerdictBlock,
			"### Must Fix (Critical)\n\n- Security issue in authentication", false),
		Entry("no verdict block + a Blocking heading → false",
			pkg.ReasonNoVerdictBlock, "## Blocking\n\n- SQL injection vulnerability", false),
		Entry("'#' without a following space is not a heading → true",
			pkg.ReasonNoVerdictBlock, "#hashtag is not a heading", true),
		Entry("funnel did not run → false", pkg.ReasonFunnelDidNotRun, "## Review\n\nprose", false),
		Entry("concerns not verified → false",
			pkg.ReasonConcernsNotVerified, "## Review\n\nprose", false),
		Entry("blocking finding present → false",
			pkg.ReasonBlockingFindingPresent, "## Review\n\nprose", false),
		Entry("malformed JSON → false",
			"malformed JSON: unexpected end of JSON input", "## Review\n\nprose", false),
		Entry("unknown verdict → false", "unknown verdict: comment", "## Review\n\nprose", false),
		Entry("model-authored reason → false", "looks good to me", "## Review\n\nprose", false),
		Entry("empty reason → false", "", "", false),
	)

	It("agrees with the reasons ParseVerdict actually emits", func() {
		// Guards the coupling: if ParseVerdict's literals drift from the exported
		// constants, the gate silently stops firing.
		Expect(pkg.IsNoReviewReason(pkg.ParseVerdict("").Reason, "")).To(BeTrue())

		const crashBody = "(tool 'WebFetch' unavailable)"
		crash := pkg.ParseVerdict(crashBody)
		Expect(crash.Reason).To(Equal(pkg.ReasonNoVerdictBlock))
		Expect(pkg.IsNoReviewReason(crash.Reason, crashBody)).To(BeTrue())

		// The regression the gate must NOT cause: a real review whose model
		// omitted the JSON verdict block parses to the same reason, and must
		// still post its fail-closed request-changes.
		const mustFixBody = "### Must Fix (Critical)\n\n- Security issue in authentication"
		mustFixResult := pkg.ParseVerdict(mustFixBody)
		Expect(mustFixResult.Reason).To(Equal(pkg.ReasonNoVerdictBlock))
		Expect(pkg.IsNoReviewReason(mustFixResult.Reason, mustFixBody)).To(BeFalse())

		approve := `{"verdict":"approve"}`
		Expect(pkg.IsNoReviewReason(pkg.ParseVerdict(approve).Reason, approve)).To(BeFalse())
	})
})
