// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"regexp"
	"time"

	agentlib "github.com/bborbe/agent"
	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var diagnosticStampPattern = regexp.MustCompile(`job_run: ([0-9T:+\-Z]+)`)

var _ = Describe("budgetExpiredResult", func() {
	It("stamps ## Diagnostics from the injected clock, not the wall clock", func() {
		ctx := context.Background()
		md, err := agentlib.ParseMarkdown(ctx, "---\nref: abc123\n---\n\n## Review\n\nbody\n")
		Expect(err).NotTo(HaveOccurred())

		// A clock pinned far outside any plausible wall-clock reading: a
		// time.Now() stamp cannot produce this value, so equality here is what
		// discriminates an injected clock from the wall clock.
		injected := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
		clock := libtime.NewCurrentDateTime()
		clock.SetNow(libtime.DateTime(injected))

		result := pkg.BudgetExpiredResultForTest(
			md,
			"planning",
			libtime.Duration(25*time.Minute),
			clock,
		)

		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(result.NextPhase).To(Equal("human_review"))

		section, exists := md.FindSection("## Diagnostics")
		Expect(exists).To(BeTrue())
		Expect(section.Body).To(ContainSubstring("soft_budget_expired:planning"))

		matches := diagnosticStampPattern.FindStringSubmatch(section.Body)
		Expect(matches).To(HaveLen(2))
		Expect(matches[1]).To(Equal(injected.Format(time.RFC3339)))
	})

	It("routes without writing a diagnostic when the markdown is nil", func() {
		clock := libtime.NewCurrentDateTime()
		clock.SetNow(libtime.DateTime(time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)))

		result := pkg.BudgetExpiredResultForTest(
			nil,
			"execution",
			libtime.Duration(25*time.Minute),
			clock,
		)

		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(result.NextPhase).To(Equal("human_review"))
	})
})
