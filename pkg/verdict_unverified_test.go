// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"os"
	"strings"
	"time"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// toolchainConcernJSON is the verbatim quickbooks#4 toolchain wording as an
// object with `disposition: "not-verified"` — the probe body whose benign prose
// matched the old prose whitelist and wrongly passed the approve. The prose is
// now inert: the disposition is the admission and only the elapsed/budget ratio
// decides the demotion.
const toolchainConcernJSON = `{"verdict":"approve","concerns_addressed":[{"concern":"correctness: go.mod go directive 1.27.0 dep compatibility","detail":"not verified - module files internally consistent (tidy ran, no downgrades, all hashes present) but transitive go-directive compatibility requires a Go 1.27 toolchain not available in the review sandbox; repo CI precommit (go mod tidy/verify + build) is the gate","disposition":"not-verified"}]}`

var _ = Describe("HasUnverifiedConcerns", func() {
	fence := func(body string) string {
		return "# Code Review\n\n```json\n" + body + "\n```\n"
	}

	DescribeTable("detects ## Plan concerns flagged as not verified",
		func(reviewBody string, expected bool) {
			Expect(pkg.HasUnverifiedConcerns(reviewBody)).To(Equal(expected))
		},
		// flagged lowercase `not verified` → the model stopped at the time budget.
		// spec-002 regression (the `:26` fixture), object shape now: the FULL
		// prose "security: rate-limit not verified" is preserved in the `concern`
		// field with `disposition: "not-verified"` — prose reading "not verified"
		// AND the not-verified disposition both hold, so the row must still demote.
		Entry(
			"flagged lowercase not verified (spec-002 :26 object shape)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit not verified","disposition":"not-verified"}]}`,
			),
			true,
		),
		// case-insensitive match on the canonical wording.
		Entry(
			"flagged uppercase NOT VERIFIED",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit NOT VERIFIED"]}`,
			),
			true,
		),
		// `unverified` variant is matched too (robust to model phrasing).
		Entry(
			"unverified variant",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit unverified"]}`,
			),
			true,
		),
		// BENIGN regression (2026-08-23 bborbe/math#18, reviewBody_len=1079): a
		// "not verified" concern that self-describes as not-applicable (config/
		// docs-only change, no code to verify) must NOT fail-close an approve.
		// The old prose whitelist is gone: the concern is now an object whose
		// `disposition` field is authoritative — `not-an-issue` passes no matter
		// what the prose says. The prose is kept verbatim in `concern` to prove
		// it is inert (it contains `not verified` and would demote under the
		// legacy bare-string rule).
		Entry(
			"benign not verified object (code logic not applicable — config-only)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"security: not verified (code logic not applicable — this is a config/changelog-only diff with no Go code changes)","disposition":"not-an-issue"}]}`,
			),
			false,
		),
		// REGRESSION 2026-08-24 (bborbe/nuke#68): a benign "not verified" concern
		// that explains the verification gap as contextual (source not in this
		// repo → could not be cross-checked) escaped the old benign-phrase
		// whitelist and demoted a clean approve → false CHANGES_REQUESTED on
		// v0.6.2. Under the new encoding the `disposition` field is the
		// mechanism: `not-an-issue` passes; the verbatim prose (which still
		// contains `not verified`) is never inspected.
		Entry(
			"benign not verified object (source not in repo — could not be cross-checked)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"correctness: metric name agent_controller_results_written_total{result=\"not_found\"} — searched entire repo (go.mod, all source, all alerts) and the metric only appears in this new alert and the CHANGELOG. The agent-task-controller source is not in this repository, so the metric name and label value could not be cross-checked against the actual controller code. Not verified.","disposition":"not-an-issue"}]}`,
			),
			false,
		),
		// POSITIVE CONTROL 2026-08-24 (bborbe/nuke#73): a "not verified" concern
		// that IS a MUST-tier blocker (metric existence unconfirmed; without it
		// the alerts will never fire; must verify before deploying) must still
		// fail-close. Object shape now: the full verbatim text is the `concern`
		// with `disposition: "not-verified"` — the MUST-tier positive control
		// must still demote.
		Entry(
			"MUST-tier unverified blocker (alerts will never fire)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"correctness: expression references github_build_watcher_rate_limit_remaining — not verified: build-watcher source not present in this monorepo, metric existence cannot be confirmed; without it the alerts will never fire. Must verify metric is exported before deploying.","disposition":"not-verified"}]}`,
			),
			true,
		),
		// REGRESSION 2026-09-01 (Seibert-Data/quickbooks#4, 09:46 fail-close): a
		// benign `not-verified` concern whose gap is toolchain-limited — the model
		// examined the module files (internally consistent, tidy ran, no
		// downgrades, all hashes present) but could not run a Go 1.27 toolchain in
		// the sandbox, and names CI/precommit as the gate. The prose layer is gone
		// (migrated, requirement 7a): the `disposition` is the admission and the
		// toolchain wording is not inspected, so the admission reader returns
		// true. Whether this approve demotes is now decided by the elapsed/budget
		// ratio in DemotesUnverifiedConcerns — on a run that finished well inside
		// its budget the `not-verified` is a mislabel and the approve stands; on a
		// budget-heavy run it fail-closes (see the budget-keyed table below).
		Entry(
			"toolchain-limited not-verified is an admission (Go 1.27 toolchain unavailable, CI is the gate)",
			fence(toolchainConcernJSON),
			true,
		),
		// The octopus fleet posts the LEGACY flat-string shape (the model wrote
		// the whole explanation in one string, no disposition object). Same
		// quickbooks#4 09:46 content verbatim: the flag wording is the admission
		// (legacy strings.Contains path, spec 004 Desired Behavior 5) and the
		// explanation is not inspected, so the admission is unconditional —
		// migrated from the old tier-keyed pass to true (requirement 7a).
		Entry(
			"toolchain-limited not-verified — legacy flat-string shape (octopus posted form)",
			fence(
				`{"verdict":"approve","concerns_addressed":["correctness: go.mod go directive 1.27.0 dep compatibility: not verified - module files internally consistent (tidy ran, no downgrades, all hashes present) but transitive go-directive compatibility requires a Go 1.27 toolchain not available in the review sandbox; repo CI precommit (go mod tidy/verify + build) is the gate"]}`,
			),
			true,
		),
		// No flagged concern → the gate must not over-trigger.
		Entry(
			"no flags",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit addressed in handler.go:45"]}`,
			),
			false,
		),
		// Empty concerns list → no over-trigger.
		Entry("empty concerns_addressed",
			fence(`{"verdict":"approve","concerns_addressed":[]}`), false),
		// Missing concerns_addressed key → no over-trigger.
		Entry("no concerns_addressed key",
			fence(`{"verdict":"approve","reason":"clean"}`), false),
		// Prose without a verdict block → nothing to examine.
		Entry("prose without a verdict block",
			"LGTM. All checks pass.", false),
		// Malformed verdict JSON → unmarshal fails → no over-trigger.
		Entry("malformed JSON",
			fence(`{"verdict":"approve","concerns_addressed":["unterminated`), false),

		// Object entries: the `disposition` field is authoritative; prose is inert.
		Entry(
			"object disposition addressed passes",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"addressed"}]}`,
			),
			false,
		),
		Entry(
			"object disposition not-an-issue passes",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not-an-issue"}]}`,
			),
			false,
		),
		Entry(
			"object disposition not-verified demotes",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not-verified"}]}`,
			),
			true,
		),
		Entry(
			"object disposition absent demotes (fail-safe)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit"}]}`,
			),
			true,
		),
		Entry(
			"object unrecognised disposition demotes (fail-safe)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"inconclusive"}]}`,
			),
			true,
		),
		// Spec constraint: one valid not-verified object is enough; the
		// uninterpretable element is skipped, not a parse failure.
		Entry(
			"mixed list demotes on the valid not-verified entry",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not-verified"},42]}`,
			),
			true,
		),
		// All-uninterpretable entries (number + nested array) are skipped → no demotion.
		Entry(
			"all-uninterpretable list passes (entries skipped)",
			fence(
				`{"verdict":"approve","concerns_addressed":[42,["correctness: nested"]]}`,
			),
			false,
		),
		// concerns_addressed that is not a list at all → unmarshal fails → permissive false.
		Entry("concerns_addressed not a list passes (no over-trigger)",
			fence(`{"verdict":"approve","concerns_addressed":"oops"}`), false),
		// The pre-v0.6.6 schema taught the model the space form `not an issue`;
		// the hyphenated enum is matched exactly and the space form is NOT
		// normalized → fail-safe demote (locks this decision).
		Entry(
			"object retired space-form disposition demotes (fail-safe)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not an issue"}]}`,
			),
			true,
		),

		// The three prose-inert wordings from the old tier-keyed table, kept at
		// the admission level: the disposition alone decides, the pairs differ
		// only in the enum value, and the not-verified variants are admissions
		// regardless of how benign the wording is (the elapsed/budget ratio in
		// DemotesUnverifiedConcerns — not the prose — decides the demotion).
		Entry(
			"wording 1 not-an-issue passes (prose contains not verified)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested","disposition":"not-an-issue"}]}`,
			),
			false,
		),
		Entry(
			"wording 1 not-verified is an admission (bare admission)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested","disposition":"not-verified"}]}`,
			),
			true,
		),
		Entry(
			"wording 2 not-an-issue passes (prose contains not verified)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"correctness: could not be cross-checked against the actual controller code. Not verified.","disposition":"not-an-issue"}]}`,
			),
			false,
		),
		// MIGRATED (requirement 7a): the tier-keyed gate passed this wording-2
		// `not-verified` row on its benign cross-check explanation; with the
		// prose layer gone the disposition alone is the admission, so the
		// expectation flips to true — whether an approve demotes is decided by
		// the elapsed/budget ratio, never by the wording.
		Entry(
			"wording 2 not-verified is an admission (benign cross-check gap explained)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"correctness: could not be cross-checked against the actual controller code. Not verified.","disposition":"not-verified"}]}`,
			),
			true,
		),
		Entry(
			"wording 3 not-an-issue passes (prose carries no flag)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"performance: I inspected the vendored copy and the mutex is uncontended in this call graph","disposition":"not-an-issue"}]}`,
			),
			false,
		),
		Entry(
			"wording 3 not-verified is an admission (bare admission)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"performance: I inspected the vendored copy and the mutex is uncontended in this call graph","disposition":"not-verified"}]}`,
			),
			true,
		),
	)

	// The nuke#216 fixture cannot be an Entry of the table above (its closure
	// takes the body as a string and cannot read a file), so it gets its own It
	// with the os.ReadFile-in-leaf shape the incident Describe uses — a
	// missing/drifted fixture fails the row, not the suite construction. Four
	// `not-verified` dispositions are four admissions.
	It("flags the nuke#216 fixture as four unexamined admissions", func() {
		body, err := os.ReadFile("testdata/review_bborbe_nuke_216_run1.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(pkg.HasUnverifiedConcerns(string(body))).To(BeTrue())
	})

	// The verbatim toolchain wording as an object with `disposition:
	// "not-verified"` (requirement 7b) — already asserted by the migrated row
	// above; this row keeps the admission lock explicit for the probe body
	// shape that carries a `reason` field.
	DescribeTable("the toolchain wording object is an admission",
		func(body string) {
			Expect(pkg.HasUnverifiedConcerns(body)).To(BeTrue())
		},
		Entry(
			"toolchain object with disposition not-verified (probe shape)",
			fence(
				`{"verdict":"approve","reason":"clean","concerns_addressed":[{"concern":"correctness: go.mod go directive 1.27.0 dep compatibility","detail":"not verified - module files internally consistent (tidy ran, no downgrades, all hashes present) but transitive go-directive compatibility requires a Go 1.27 toolchain not available in the review sandbox; repo CI precommit (go mod tidy/verify + build) is the gate","disposition":"not-verified"}]}`,
			),
		),
	)

	// The demotion decision, budget-keyed (spec 004's named follow-up lever):
	// an `approve` carrying an unexamined concern fail-closes ONLY when the run
	// consumed at least 0.8 of its soft budget. The rows are (body supplier,
	// elapsed, budget, expected) with a budget of 30 minutes throughout so the
	// ratios are explicit; the body supplier reads the fixture in-leaf. The
	// prose is inert in both directions — the pairs below differ only in the
	// elapsed value.
	DescribeTable(
		"budget-keyed: an unexamined concern demotes only when the run consumed >=0.8 of its soft budget",
		func(reviewBody func() string, elapsed, budget time.Duration, expected bool) {
			Expect(pkg.DemotesUnverifiedConcerns(reviewBody(), elapsed, budget)).To(Equal(expected))
		},
		// Incident provenance (2026-09-10 bborbe/nuke#216, review_id 5172635280):
		// the run-1 body carries four `not-verified` dispositions with benign
		// operational wording (TeamVault revocation). A short run's `not-verified`
		// is provably a mislabel — the model had budget left and examined the
		// concern — so the approve stands; on a budget-heavy run the disposition
		// is credible and the approve fail-closes.
		Entry("nuke#216 fixture, elapsed 2m of 30m (ratio ~0.07) — approve stands (the incident)",
			func() string {
				body, err := os.ReadFile("testdata/review_bborbe_nuke_216_run1.md")
				Expect(err).NotTo(HaveOccurred())
				return string(body)
			},
			2*time.Minute, 30*time.Minute, false),
		Entry("nuke#216 fixture, elapsed 27m of 30m (ratio 0.9) — fail-closes",
			func() string {
				body, err := os.ReadFile("testdata/review_bborbe_nuke_216_run1.md")
				Expect(err).NotTo(HaveOccurred())
				return string(body)
			},
			27*time.Minute, 30*time.Minute, true),
		// The toolchain wording pair differs only in elapsed: prose is inert.
		Entry("toolchain wording, elapsed 2m of 30m (ratio ~0.07) — approve stands",
			func() string { return fence(toolchainConcernJSON) },
			2*time.Minute, 30*time.Minute, false),
		Entry("toolchain wording, elapsed 27m of 30m (ratio 0.9) — fail-closes",
			func() string { return fence(toolchainConcernJSON) },
			27*time.Minute, 30*time.Minute, true),
		// Bare admissions on a short run are mislabels too (wording 1 and 3
		// from the deleted tier-keyed table, reused per requirement 7c).
		Entry("wording 1 bare admission, elapsed 2m of 30m — approve stands (mislabel)",
			func() string {
				return fence(
					`{"verdict":"approve","concerns_addressed":[{"concern":"tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested","disposition":"not-verified"}]}`,
				)
			},
			2*time.Minute, 30*time.Minute, false),
		Entry("wording 3 bare admission, elapsed 2m of 30m — approve stands (mislabel)",
			func() string {
				return fence(
					`{"verdict":"approve","concerns_addressed":[{"concern":"performance: I inspected the vendored copy and the mutex is uncontended in this call graph","disposition":"not-verified"}]}`,
				)
			},
			2*time.Minute, 30*time.Minute, false),
		// Threshold boundary: the integer-form comparison is exact at 0.8.
		Entry("boundary: elapsed 24m of 30m (exactly 0.8) — fail-closes",
			func() string { return fence(toolchainConcernJSON) },
			24*time.Minute, 30*time.Minute, true),
		Entry("boundary: elapsed 23m59s of 30m (just below 0.8) — approve stands",
			func() string { return fence(toolchainConcernJSON) },
			23*time.Minute+59*time.Second, 30*time.Minute, false),
		// Unknown budget: the integer comparison never divides by the budget, so
		// a zero budget demotes (fail-safe) instead of a NaN silently passing.
		Entry("unknown budget (elapsed 0, budget 0) — fail-closes (no-division fail-safe)",
			func() string { return fence(toolchainConcernJSON) },
			time.Duration(0), time.Duration(0), true),
		// No admission → no demotion regardless of budget.
		Entry("disposition not-an-issue, elapsed 27m of 30m — no admission, no demotion",
			func() string {
				return fence(
					`{"verdict":"approve","concerns_addressed":[{"concern":"security: rate-limit","disposition":"not-an-issue"}]}`,
				)
			},
			27*time.Minute, 30*time.Minute, false),
	)
})

