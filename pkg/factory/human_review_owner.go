// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"regexp"
	"strings"
)

// HumanReviewOwnerEnv names the environment variable carrying the human owner
// this agent stamps onto every record it routes into human_review.
const HumanReviewOwnerEnv = "HUMAN_REVIEW_OWNER"

// humanLoginPattern matches a GitHub user or organisation login: alphanumerics
// with interior hyphens, no leading or trailing hyphen.
//
// The owner is written verbatim into task frontmatter and consumed cross-repo as
// an assignee, so an unvalidated deploy value could corrupt the YAML the
// controller parses. A `[bot]` suffix is deliberately rejected — a bot login
// here would re-create the unowned-record defect this stamp exists to fix.
var humanLoginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)

// maxGitHubLoginLength bounds the value at GitHub's own limit. An over-long
// login would pass the charset test, be stamped as the assignee, and then never
// match the controller's HUMAN_ASSIGNEES set — reproducing the unowned-record
// state this stamp exists to fix, with no error anywhere.
const maxGitHubLoginLength = 39

// ResolveHumanReviewOwner returns the trimmed env[HumanReviewOwnerEnv], or the
// empty string when it is unset or is not a valid GitHub login.
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
	owner := strings.TrimSpace(env[HumanReviewOwnerEnv])
	if !IsHumanReviewOwnerValid(owner) {
		return ""
	}
	return owner
}

// IsHumanReviewOwnerValid reports whether value, trimmed, is a usable owner
// login. main uses it to distinguish "unset" from "set but malformed" in its
// startup warning — the two need different operator actions, and a malformed
// value disables the stamp exactly as an unset one does.
func IsHumanReviewOwnerValid(value string) bool {
	trimmed := strings.TrimSpace(value)
	return len(trimmed) <= maxGitHubLoginLength && humanLoginPattern.MatchString(trimmed)
}
