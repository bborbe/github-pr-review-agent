// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	agentlib "github.com/bborbe/agent"
	"github.com/golang/glog"
)

// PRStateClient queries GitHub for the live state of a pull request.
// Satisfied by pkg/github.Client (the gh CLI wrapper). Defined here as a
// narrow interface — not a reference to pkg/github — because pkg/github
// already imports pkg (for Verdict), so the steps in pkg cannot import
// the concrete client without an import cycle.
//
//counterfeiter:generate -o ../mocks/pr-state-client.go --fake-name PRStateClient . PRStateClient
type PRStateClient interface {
	PRState(ctx context.Context, prURL string) (state, mergedAt, headRefOid string, err error)
}

// ResolutionOutput is the typed contract for the `## Resolution` JSON
// section the prStateCheck helper appends when the review is moot.
// Round-trips with agentlib.MarshalSectionTyped + ExtractSection.
//
// Three verdicts are valid:
//   - merged          — PR state == MERGED; MergedAt populated
//   - closed_unmerged — PR state == CLOSED (no merge)
//   - superseded      — PR state == OPEN but the reviewed ref is no
//     longer PR HEAD; HeadRefOid populated
type ResolutionOutput struct {
	Verdict    string `json:"verdict"`
	PRState    string `json:"pr_state"`
	MergedAt   string `json:"merged_at,omitempty"`
	HeadRefOid string `json:"head_ref_oid,omitempty"`
}

// Verdict values for ResolutionOutput.Verdict. Exhaustive: every
// resolution written by the prStateCheck helper carries one of these.
const (
	// ResolutionVerdictMerged is the verdict when the PR is already
	// merged — the review is moot, the task closes completed.
	ResolutionVerdictMerged = "merged"
	// ResolutionVerdictClosedUnmerged is the verdict when the PR was
	// closed without a merge — review moot, task closes completed.
	ResolutionVerdictClosedUnmerged = "closed_unmerged"
	// ResolutionVerdictSuperseded is the verdict when the PR is still
	// open but HEAD has moved past the reviewed ref — the old review
	// is obsolete, the task closes aborted.
	ResolutionVerdictSuperseded = "superseded"
)

// prStateCheck consults the live GitHub PR state and, when the review is
// moot, writes the terminal verdict (`## Resolution` + frontmatter
// status/phase) and returns the Result to short-circuit with. Returns nil
// when the PR is still reviewable (OPEN at the reviewed ref) — the caller
// proceeds with its normal phase work. A nil client means "no check"
// (mirrors the nil-poster = skip-posting pattern) — tests that don't
// exercise PR state pass nil and phase work runs unchanged.
//
// Routing (ground truth = gh pr view):
//   - MERGED  → completed + Resolution(verdict=merged)
//   - CLOSED  → completed + Resolution(verdict=closed_unmerged)
//   - OPEN with task.ref != headRefOid → aborted + Resolution(verdict=superseded)
//   - OPEN with task.ref == headRefOid → nil (review still meaningful)
//
// On gh failure the check is a no-op (proceed) — a transient gh error
// must not block or distort phase work; the task still routes normally
// and the operator-side closer remains the backstop.
func prStateCheck(
	ctx context.Context,
	md *agentlib.Markdown,
	client PRStateClient,
) (*agentlib.Result, error) {
	// 0. Nil client = check disabled (tests without PR-state concern).
	if client == nil {
		return nil, nil
	}
	// 1. Idempotency guard — first statement. When the task is already
	// terminal (status completed/aborted) or already carries a
	// ## Resolution, do nothing. Mirrors the existing "## Verdict already
	// present" short-circuit in steps_review.go.
	if status, _ := md.Frontmatter.String("status"); status == "completed" || status == "aborted" {
		glog.V(2).Infof("pr-state-check: task already terminal status=%s — skipping", status)
		return nil, nil
	}
	if _, exists := md.FindSection("## Resolution"); exists {
		glog.V(2).Infof("pr-state-check: ## Resolution already present — skipping")
		return nil, nil
	}

	// 2. Extract the PR URL; without one the check cannot run.
	prURL := ExtractPRURL(md)
	if prURL == "" {
		glog.V(2).Infof("pr-state-check: no GitHub PR URL in task — skipping")
		return nil, nil
	}

	// 3. Ask GitHub for the live state.
	state, mergedAt, headRefOid, err := client.PRState(ctx, prURL)
	if err != nil {
		glog.V(2).Infof("pr-state-check: PR=%s check failed err=%v — proceeding", prURL, err)
		return nil, nil
	}

	// 4. Route by state.
	ref, _ := md.Frontmatter.String("ref")
	switch {
	case state == "MERGED":
		return closeMoot(ctx, md, ResolutionVerdictMerged, "completed", state, mergedAt, headRefOid)
	case state == "CLOSED":
		return closeMoot(
			ctx,
			md,
			ResolutionVerdictClosedUnmerged,
			"completed",
			state,
			mergedAt,
			headRefOid,
		)
	case state == "OPEN" && ref != headRefOid:
		return closeMoot(
			ctx,
			md,
			ResolutionVerdictSuperseded,
			"aborted",
			state,
			mergedAt,
			headRefOid,
		)
	default:
		// OPEN at the reviewed ref — the review may still matter.
		glog.V(2).Infof(
			"pr-state-check: PR=%s state=%s ref=%s head=%s — review still meaningful",
			prURL, state, ref, headRefOid,
		)
		return nil, nil
	}
}

// closeMoot appends the ## Resolution section, rewrites the frontmatter
// to the terminal state, and returns the short-circuit Result. merged /
// closed_unmerged close as completed; superseded closes as aborted —
// both land at phase: done so the controller stops re-triggering.
func closeMoot(
	ctx context.Context,
	md *agentlib.Markdown,
	verdict string,
	status string,
	prState string,
	mergedAt string,
	headRefOid string,
) (*agentlib.Result, error) {
	section, marshalErr := agentlib.MarshalSectionTyped(
		ctx,
		"## Resolution",
		ResolutionOutput{
			Verdict:    verdict,
			PRState:    prState,
			MergedAt:   mergedAt,
			HeadRefOid: headRefOid,
		},
	)
	if marshalErr != nil {
		// Marshalling our own typed struct should not fail; if it does,
		// log and bail — the check is best-effort and must not distort
		// the verdict on its own failure.
		glog.V(2).Infof("pr-state-check: marshal ## Resolution failed err=%v", marshalErr)
		return nil, nil
	}
	md.ReplaceSection(section)
	md.Frontmatter["status"] = status
	md.Frontmatter["phase"] = "done"

	glog.V(2).Infof(
		"pr-state-check: verdict=%s pr_state=%s merged_at=%s head_ref_oid=%s",
		verdict,
		prState,
		mergedAt,
		headRefOid,
	)

	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: "done",
	}, nil
}
