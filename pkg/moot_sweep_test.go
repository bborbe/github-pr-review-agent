// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"

	agentlib "github.com/bborbe/agent"
	base "github.com/bborbe/cqrs/base"
	cqrsmocks "github.com/bborbe/cqrs/mocks"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	sweepPRURL = "https://github.com/bborbe/maintainer/pull/2"
	sweepRef   = "abc123"
	sweepHead  = "def456"
)

func newSweepTask(id, currentJob string) agentlib.Task {
	return agentlib.Task{
		TaskIdentifier: agentlib.TaskIdentifier(id),
		Vault:          "maintainer",
		Frontmatter: agentlib.TaskFrontmatter{
			"assignee":    "pr-reviewer-agent",
			"status":      "in_progress",
			"phase":       "execution",
			"ref":         sweepRef,
			"current_job": currentJob,
		},
		Content: agentlib.TaskContent(sweepPRURL + "\n\nsome content\n"),
	}
}

var _ = Describe("MootSweep", func() {
	var (
		ctx             context.Context
		tracker         pkg.MootTracker
		fakePRState     *mocks.PRStateClient
		fakeSender      *cqrsmocks.CDBCommandObjectSender
		sweep           pkg.MootSweep
		currentDateTime libtime.CurrentDateTime
	)

	BeforeEach(func() {
		ctx = context.Background()
		tracker = pkg.NewMootTracker()
		fakePRState = &mocks.PRStateClient{}
		fakeSender = &cqrsmocks.CDBCommandObjectSender{}
		currentDateTime = libtime.NewCurrentDateTime()
		sweep = pkg.NewMootSweep(tracker, fakePRState, fakeSender, currentDateTime)
	})

	// assertClosure reads back the single published command object and checks
	// the routing's own fields plus the sweep's provenance stamps.
	assertClosure := func(expectedStatus, expectedVerdict string) {
		Expect(fakeSender.SendCommandObjectCallCount()).To(Equal(1))
		_, commandObject := fakeSender.SendCommandObjectArgsForCall(0)
		Expect(commandObject.SchemaID).To(Equal(agentlib.TaskV1SchemaID))
		Expect(commandObject.Command.Operation).To(Equal(base.UpdateOperation))

		var written agentlib.Task
		Expect(commandObject.Command.Data.MarshalInto(ctx, &written)).To(Succeed())
		Expect(written.TaskIdentifier).To(Equal(agentlib.TaskIdentifier("task-1")))
		Expect(written.Vault).To(Equal("maintainer"))
		Expect(written.Frontmatter["status"]).To(Equal(expectedStatus))
		Expect(written.Frontmatter["phase"]).To(Equal("done"))
		Expect(written.Frontmatter["closed_by"]).To(Equal("sweep"))

		writtenMD, err := agentlib.ParseMarkdown(ctx, string(written.Content))
		Expect(err).NotTo(HaveOccurred())
		resolution, err := agentlib.ExtractSection[pkg.ResolutionOutput](
			ctx, writtenMD, "## Resolution",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolution.Verdict).To(Equal(expectedVerdict))
	}

	DescribeTable("routing",
		func(
			state, head, mergedAt string,
			expectClosed bool,
			expectStatus, expectVerdict string,
		) {
			fakePRState.PRStateReturns(state, mergedAt, head, nil)
			task := newSweepTask("task-1", "")
			tracker.Observe(ctx, task)

			wasClosed, err := sweep.SweepTask(ctx, task)
			Expect(err).NotTo(HaveOccurred())
			Expect(wasClosed).To(Equal(expectClosed))

			if !expectClosed {
				Expect(fakeSender.SendCommandObjectCallCount()).To(Equal(0))
				return
			}
			assertClosure(expectStatus, expectVerdict)
		},
		Entry("OPEN at the reviewed ref is not closed",
			"OPEN", sweepRef, "", false, "", ""),
		Entry("OPEN past the reviewed ref is superseded",
			"OPEN", "999999", "", true, "aborted", "superseded"),
		Entry("MERGED closes completed",
			"MERGED", sweepHead, "2026-10-28T10:00:00Z", true, "completed", "merged"),
		Entry("CLOSED closes completed as closed_unmerged",
			"CLOSED", sweepHead, "", true, "completed", "closed_unmerged"),
	)

	DescribeTable("spawn_state",
		func(currentJob, expectedSpawnState string) {
			fakePRState.PRStateReturns("MERGED", "2026-10-28T10:00:00Z", sweepHead, nil)
			task := newSweepTask("task-1", currentJob)
			tracker.Observe(ctx, task)

			wasClosed, err := sweep.SweepTask(ctx, task)
			Expect(err).NotTo(HaveOccurred())
			Expect(wasClosed).To(BeTrue())

			Expect(fakeSender.SendCommandObjectCallCount()).To(Equal(1))
			_, commandObject := fakeSender.SendCommandObjectArgsForCall(0)
			var written agentlib.Task
			Expect(commandObject.Command.Data.MarshalInto(ctx, &written)).To(Succeed())
			Expect(written.Frontmatter["closed_by"]).To(Equal("sweep"))
			Expect(written.Frontmatter["spawn_state"]).To(Equal(expectedSpawnState))
		},
		Entry("no job assigned", "", "none"),
		Entry("job already assigned",
			"pr-reviewer-agent-task-1-20261028120000", "job_assigned"),
	)

	It("leaves a task tracked when GitHub cannot be reached", func() {
		fakePRState.PRStateReturns("", "", "", stderrors.New("boom"))
		task := newSweepTask("task-1", "")
		tracker.Observe(ctx, task)

		wasClosed, err := sweep.SweepTask(ctx, task)
		Expect(err).NotTo(HaveOccurred())
		Expect(wasClosed).To(BeFalse())

		closed, err := sweep.SweepPass(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(closed).To(BeEmpty())
		Expect(fakeSender.SendCommandObjectCallCount()).To(Equal(0))
		Expect(tracker.List()).To(HaveLen(1))
	})

	It("leaves a task tracked when the closure cannot be published", func() {
		fakePRState.PRStateReturns("MERGED", "2026-10-28T10:00:00Z", sweepHead, nil)
		fakeSender.SendCommandObjectReturns(stderrors.New("send failed"))
		task := newSweepTask("task-1", "")
		tracker.Observe(ctx, task)

		wasClosed, err := sweep.SweepTask(ctx, task)
		Expect(err).To(HaveOccurred())
		Expect(wasClosed).To(BeFalse())

		closed, err := sweep.SweepPass(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(closed).To(BeEmpty())
		Expect(tracker.List()).To(HaveLen(1))
	})

	It("writes only the tasks it closes in a mixed pass", func() {
		fakePRState.PRStateStub = func(
			_ context.Context, prURL string,
		) (string, string, string, error) {
			switch prURL {
			case "https://github.com/bborbe/maintainer/pull/2":
				return "OPEN", "", "999999", nil
			default:
				return "OPEN", "", "def456", nil
			}
		}

		mootTask := newSweepTask("task-moot", "")
		currentTask := newSweepTask("task-current", "")
		currentTask.Frontmatter["ref"] = "def456"
		currentTask.Content = agentlib.TaskContent(
			"https://github.com/bborbe/maintainer/pull/3\n\nsome content\n",
		)

		tracker.Observe(ctx, mootTask)
		tracker.Observe(ctx, currentTask)

		closed, err := sweep.SweepPass(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(closed).To(Equal([]agentlib.TaskIdentifier{"task-moot"}))
		Expect(fakeSender.SendCommandObjectCallCount()).To(Equal(1))
		Expect(tracker.List()).To(HaveLen(1))
		Expect(tracker.List()[0]).To(Equal(currentTask))
	})
})