var _ = Describe("incident regression (bborbe/discord-assistant#37 run 2)", func() {
	// Regression-lock for the 2026-08-30 false-CHANGES_REQUESTED: the run-2
	// verdict stored verbatim in the fixture carries `disposition: "not-an-issue"`,
	// so the gate must let the approve through even though the concern prose
	// contains the literal `not verified` substring (the old regex gate demoted
	// on that prose). Only the disposition value is transformed for the negative
	// row. The fixture body is read at test time (inside the leaf nodes) so a
	// missing/drifted fixture fails the rows, not the suite construction.
	DescribeTable("the disposition field wins over the concern prose",
		func(transform func(string) string, expected bool) {
			body, err := os.ReadFile("testdata/review_discord_assistant_37_run2.md")
			Expect(err).NotTo(HaveOccurred())
			text := string(body)
			if transform != nil {
				text = transform(text)
			}
			Expect(pkg.HasUnverifiedConcerns(text)).To(Equal(expected))
		},
		Entry("fixture as-is (not-an-issue) passes", nil, false),
		Entry("fixture with disposition flipped to not-verified demotes",
			func(text string) string {
				// Target the exact JSON field token, not the bare enum value — the
				// prose header may itself mention `not-an-issue`, and a
				// first-occurrence replace would flip the header instead of the JSON.
				flipped := strings.ReplaceAll(
					text,
					`"disposition": "not-an-issue"`,
					`"disposition": "not-verified"`,
				)
				// Fail loudly on header/format drift: a no-op substitution would
				// silently test the unflipped body and turn the lock into a tautology.
				Expect(flipped).NotTo(Equal(text))
				return flipped
			},
			true,
		),
	)

	It("parses the as-is fixture verdict as approve", func() {
		body, err := os.ReadFile("testdata/review_discord_assistant_37_run2.md")
		Expect(err).NotTo(HaveOccurred())
		result := pkg.ParseVerdict(string(body))
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
	})
})

var _ = Describe("incident regression (bborbe/nuke#216 run 1)", func() {
	// Regression-lock for the 2026-09-10 false-CHANGES_REQUESTED (review_id
	// 5172635280): the run-1 body stored verbatim in the fixture yields a clean
	// approve, and its four `not-verified` dispositions are four admissions. The
	// demotion rows in the budget-keyed table can only be meaningful against a
	// fixture whose verdict is an approve — assert that here so a fixture drift
	// that turns the verdict into request-changes fails loudly instead of making
	// the budget rows silently vacuous.
	It("parses the nuke#216 fixture verdict as approve", func() {
		body, err := os.ReadFile("testdata/review_bborbe_nuke_216_run1.md")
		Expect(err).NotTo(HaveOccurred())
		result := pkg.ParseVerdict(string(body))
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
	})
})
