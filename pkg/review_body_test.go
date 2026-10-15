// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	claudelib "github.com/bborbe/agent/claude"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("reviewBodyFromRun", func() {
	const verdictBlock = "```json\n{\"verdict\": \"approve\", \"summary\": \"fine\"}\n```"

	DescribeTable("Result/Partial selection",
		func(result *claudelib.ClaudeResult, expect string) {
			Expect(pkg.ReviewBodyFromRunForTest(result)).To(Equal(expect))
		},
		Entry(
			"final result carries the verdict block — used verbatim",
			&claudelib.ClaudeResult{
				Result:  "The review.\n\n" + verdictBlock,
				Partial: "older streamed draft",
			},
			"The review.\n\n"+verdictBlock,
		),
		// The observed failure: the model emitted the whole review mid-run and
		// then ended on a follow-up turn, so Result held only the closing remark.
		// Without the fallback ParseVerdict fail-closes with "no verdict block"
		// and the review is skipped rather than posted.
		Entry(
			"final result has no block — falls back to the streamed partial",
			&claudelib.ClaudeResult{
				Result:  "The background test run completed successfully — all tests pass.",
				Partial: "The review.\n\n" + verdictBlock + "\n\nThe background test run completed successfully.",
			},
			"The review.\n\n"+verdictBlock+"\n\nThe background test run completed successfully.",
		),
		Entry(
			"neither carries a block — returns the final result unchanged",
			&claudelib.ClaudeResult{
				Result:  "no block here",
				Partial: "still no block",
			},
			"no block here",
		),
		Entry(
			"result has no block and partial is empty — returns the final result",
			&claudelib.ClaudeResult{Result: "no block here"},
			"no block here",
		),
		Entry(
			"nil result yields empty",
			nil,
			"",
		),
	)
})
