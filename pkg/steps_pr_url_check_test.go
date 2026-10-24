// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PRURLPreflightStep", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("gates the pipeline on a PR URL in the task body",
		func(body string, expectedStatus agentlib.AgentStatus) {
			md, err := agentlib.ParseMarkdown(ctx, body)
			Expect(err).NotTo(HaveOccurred())

			step := pkg.NewPRURLPreflightStep()
			Expect(step.ShouldRun(ctx, md)).To(BeTrue())

			result, err := step.Run(ctx, md)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Status).To(Equal(expectedStatus))

			if expectedStatus == agentlib.AgentStatusDone {
				Expect(result.ContinueToNext).To(BeTrue(),
					"a found URL must let the phase continue")
				return
			}
			Expect(result.Message).NotTo(BeEmpty(),
				"needs_input must tell the operator what is missing")
		},
		// The body below is the exact document read by Job
		// pr-reviewer-agent-55d44221-20261024201049-x86qn (started 20:10:49Z) in the
		// 2026-10-24 prod incident — task 55d44221 at commit c0b967a390 of the
		// private-agent vault. It is the size-gate "parked for human review" text with
		// no PR URL anywhere. Before this step the run completed a full review and
		// only failed in the post step with failure_step=pr_url_extraction,
		// discarding it.
		Entry(
			"real parked-for-human-review task body — needs_input",
			`---
assignee: ""
base_ref: master
clone_url: https://github.com/bborbe/git-rest.git
phase: human_review
ref: 9a509d20aae4d4970c3587856e1d1bc95aaef68e
stage: prod
status: todo
target_vault: openclaw
task_identifier: 55d44221-4a05-5193-a56f-490ec9060b72
task_type: pr-review
title: 'fix: serve claim refs from a local mirror instead of ls-remote per GET'
---
## Oversized PR — parked for human review

This PR has 1275 added lines (limit 1200) and 9 changed files (limit 100), which exceeds the auto-reviewer's context budget. Spawning a review pod would overflow its context window (`+"`claude CLI failed: Prompt is too long`"+`) and die before posting a verdict.

1314 additions across 7 files excluded by `+"`.reviewignore`"+` (not counted toward the size gate, not sent to the reviewer).

To review anyway, edit the frontmatter: `+"`assignee: pr-reviewer-agent`"+`, `+"`phase: in_progress`"+`, `+"`status: in_progress`"+`. To dismiss, set `+"`status: aborted`"+`.`,
			agentlib.AgentStatusNeedsInput,
		),
		Entry(
			"URL in preamble — done, continue",
			"https://github.com/bborbe/git-rest/pull/51\n\n## Plan\n\nbody",
			agentlib.AgentStatusDone,
		),
		// The watcher's own layout: the URL lives in the H1 body, not the preamble.
		Entry(
			"URL in H1 body — done, continue",
			"# PR Review: bborbe/git-rest#51\n\nhttps://github.com/bborbe/git-rest/pull/51\n## Plan\n\nbody",
			agentlib.AgentStatusDone,
		),
		Entry(
			"no URL anywhere — needs_input",
			"# H1 only\n\nno url here\n## Plan",
			agentlib.AgentStatusNeedsInput,
		),
	)
})
