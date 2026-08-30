// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
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
		Entry(
			"flagged lowercase not verified",
			fence(
				`{"verdict":"approve","concerns_addressed":["security: rate-limit not verified"]}`,
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
		// fail-close. Still a legacy bare string here — its `not verified` text
		// demotes via the legacy substring rule; the next prompt migrates it to
		// the object shape.
		Entry(
			"MUST-tier unverified blocker (alerts will never fire)",
			fence(
				`{"verdict":"approve","concerns_addressed":["correctness: expression references github_build_watcher_rate_limit_remaining — not verified: build-watcher source not present in this monorepo, metric existence cannot be confirmed; without it the alerts will never fire. Must verify metric is exported before deploying."]}`,
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
	)
})
