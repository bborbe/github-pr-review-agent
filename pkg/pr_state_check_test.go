// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"errors"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/github-pr-review-agent/mocks"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PRStateCheck", func() {
	const (
		testPRURL   = "https://github.com/bborbe/maintainer/pull/2"
		testRef     = "abc123"
		testHeadOid = "def456"
	)

	var (
		ctx  context.Context
		fake *mocks.GitHubClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		fake = &mocks.GitHubClient{}
	})

	buildMD := func() *agentlib.Markdown {
		md, err := agentlib.ParseMarkdown(
			ctx,
			"---\nref: "+testRef+"\n---\n"+testPRURL+"\n\nsome content",
		)
		Expect(err).NotTo(HaveOccurred())
		return md
	}

	Context("nil client", func() {
		It("is a no-op — returns nil result, nil error", func() {
			result, err := pkg.PRStateCheckForTest(ctx, buildMD(), nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
		})
	})

	Context("idempotency guard", func() {
		It("skips when status is already completed", func() {
			md := buildMD()
			md.Frontmatter["status"] = "completed"
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
			Expect(fake.PRStateCallCount()).To(Equal(0))
		})

		It("skips when status is already aborted", func() {
			md := buildMD()
			md.Frontmatter["status"] = "aborted"
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
			Expect(fake.PRStateCallCount()).To(Equal(0))
		})

		It("skips when ## Resolution is already present", func() {
			md := buildMD()
			md.ReplaceSection(agentlib.Section{
				Heading: "## Resolution",
				Body:    "```json\n{\"verdict\":\"merged\"}\n```",
			})
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
			Expect(fake.PRStateCallCount()).To(Equal(0))
		})
	})

	Context("no PR URL in task", func() {
		It("is a no-op — nothing to ask GitHub about", func() {
			md, err := agentlib.ParseMarkdown(ctx, "---\nref: "+testRef+"\n---\nno url here")
			Expect(err).NotTo(HaveOccurred())
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
			Expect(fake.PRStateCallCount()).To(Equal(0))
		})
	})

	Context("gh PRState error", func() {
		It("is a no-op — proceeds with phase work on transient gh failure", func() {
			fake.PRStateReturns("", "", "", context.DeadlineExceeded)
			result, err := pkg.PRStateCheckForTest(ctx, buildMD(), fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
			Expect(fake.PRStateCallCount()).To(Equal(1))
		})
	})

	Context("MERGED", func() {
		It("closes as completed + Resolution(verdict=merged) + NextPhase=done", func() {
			fake.PRStateReturns("MERGED", "2026-06-08T12:00:00Z", testHeadOid, nil)
			md := buildMD()
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
			Expect(md.Frontmatter["status"]).To(Equal("completed"))
			Expect(md.Frontmatter["phase"]).To(Equal("done"))

			resolution, ok := md.FindSection("## Resolution")
			Expect(ok).To(BeTrue())
			Expect(resolution.Body).To(ContainSubstring(`"verdict": "merged"`))
			Expect(resolution.Body).To(ContainSubstring(`"pr_state": "MERGED"`))
			Expect(resolution.Body).To(ContainSubstring(`"merged_at": "2026-06-08T12:00:00Z"`))

			_, url := fake.PRStateArgsForCall(0)
			Expect(url).To(Equal(testPRURL))
		})
	})

	Context("CLOSED", func() {
		It("closes as completed + Resolution(verdict=closed_unmerged) + NextPhase=done", func() {
			fake.PRStateReturns("CLOSED", "", testHeadOid, nil)
			md := buildMD()
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
			Expect(md.Frontmatter["status"]).To(Equal("completed"))
			Expect(md.Frontmatter["phase"]).To(Equal("done"))

			resolution, ok := md.FindSection("## Resolution")
			Expect(ok).To(BeTrue())
			Expect(resolution.Body).To(ContainSubstring(`"verdict": "closed_unmerged"`))
			Expect(resolution.Body).To(ContainSubstring(`"pr_state": "CLOSED"`))
		})
	})

	Context("OPEN at the reviewed ref", func() {
		It("is a no-op — review still meaningful, no resolution written", func() {
			fake.PRStateReturns("OPEN", "", testRef, nil) // head == task ref
			md := buildMD()
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())

			_, exists := md.FindSection("## Resolution")
			Expect(exists).To(BeFalse())
		})
	})

	Context("OPEN past the reviewed ref (superseded)", func() {
		It("closes as aborted + Resolution(verdict=superseded) + NextPhase=done", func() {
			fake.PRStateReturns("OPEN", "", testHeadOid, nil) // head != task ref
			md := buildMD()
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
			Expect(md.Frontmatter["status"]).To(Equal("aborted"))
			Expect(md.Frontmatter["phase"]).To(Equal("done"))

			resolution, ok := md.FindSection("## Resolution")
			Expect(ok).To(BeTrue())
			Expect(resolution.Body).To(ContainSubstring(`"verdict": "superseded"`))
			Expect(resolution.Body).To(ContainSubstring(`"pr_state": "OPEN"`))
			Expect(resolution.Body).To(ContainSubstring(`"head_ref_oid": "` + testHeadOid + `"`))
		})
	})

	Context("superseded notice", func() {
		const runID = "00000000-0000-0000-0000-000000000042"

		buildMDWith := func(extra string) *agentlib.Markdown {
			md, err := agentlib.ParseMarkdown(
				ctx,
				"---\nref: "+testRef+"\n"+extra+"---\n"+testPRURL+"\n\nsome content",
			)
			Expect(err).NotTo(HaveOccurred())
			return md
		}

		It("notifies once for a superseded verdict, even without job_started_at", func() {
			// The in-pod TaskContent never carries job_started_at: the executor
			// stamps it through an async UpdateFrontmatterCommand after the Job
			// spec was rendered. A running pod is itself the proof the run
			// started, so the notice must fire on exactly this shape.
			fake.PRStateReturns("OPEN", "", testHeadOid, nil)
			md := buildMDWith("task_identifier: " + runID + "\n")
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(fake.NotifySupersededCallCount()).To(Equal(1))

			_, url, ref, head, gotRunID := fake.NotifySupersededArgsForCall(0)
			Expect(url).To(Equal(testPRURL))
			Expect(ref).To(Equal(testRef))
			Expect(head).To(Equal(testHeadOid))
			Expect(gotRunID).To(Equal(runID))

			resolution, ok := md.FindSection("## Resolution")
			Expect(ok).To(BeTrue())
			Expect(resolution.Body).To(ContainSubstring(`"notified": true`))
		})

		It("does not notify from the sweep when no pod ever existed", func() {
			// The sweep reads the LIVE record, so job_started_at is meaningful
			// there: a queued record closed before any pod existed stays quiet.
			fake.PRStateReturns("OPEN", "", testHeadOid, nil)
			md := buildMDWith("task_identifier: " + runID + "\n")
			result, err := pkg.PRStateCheckFromSweepForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(fake.NotifySupersededCallCount()).To(Equal(0))

			resolution, ok := md.FindSection("## Resolution")
			Expect(ok).To(BeTrue())
			Expect(resolution.Body).To(ContainSubstring(`"verdict": "superseded"`))
			Expect(resolution.Body).NotTo(ContainSubstring(`"notified"`))
		})

		It("notifies from the sweep when job_started_at is set", func() {
			fake.PRStateReturns("OPEN", "", testHeadOid, nil)
			md := buildMDWith(
				"job_started_at: \"2026-06-08T12:00:00Z\"\ntask_identifier: " + runID + "\n",
			)
			result, err := pkg.PRStateCheckFromSweepForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(fake.NotifySupersededCallCount()).To(Equal(1))
		})

		It("notifies when cancelled mid-run (cancelled_during set)", func() {
			fake.PRStateReturns("OPEN", "", testHeadOid, nil)
			md := buildMD()
			result, err := pkg.PRStateCheckCancelledForTest(
				ctx, md, fake, "ai_review",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(fake.NotifySupersededCallCount()).To(Equal(1))
		})

		It("does not notify for a merged PR", func() {
			fake.PRStateReturns("MERGED", "2026-06-08T12:00:00Z", testHeadOid, nil)
			md := buildMDWith("job_started_at: \"2026-06-08T12:00:00Z\"\n")
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(fake.NotifySupersededCallCount()).To(Equal(0))
		})

		It("does not notify for a closed-unmerged PR", func() {
			fake.PRStateReturns("CLOSED", "", testHeadOid, nil)
			md := buildMDWith("job_started_at: \"2026-06-08T12:00:00Z\"\n")
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(fake.NotifySupersededCallCount()).To(Equal(0))
		})

		It("does not notify a record that already carries ## Resolution", func() {
			md := buildMDWith("job_started_at: \"2026-06-08T12:00:00Z\"\n")
			md.ReplaceSection(agentlib.Section{
				Heading: "## Resolution",
				Body:    "```json\n{\"verdict\":\"superseded\"}\n```",
			})
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeNil())
			Expect(fake.NotifySupersededCallCount()).To(Equal(0))
		})

		It("still closes when the notice fails and records notified=false", func() {
			fake.PRStateReturns("OPEN", "", testHeadOid, nil)
			fake.NotifySupersededReturns(errors.New("gh exploded"))
			md := buildMDWith("job_started_at: \"2026-06-08T12:00:00Z\"\n")
			result, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
			Expect(md.Frontmatter["status"]).To(Equal("aborted"))
			Expect(md.Frontmatter["phase"]).To(Equal("done"))

			resolution, ok := md.FindSection("## Resolution")
			Expect(ok).To(BeTrue())
			Expect(resolution.Body).To(ContainSubstring(`"notified": false`))
			Expect(resolution.Body).To(ContainSubstring(`"notify_error": "gh exploded"`))
		})
	})

	Context("idempotency — running the check twice", func() {
		It("does not double-append ## Resolution or rewrite frontmatter", func() {
			fake.PRStateReturns("MERGED", "2026-06-08T12:00:00Z", testHeadOid, nil)
			md := buildMD()

			result1, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result1).NotTo(BeNil())

			// Second run: task is now status=completed → idempotency guard fires.
			result2, err := pkg.PRStateCheckForTest(ctx, md, fake)
			Expect(err).NotTo(HaveOccurred())
			Expect(result2).To(BeNil())

			count := 0
			for _, sec := range md.Sections {
				if sec.Heading == "## Resolution" {
					count++
				}
			}
			Expect(count).To(Equal(1))
			Expect(md.Frontmatter["status"]).To(Equal("completed"))
		})
	})
})
