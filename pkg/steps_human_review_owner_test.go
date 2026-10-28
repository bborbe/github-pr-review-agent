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

// stubStep is the inner step the wrapper is exercised against: it returns the
// configured result verbatim and records whether ShouldRun/Run were delegated.
type stubStep struct {
	name   string
	result *agentlib.Result
	err    error
}

func (s *stubStep) Name() string { return s.name }

func (s *stubStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

func (s *stubStep) Run(_ context.Context, _ *agentlib.Markdown) (*agentlib.Result, error) {
	return s.result, s.err
}

var _ = Describe("HumanReviewOwnerStep", func() {
	var (
		ctx context.Context
		md  *agentlib.Markdown
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		md, err = agentlib.ParseMarkdown(ctx, "---\nstatus: in_progress\n---\n\nbody")
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable(
		"stamps the owner on every routing class that lands in human_review",
		func(stepName string) {
			inner := &stubStep{
				name: stepName,
				result: &agentlib.Result{
					Status:    agentlib.AgentStatusDone,
					NextPhase: "human_review",
				},
			}

			got, err := pkg.NewHumanReviewOwnerStep(inner, "bborbe").Run(ctx, md)

			Expect(err).NotTo(HaveOccurred())
			// The wrapper never alters the result — routing stays the inner step's.
			Expect(got).To(Equal(inner.result))
			Expect(md.Frontmatter["assignee"]).To(Equal("bborbe"))
			// Without the declaration the controller's operator-owned-assignee guard
			// keeps the on-disk agent assignee and discards the stamp.
			Expect(md.Frontmatter["assignee_reassigned_by"]).To(Equal(stepName))
		},
		Entry("soft budget expiry (shared budgetExpiredResult)", "plan-execution"),
		Entry("planning: unparseable plan JSON", "planning"),
		Entry("planning: no GitHub PR URL", "planning"),
		Entry("execution: posting failed", "execution"),
		Entry("execution: no review produced, post skipped", "execution"),
		Entry("review: unparseable verdict", "review"),
		Entry("review: fail verdict", "review"),
		Entry("pr-override path", "override"),
	)

	It("leaves a non-human_review result untouched", func() {
		inner := &stubStep{
			name:   "review",
			result: &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "ai_review"},
		}

		got, err := pkg.NewHumanReviewOwnerStep(inner, "bborbe").Run(ctx, md)

		Expect(err).NotTo(HaveOccurred())
		Expect(got.NextPhase).To(Equal("ai_review"))
		Expect(md.Frontmatter).NotTo(HaveKey("assignee"))
		Expect(md.Frontmatter).NotTo(HaveKey("assignee_reassigned_by"))
	})

	It("stamps nothing when the owner is unconfigured", func() {
		inner := &stubStep{
			name:   "review",
			result: &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "human_review"},
		}

		wrapped := pkg.NewHumanReviewOwnerStep(inner, "")
		got, err := wrapped.Run(ctx, md)

		Expect(err).NotTo(HaveOccurred())
		Expect(got.NextPhase).To(Equal("human_review"))
		Expect(md.Frontmatter).NotTo(HaveKey("assignee"))
		// An unconfigured wrapper is the inner step itself, not a no-op decorator.
		Expect(wrapped).To(BeIdenticalTo(agentlib.Step(inner)))
	})

	It("delegates Name so wrapping does not rename a phase's step", func() {
		inner := &stubStep{name: "review"}
		Expect(pkg.NewHumanReviewOwnerStep(inner, "bborbe").Name()).To(Equal("review"))
	})

	It("returns the inner error unchanged and stamps nothing", func() {
		sentinel := context.Canceled
		inner := &stubStep{name: "review", err: sentinel}

		got, err := pkg.NewHumanReviewOwnerStep(inner, "bborbe").Run(ctx, md)

		Expect(err).To(Equal(sentinel))
		Expect(got).To(BeNil())
		Expect(md.Frontmatter).NotTo(HaveKey("assignee"))
	})
})
