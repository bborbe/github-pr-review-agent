// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

// HumanReviewOwnerEnv names the environment variable carrying the human owner
// this agent stamps onto every record it routes into human_review.
const HumanReviewOwnerEnv = "HUMAN_REVIEW_OWNER"

// ResolveHumanReviewOwner returns env[HumanReviewOwnerEnv], or the empty string
// when it is unset.
//
// There is deliberately no fallback name: the owner is a person, and hard-coding
// one here would make the deployment's behaviour depend on a name written into
// this repository rather than on its configuration. Empty disables the stamp, so
// an unconfigured agent behaves exactly as before this change — the record is
// routed to human_review and the controller's chokepoint clears the agent's
// assignee, leaving it unowned. main logs a warning in that case, so the
// misconfiguration is visible at startup rather than inferred later from records
// that lost their owner.
func ResolveHumanReviewOwner(env map[string]string) string {
	return env[HumanReviewOwnerEnv]
}
