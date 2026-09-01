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
		// the sandbox, and names CI/precommit as the gate. Tier-keyed: no MUST-tier
		// blocker language → the approve must NOT fail-close. This exact body
		// posted a false CHANGES_REQUESTED on the octopus fleet on 2026-09-01.
		Entry(
			"benign toolchain-limited not verified (Go 1.27 toolchain unavailable, CI is the gate)",
			fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"correctness: go.mod go directive 1.27.0 dep compatibility","detail":"not verified - module files internally consistent (tidy ran, no downgrades, all hashes present) but transitive go-directive compatibility requires a Go 1.27 toolchain not available in the review sandbox; repo CI precommit (go mod tidy/verify + build) is the gate","disposition":"not-verified"}]}`,
			),
			false,
		),
		// The octopus fleet posts the LEGACY flat-string shape (the model wrote
		// the whole explanation in one string, no disposition object). Same
		// quickbooks#4 09:46 content verbatim: the flag wording is the admission,
		// the explanation names the verifier (CI/precommit) -> must pass.
		Entry(
			"benign toolchain-limited not verified — legacy flat-string shape (octopus posted form)",
			fence(
				`{"verdict":"approve","concerns_addressed":["correctness: go.mod go directive 1.27.0 dep compatibility: not verified - module files internally consistent (tidy ran, no downgrades, all hashes present) but transitive go-directive compatibility requires a Go 1.27 toolchain not available in the review sandbox; repo CI precommit (go mod tidy/verify + build) is the gate"]}`,
			),
			false,
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
	)

	// The pairs below differ ONLY in the `disposition` enum value — the concern
	// prose is byte-identical across each pair. Three distinct organic
	// explanation wordings: the incident run-2 gap phrasing, the nuke#68
	// cross-check phrasing that escaped the old benign whitelist, and an
	// invented phrasing matching no former whitelist entry. Tier-keyed
	// contract (2026-09-01, Seibert-Data/quickbooks#4): `not-an-issue` always
	// passes; `not-verified` demotes unless the concern itself explains the gap
	// as benign — wording 2 ("could not be cross-checked") is a benign
	// explanation and passes even with `not-verified`; wordings 1 and 3 carry no
	// benign explanation, so `not-verified` demotes (bare unexamined admission).
	DescribeTable("tier-keyed: not-verified demotes only when MUST-tier or a bare admission",
		func(concern, disposition string, expected bool) {
			Expect(pkg.HasUnverifiedConcerns(fence(
				`{"verdict":"approve","concerns_addressed":[{"concern":"` + concern + `","disposition":"` + disposition + `"}]}`,
			))).To(Equal(expected))
		},
		// Wording 1 — the incident run-2 gap phrasing (bborbe/discord-assistant#37).
		Entry(
			"wording 1 not-an-issue passes",
			"tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested",
			"not-an-issue",
			false,
		),
		Entry(
			"wording 1 not-verified demotes (bare admission)",
			"tests: limit=200 safety valve not directly tested when transcripts are within the age window — not verified: scenario requires 200+ transcripts in same cwd, gap is reasonable to leave untested",
			"not-verified",
			true,
		),
		// Wording 2 — the nuke#68 cross-check phrasing (escaped the old whitelist).
		Entry(
			"wording 2 not-an-issue passes",
			"correctness: could not be cross-checked against the actual controller code. Not verified.",
			"not-an-issue",
			false,
		),
		Entry(
			"wording 2 not-verified passes (benign cross-check gap explained)",
			"correctness: could not be cross-checked against the actual controller code. Not verified.",
			"not-verified",
			false,
		),
		// Wording 3 — invented phrasing matching no former whitelist entry.
		Entry(
			"wording 3 not-an-issue passes",
			"performance: I inspected the vendored copy and the mutex is uncontended in this call graph",
			"not-an-issue",
			false,
		),
		Entry(
			"wording 3 not-verified demotes (bare admission)",
			"performance: I inspected the vendored copy and the mutex is uncontended in this call graph",
			"not-verified",
			true,
		),
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
