// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github_test

import (
	"context"
	"strings"

	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Client", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("NewGHClient", func() {
		It("creates a non-nil client with empty token", func() {
			client := github.NewGHClient("")
			Expect(client).NotTo(BeNil())
		})

		It("creates a non-nil client with token", func() {
			client := github.NewGHClient("test-token")
			Expect(client).NotTo(BeNil())
		})

		It("implements Client interface", func() {
			var _ github.Client = github.NewGHClient("") //nolint:staticcheck
		})
	})

	Context("GetPRBranches", func() {
		It("requires valid context", func() {
			client := github.NewGHClient("")
			_, err := client.GetPRBranches(ctx, "owner", "repo", 123)
			// Will fail in test env without gh CLI, but validates interface contract
			Expect(err).To(HaveOccurred())
		})

		It("respects context cancellation", func() {
			client := github.NewGHClient("")
			cancelCtx, cancel := context.WithCancel(ctx)
			cancel() // Cancel immediately
			_, err := client.GetPRBranches(cancelCtx, "owner", "repo", 123)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("PostComment", func() {
		It("requires valid context", func() {
			client := github.NewGHClient("")
			err := client.PostComment(ctx, "owner", "repo", 123, "test comment")
			// Will fail in test env without gh CLI, but validates interface contract
			Expect(err).To(HaveOccurred())
		})

		It("respects context cancellation", func() {
			client := github.NewGHClient("")
			cancelCtx, cancel := context.WithCancel(ctx)
			cancel() // Cancel immediately
			err := client.PostComment(cancelCtx, "owner", "repo", 123, "test comment")
			Expect(err).To(HaveOccurred())
		})
	})

	Context("SubmitReview", func() {
		It("returns error for unknown verdict", func() {
			client := github.NewGHClient("")
			err := client.SubmitReview(
				ctx,
				"owner",
				"repo",
				123,
				"test review",
				prpkg.Verdict("unknown"),
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unsupported verdict"))
		})

		It("respects context cancellation", func() {
			client := github.NewGHClient("")
			cancelCtx, cancel := context.WithCancel(ctx)
			cancel() // Cancel immediately
			err := client.SubmitReview(
				cancelCtx,
				"owner",
				"repo",
				123,
				"test review",
				prpkg.VerdictApprove,
			)
			Expect(err).To(HaveOccurred())
		})

		// NOTE: Success path tests for approve and request-changes are not practical
		// without refactoring. The ghClient uses exec.CommandContext internally, which
		// cannot be mocked without injecting a command executor interface. To test:
		// - Correct gh pr review command construction with --approve or --request-changes
		// - GH_TOKEN environment variable handling
		// - Body text passing
		// We would need to refactor ghClient to accept an injectable command executor.
	})

	Context("NotifySuperseded", func() {
		It("respects context cancellation", func() {
			client := github.NewGHClient("")
			cancelCtx, cancel := context.WithCancel(ctx)
			cancel() // Cancel immediately
			err := client.NotifySuperseded(
				cancelCtx,
				"https://github.com/nonexistent-owner/nonexistent-repo/pull/1",
				"abc123",
				"def456",
				"run-1",
			)
			Expect(err).To(HaveOccurred())
		})

		It("fails for an unreachable PR URL", func() {
			client := github.NewGHClient("")
			err := client.NotifySuperseded(
				ctx,
				"https://github.com/nonexistent-owner/nonexistent-repo/pull/1",
				"abc123",
				"def456",
				"run-1",
			)
			Expect(err).To(HaveOccurred())
		})

		// NOTE: The success-path argv (`gh pr review <url> --comment --body ...`)
		// is not exercised without executor injection. The concrete client's
		// contract here is the shell-out + error surface; the body composition is
		// covered by the buildSupersededNoticeBody table below.
	})

	Context("buildSupersededNoticeBody", func() {
		It("opens with the required sentence and ends with machine-readable keys", func() {
			body := github.BuildSupersededNoticeBodyForTest("abc123", "def456", "run-1")
			Expect(body).To(HavePrefix("Automated review superseded — no review was posted."))
			Expect(body).To(ContainSubstring("run: run-1"))
			Expect(body).To(ContainSubstring("reviewed_ref: abc123"))
			Expect(body).To(ContainSubstring("current_head: def456"))
			Expect(body).To(HaveSuffix("run: run-1\nreviewed_ref: abc123\ncurrent_head: def456\n"))
		})

		It("tolerates empty inputs without inventing values", func() {
			var body string
			Expect(func() {
				body = github.BuildSupersededNoticeBodyForTest("", "", "")
			}).NotTo(Panic())
			Expect(body).To(HavePrefix("Automated review superseded — no review was posted."))
			Expect(body).To(ContainSubstring("run: \n"))
			Expect(body).To(ContainSubstring("reviewed_ref: \n"))
			Expect(body).To(ContainSubstring("current_head: \n"))
			Expect(body).NotTo(ContainSubstring("unknown"))
		})

		It("caps an over-long runID at 2000 characters", func() {
			body := github.BuildSupersededNoticeBodyForTest(
				"abc123",
				"def456",
				strings.Repeat("x", 3000),
			)
			Expect(len(body)).To(BeNumerically("<=", 2000))
		})
	})

	Context("PRState", func() {
		It("fails for an unreachable PR URL", func() {
			client := github.NewGHClient("")
			_, _, _, err := client.PRState(
				ctx,
				"https://github.com/nonexistent-owner/nonexistent-repo/pull/1",
			)
			// 404 from gh against a non-existent repo — validates the shell-out
			// + error surface regardless of whether gh CLI is present locally.
			Expect(err).To(HaveOccurred())
		})

		It("respects context cancellation", func() {
			client := github.NewGHClient("")
			cancelCtx, cancel := context.WithCancel(ctx)
			cancel() // Cancel immediately
			_, _, _, err := client.PRState(
				cancelCtx,
				"https://github.com/nonexistent-owner/nonexistent-repo/pull/1",
			)
			Expect(err).To(HaveOccurred())
		})

		// NOTE: Success path tests (MERGED/CLOSED/OPEN state routing) are not
		// practical without refactoring. The ghClient uses exec.CommandContext
		// internally, which cannot be mocked without injecting a command executor
		// interface. The routing logic over the three states is covered by
		// pr_state_check_test.go via the counterfeiter fake — the concrete
		// client's contract here is just the shell-out + error surface.
	})

	Context("PRDiff", func() {
		It("fails for an unreachable PR URL", func() {
			client := github.NewGHClient("")
			_, err := client.PRDiff(
				ctx,
				"https://github.com/nonexistent-owner/nonexistent-repo/pull/1",
			)
			// 404 from gh against a non-existent repo — validates the shell-out
			// + error surface regardless of whether gh CLI is present locally.
			Expect(err).To(HaveOccurred())
		})

		It("respects context cancellation", func() {
			client := github.NewGHClient("")
			cancelCtx, cancel := context.WithCancel(ctx)
			cancel() // Cancel immediately
			_, err := client.PRDiff(
				cancelCtx,
				"https://github.com/nonexistent-owner/nonexistent-repo/pull/1",
			)
			Expect(err).To(HaveOccurred())
		})

		// NOTE: Success path tests (diff fetching + routing) are not practical
		// without refactoring. The ghClient uses exec.CommandContext internally,
		// which cannot be mocked without injecting a command executor interface.
		// The success-path wiring — the fetched diff reaching the ai_review
		// verifier prompt — is covered by the fake-runner wiring rows in
		// pkg/steps_review_test.go; the concrete client's contract here is just
		// the shell-out + error surface.
	})

	// NOTE: Success path tests for GetPRBranch and PostComment are not practical
	// without refactoring. The ghClient uses exec.CommandContext internally, which
	// cannot be mocked without injecting a command executor interface. To test:
	// - Branch name parsing from gh output
	// - Correct command argument construction
	// - GH_TOKEN environment variable handling
	// We would need to refactor ghClient to accept an injectable command executor,
	// which is outside the scope of adding tests to existing code.
})
