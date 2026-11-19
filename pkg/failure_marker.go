// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	agentlib "github.com/bborbe/agent"
	"github.com/golang/glog"

	prurl "github.com/bborbe/maintainer/prurl"
)

// postFailureMarker posts a best-effort COMMENT naming a run that ended without
// posting a verdict. Shared by every phase step that can terminate on an expired
// soft budget, so the planning, execution and review phases all leave the same
// PR-visible marker.
//
// Every failure mode is swallowed: a nil poster, an unparseable or non-GitHub
// URL, and a failed marker POST all leave the caller's routing untouched. The
// returned PostResult is discarded and no ## Diagnostics entry is appended, so a
// marker can never change the run's Status, NextPhase, Message or run_outcome.
// resolvePRInfo is deliberately NOT used here: it appends failure diagnostics and
// returns an early result, and propagating either would change the routing.
func postFailureMarker(
	ctx context.Context,
	poster PrPoster,
	md *agentlib.Markdown,
	prURLStr, outcome, reason string,
) {
	if poster == nil {
		return
	}
	prInfo, err := prurl.ParsePRURL(ctx, prURLStr)
	if err != nil {
		glog.V(2).Infof("failure marker skipped: cannot parse PR URL %q: %v", prURLStr, err)
		return
	}
	if prInfo.Platform != prurl.PlatformGitHub {
		return
	}
	ref, _ := md.Frontmatter.String("ref")
	runID, _ := md.Frontmatter.String("task_identifier")
	result := poster.PostFailureMarker(ctx, *prInfo, ref, outcome, reason, runID)
	if result.Outcome != "success" {
		glog.V(2).Infof(
			"failure marker post unsuccessful outcome=%s step=%s http_status=%d",
			result.Outcome,
			result.FailureStep,
			result.HTTPStatus,
		)
	}
}
