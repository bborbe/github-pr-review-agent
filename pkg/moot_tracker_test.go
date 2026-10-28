// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/json"

	agentlib "github.com/bborbe/agent"
	base "github.com/bborbe/cqrs/base"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newReviewTask(id, assignee, status, phase string) agentlib.Task {
	return agentlib.Task{
		TaskIdentifier: agentlib.TaskIdentifier(id),
		Frontmatter: agentlib.TaskFrontmatter{
			"assignee": assignee,
			"status":   status,
			"phase":    phase,
		},
		Content: agentlib.TaskContent(
			"https://github.com/bborbe/maintainer/pull/2\n\nsome content\n",
		),
	}
}

var _ = Describe("MootTracker", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("admission",
		func(task agentlib.Task, expected bool) {
			tracker := pkg.NewMootTracker()
			tracker.Observe(ctx, task)
			if expected {
				Expect(tracker.List()).To(HaveLen(1))
			} else {
				Expect(tracker.List()).To(HaveLen(0))
			}
		},
		Entry("planning is admitted",
			newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "planning"), true),
		Entry("execution is admitted",
			newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "execution"), true),
		Entry("legacy in_progress phase normalises to execution and is admitted",
			newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "in_progress"), true),
		Entry("ai_review is admitted",
			newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "ai_review"), true),
		Entry("todo phase is rejected",
			newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "todo"), false),
		Entry("human_review phase is rejected",
			newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "human_review"), false),
		Entry("done phase is rejected",
			newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "done"), false),
		Entry("empty phase is rejected",
			newReviewTask("task-1", "pr-reviewer-agent", "in_progress", ""), false),
		Entry("next status is rejected",
			newReviewTask("task-1", "pr-reviewer-agent", "next", "execution"), false),
		Entry("completed status is rejected",
			newReviewTask("task-1", "pr-reviewer-agent", "completed", "execution"), false),
		Entry("aborted status is rejected",
			newReviewTask("task-1", "pr-reviewer-agent", "aborted", "execution"), false),
		Entry("empty status is rejected",
			newReviewTask("task-1", "pr-reviewer-agent", "", "execution"), false),
		Entry("another assignee is rejected",
			newReviewTask("task-1", "github-releaser-agent", "in_progress", "execution"), false),
		Entry("empty assignee is rejected",
			newReviewTask("task-1", "", "in_progress", "execution"), false),
	)

	// The table rows are hand-built structs, so only this spec fails when the
	// tracker is never fed from the task-event topic's actual payload shape.
	It("admits a task decoded from the task-event schema payload", func() {
		original := newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "execution")
		event, err := base.ParseEvent(ctx, original)
		Expect(err).NotTo(HaveOccurred())
		payload, err := json.Marshal(event)
		Expect(err).NotTo(HaveOccurred())

		var decoded agentlib.Task
		Expect(json.Unmarshal(payload, &decoded)).To(Succeed())
		Expect(decoded.TaskIdentifier).To(Equal(agentlib.TaskIdentifier("task-1")))

		tracker := pkg.NewMootTracker()
		tracker.Observe(ctx, decoded)
		Expect(tracker.List()).To(HaveLen(1))
		Expect(
			tracker.List()[0].Frontmatter.Assignee(),
		).To(Equal(agentlib.TaskAssignee("pr-reviewer-agent")))
	})

	Context("lifecycle", func() {
		It("evicts an admitted task that becomes terminal", func() {
			tracker := pkg.NewMootTracker()
			tracker.Observe(
				ctx,
				newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "execution"),
			)
			Expect(tracker.List()).To(HaveLen(1))

			tracker.Observe(
				ctx,
				newReviewTask("task-1", "pr-reviewer-agent", "completed", "execution"),
			)
			Expect(tracker.List()).To(BeEmpty())
		})

		It("evicts an admitted task that is reassigned", func() {
			tracker := pkg.NewMootTracker()
			tracker.Observe(
				ctx,
				newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "execution"),
			)
			Expect(tracker.List()).To(HaveLen(1))

			tracker.Observe(
				ctx,
				newReviewTask("task-1", "github-releaser-agent", "in_progress", "execution"),
			)
			Expect(tracker.List()).To(BeEmpty())
		})

		It("keeps only the newest payload for the same task id", func() {
			tracker := pkg.NewMootTracker()
			tracker.Observe(
				ctx,
				newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "planning"),
			)
			tracker.Observe(
				ctx,
				newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "ai_review"),
			)

			tasks := tracker.List()
			Expect(tasks).To(HaveLen(1))
			Expect(tasks[0].Frontmatter.Phase()).NotTo(BeNil())
			Expect(*tasks[0].Frontmatter.Phase()).To(Equal(domain.TaskPhaseAIReview))
		})

		It("orders distinct tasks by id ascending", func() {
			tracker := pkg.NewMootTracker()
			tracker.Observe(
				ctx,
				newReviewTask("task-2", "pr-reviewer-agent", "in_progress", "execution"),
			)
			tracker.Observe(
				ctx,
				newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "execution"),
			)

			tasks := tracker.List()
			Expect(tasks).To(HaveLen(2))
			Expect(tasks[0].TaskIdentifier).To(Equal(agentlib.TaskIdentifier("task-1")))
			Expect(tasks[1].TaskIdentifier).To(Equal(agentlib.TaskIdentifier("task-2")))
		})

		It("removes a tracked task and ignores unknown ids", func() {
			tracker := pkg.NewMootTracker()
			tracker.Observe(
				ctx,
				newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "execution"),
			)

			tracker.Remove(agentlib.TaskIdentifier("task-unknown"))
			Expect(tracker.List()).To(HaveLen(1))

			tracker.Remove(agentlib.TaskIdentifier("task-1"))
			Expect(tracker.List()).To(BeEmpty())
		})
	})
})
