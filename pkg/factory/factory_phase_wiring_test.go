// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"strings"
	"time"

	agentlib "github.com/bborbe/agent"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/factory"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The wrapper's own table passes whether or not the factory installs it, and
// *agentlib.Agent keeps its phase list unexported — so a factory that forgets
// one wrapper is invisible everywhere else. That is how the unread-owner defect
// reached review, and this is the composition test that closes it.
var _ = Describe("Human-review owner wiring", func() {
	const owner = "bborbe"

	// createAllPhases builds the four phases that can route a result into
	// human_review: the three review phases plus the pr-override phase.
	createAllPhases := func(configuredOwner string) []agentlib.Phase {
		env := map[string]string{}
		if configuredOwner != "" {
			env[factory.HumanReviewOwnerEnv] = configuredOwner
		}
		phases := factory.CreatePhases(
			"",
			"agent",
			"sonnet",
			"test-token",
			env,
			nil,
			"standard",
			nil,
			nil,
			nil,
			libtime.NewCurrentDateTime(),
			libtime.Duration(25*time.Minute),
			prpkg.DefaultReviewChunkConfig(),
			libtime.Duration(0),
			nil,
		)
		return append(phases, factory.CreateOverridePhase(
			"test-token",
			nil,
			"bot",
			factory.ResolveHumanReviewOwner(env),
		))
	}

	wrappedOwner := func(phase agentlib.Phase) (string, bool) {
		for _, step := range phase.Steps {
			if stamped, ok := prpkg.IsHumanReviewOwnerStep(step); ok {
				return stamped, true
			}
		}
		return "", false
	}

	It("wraps every phase body with the configured owner", func() {
		phases := createAllPhases(owner)
		Expect(phases).To(HaveLen(4), "planning, execution, ai_review, override")

		for _, phase := range phases {
			stamped, ok := wrappedOwner(phase)
			Expect(ok).To(
				BeTrue(),
				"phase %q: no human-review owner wrapper — a result routed to "+
					"human_review from this phase would keep the agent's assignee and "+
					"end up owned by nobody",
				phase.Name,
			)
			Expect(stamped).To(Equal(owner), "phase %q", phase.Name)
		}
	})

	It("leaves every phase unwrapped when no owner is configured", func() {
		for _, phase := range createAllPhases("") {
			_, ok := wrappedOwner(phase)
			Expect(ok).To(
				BeFalse(),
				"phase %q is wrapped although no owner is configured",
				phase.Name,
			)
		}
	})

	// The owner is written verbatim as a YAML assignee and consumed cross-repo,
	// so interior whitespace, embedded newlines and YAML metacharacters must not
	// survive; an over-long value would be stamped and then never match the
	// controller's HUMAN_ASSIGNEES set.
	DescribeTable("rejects a malformed owner",
		func(value string) {
			Expect(factory.ResolveHumanReviewOwner(map[string]string{
				factory.HumanReviewOwnerEnv: value,
			})).To(BeEmpty())
		},
		Entry("empty", ""),
		Entry("whitespace only", "   "),
		Entry("interior space", "bb orbe"),
		Entry("interior newline", "bb\norbe"),
		Entry("yaml colon", "bborbe:"),
		Entry("yaml comment", "#bborbe"),
		Entry("bot suffix", "bborbe[bot]"),
		Entry("leading hyphen", "-bborbe"),
		Entry("trailing hyphen", "bborbe-"),
		Entry("longer than a github login", strings.Repeat("a", 40)),
	)

	// Leading and trailing whitespace is trimmed, not rejected — a trailing
	// newline from a heredoc is harmless once trimmed.
	DescribeTable("trims a usable owner",
		func(value string) {
			Expect(factory.ResolveHumanReviewOwner(map[string]string{
				factory.HumanReviewOwnerEnv: value,
			})).To(Equal(owner))
		},
		Entry("padded", "  bborbe  "),
		Entry("trailing newline", "bborbe\n"),
	)
})
