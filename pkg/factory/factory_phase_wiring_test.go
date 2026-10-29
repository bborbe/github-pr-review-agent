// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"testing"
	"time"

	agentlib "github.com/bborbe/agent"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	libtime "github.com/bborbe/time"
)

// createAllPhases builds the four phases that can route a result into
// human_review: the three review phases plus the pr-override phase.
func createAllPhases(owner string) []agentlib.Phase {
	env := map[string]string{}
	if owner != "" {
		env[HumanReviewOwnerEnv] = owner
	}
	phases := createPhases(
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
	return append(
		phases,
		createOverridePhase("test-token", nil, "bot", ResolveHumanReviewOwner(env)),
	)
}

// TestCreateAllPhasesWrapsEveryPhaseBodyWithTheHumanReviewOwner is the
// composition test the #61 review asked for. The wrapper's own table test
// passes whether or not the factory installs it, and *agentlib.Agent keeps its
// phase list unexported — so a factory that forgets one wrapper is invisible
// everywhere else. That is how the unread-owner defect reached review.
func TestCreateAllPhasesWrapsEveryPhaseBodyWithTheHumanReviewOwner(t *testing.T) {
	const owner = "bborbe"

	phases := createAllPhases(owner)

	if len(phases) != 4 {
		t.Fatalf("want 4 phases (planning, execution, ai_review, override), got %d", len(phases))
	}
	for _, phase := range phases {
		got, ok := wrappedOwner(phase)
		if !ok {
			t.Fatalf(
				"phase %q: no human-review owner wrapper — a result routed to human_review from "+
					"this phase would keep the agent's assignee and end up owned by nobody",
				phase.Name,
			)
		}
		if got != owner {
			t.Fatalf("phase %q: wrapper stamps owner %q, want %q", phase.Name, got, owner)
		}
	}
}

// TestCreateAllPhasesStaysUnwrappedWithoutAnOwner pins the documented default:
// an unset owner disables the stamp rather than inventing a name.
func TestCreateAllPhasesStaysUnwrappedWithoutAnOwner(t *testing.T) {
	for _, phase := range createAllPhases("") {
		if got, ok := wrappedOwner(phase); ok {
			t.Fatalf(
				"phase %q is wrapped with owner %q although no owner is configured",
				phase.Name,
				got,
			)
		}
	}
}

// TestResolveHumanReviewOwnerRejectsMalformedValues covers the guard that keeps
// a bad deploy value out of task frontmatter: the owner is written verbatim as
// a YAML assignee and consumed cross-repo, so interior whitespace, embedded
// newlines and YAML metacharacters must not survive the trim.
func TestResolveHumanReviewOwnerRejectsMalformedValues(t *testing.T) {
	// Leading and trailing whitespace is trimmed, not rejected — a trailing
	// newline from a heredoc is harmless once trimmed. What must not survive is
	// whitespace *inside* the value, or any YAML metacharacter.
	for _, value := range []string{
		"",
		"   ",
		"bb orbe",
		"bb\norbe",
		"bborbe:",
		"#bborbe",
		"bborbe[bot]",
		"-bborbe",
		"bborbe-",
	} {
		if got := ResolveHumanReviewOwner(map[string]string{HumanReviewOwnerEnv: value}); got != "" {
			t.Fatalf("ResolveHumanReviewOwner(%q) = %q, want empty", value, got)
		}
	}
	for _, value := range []string{"  bborbe  ", "bborbe\n"} {
		if got := ResolveHumanReviewOwner(map[string]string{HumanReviewOwnerEnv: value}); got != "bborbe" {
			t.Fatalf("ResolveHumanReviewOwner(%q) trims: got %q, want %q", value, got, "bborbe")
		}
	}
}

func wrappedOwner(phase agentlib.Phase) (string, bool) {
	for _, step := range phase.Steps {
		if owner, ok := prpkg.IsHumanReviewOwnerStep(step); ok {
			return owner, true
		}
	}
	return "", false
}
