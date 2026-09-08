// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HasBlockingFinding", func() {
	fence := func(body string) string {
		return "# Code Review\n\n```json\n" + body + "\n```\n"
	}

	DescribeTable("detects blocking comments in the verdict block",
		func(reviewBody string, expected bool) {
			Expect(pkg.HasBlockingFinding(reviewBody)).To(Equal(expected))
		},
		// The `blocking` field is authoritative when present, regardless of severity.
		Entry(
			"blocking true wins over critical severity",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"severity":"critical","blocking":true,"blocking_reason":"real defect","message":"boom"}]}`,
			),
			true,
		),
		Entry(
			"blocking false wins over critical severity (pre-existing debt)",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"severity":"critical","blocking":false,"blocking_reason":"accepted debt","message":"known issue"}]}`,
			),
			false,
		),
		Entry(
			"blocking false wins over major severity",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"severity":"major","blocking":false,"message":"later"}]}`,
			),
			false,
		),
		// An explicit blocking: true with an empty blocking_reason still blocks —
		// the reason is a model-quality requirement, not a gate condition.
		Entry(
			"blocking true with empty blocking_reason still blocks",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"severity":"nit","blocking":true,"blocking_reason":"","message":"real defect"}]}`,
			),
			true,
		),
		// Severity fallback when `blocking` is absent: critical/major block.
		Entry(
			"absent blocking + critical severity blocks (fallback)",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"severity":"critical","message":"boom"}]}`,
			),
			true,
		),
		Entry(
			"absent blocking + major severity blocks (fallback)",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"severity":"major","message":"later"}]}`,
			),
			true,
		),
		Entry(
			"absent blocking + nit severity does not block (fallback)",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"severity":"nit","message":"style"}]}`,
			),
			false,
		),
		Entry(
			"absent blocking + minor severity does not block (fallback)",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"severity":"minor","message":"cosmetic"}]}`,
			),
			false,
		),
		Entry(
			"absent blocking + unrecognised severity does not block (fallback)",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"severity":"info","message":"note"}]}`,
			),
			false,
		),
		// A comment carrying neither `blocking` nor `severity` is skipped.
		Entry(
			"comment with neither blocking nor severity is skipped",
			fence(
				`{"verdict":"approve","comments":[{"file":"main.go","line":9,"message":"note only"}]}`,
			),
			false,
		),
		// Unparseable comments are skipped, never over-trigger.
		Entry("unparseable comment (JSON number) is skipped",
			fence(`{"verdict":"approve","comments":[42]}`),
			false),
		Entry("malformed comment object is skipped",
			fence(`{"verdict":"approve","comments":[{"file":"main.go","line":5,"severity":}]}`),
			false),
		// A `comments` list that is not a list at all → unmarshal fails → permissive false.
		Entry("comments not a list passes (no over-trigger)",
			fence(`{"verdict":"approve","comments":"oops"}`),
			false),
		// Missing comments key → no over-trigger.
		Entry("missing comments key",
			fence(`{"verdict":"approve","reason":"clean"}`),
			false),
		// Prose without a verdict block → nothing to examine.
		Entry("prose without a verdict block",
			"LGTM. All checks pass.", false),
		// Malformed verdict JSON → unmarshal fails → no over-trigger.
		Entry("malformed verdict block",
			fence(`{"verdict":"approve","comments":[{"severity":"critical"`),
			false),
		// Ordering: one blocking comment among non-blocking comments must block.
		Entry(
			"one blocking true among non-blocking comments blocks",
			fence(
				`{"verdict":"approve","comments":[{"severity":"nit","blocking":false,"message":"a"},{"severity":"critical","blocking":true,"message":"b"}]}`,
			),
			true,
		),
		// Ordering: the early-return on blocking:false must not stop the scan.
		Entry(
			"blocking false BEFORE blocking true still blocks (ordering)",
			fence(
				`{"verdict":"approve","comments":[{"severity":"nit","blocking":false,"message":"a"},{"severity":"nit","blocking":true,"message":"b"}]}`,
			),
			true,
		),
		// Ordering: the severity fallback must not be masked by an earlier blocking:false.
		Entry(
			"blocking false BEFORE critical severity missing blocking still blocks (ordering)",
			fence(
				`{"verdict":"approve","comments":[{"severity":"nit","blocking":false,"message":"a"},{"severity":"critical","message":"b"}]}`,
			),
			true,
		),
	)

	Describe("ApplyBlockingGate composition", func() {
		blockingBody := fence(
			`{"verdict":"approve","comments":[{"severity":"nit","blocking":true,"blocking_reason":"isHealthy() is inverted","message":"real defect"}]}`,
		)

		It(
			"converts an approve with a blocking comment to request-changes with ReasonBlockingFindingPresent",
			func() {
				result := pkg.ApplyBlockingGate(
					pkg.Result{Verdict: pkg.VerdictApprove, Reason: "looks ok"},
					blockingBody,
				)
				Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
				Expect(result.Reason).To(Equal(pkg.ReasonBlockingFindingPresent))
			},
		)

		It(
			"passes through an approve with only blocking:false comments (reason preserved)",
			func() {
				result := pkg.ApplyBlockingGate(
					pkg.Result{Verdict: pkg.VerdictApprove, Reason: "looks ok"},
					fence(
						`{"verdict":"approve","comments":[{"severity":"nit","blocking":false,"message":"style"}]}`,
					),
				)
				Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
				Expect(result.Reason).To(Equal("looks ok"))
			},
		)

		// Chain-precedence (funnel first): a verdict the funnel gate already demoted
		// must pass through unchanged — the blocking gate never rewrites a
		// fail-closed verdict, so the earlier, coarser gate keeps its reason.
		It("does not override an already-fail-closed verdict (funnel reason preserved)", func() {
			result := pkg.ApplyBlockingGate(
				pkg.Result{Verdict: pkg.VerdictRequestChanges, Reason: pkg.ReasonFunnelDidNotRun},
				blockingBody,
			)
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
			Expect(result.Reason).To(Equal(pkg.ReasonFunnelDidNotRun))
		})

		// Chain-precedence (concerns): same contract for the concerns gate's reason.
		It("does not override an already-fail-closed verdict (concerns reason preserved)", func() {
			result := pkg.ApplyBlockingGate(
				pkg.Result{
					Verdict: pkg.VerdictRequestChanges,
					Reason:  pkg.ReasonConcernsNotVerified,
				},
				blockingBody,
			)
			Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
			Expect(result.Reason).To(Equal(pkg.ReasonConcernsNotVerified))
		})
	})
})
