// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

// DeliverStartupFailure exposes the package-private helper for direct testing
// without standing up the full RunAgent pipeline (which depends on installing
// the bborbe/coding plugin into CLAUDE_CONFIG_DIR — heavy + flaky in tests).
var DeliverStartupFailure = deliverStartupFailure

// ExecutionTools exposes the package-private execution-phase allowlist so
// tests can assert the selector-mode tool scope, the anti-injection boundary,
// and that the ast-grep runner is NOT granted (the agent runs the funnel
// itself in Go — see pkg.FunnelRunner).
var ExecutionTools = executionTools

// ReviewTools exposes the package-private ai_review-phase allowlist so tests
// can assert the gh shell-out entries are gone (spec 003 AC 1).
var ReviewTools = reviewTools

// CreatePhases exposes the three review phases CreateAgent composes, and
// CreateOverridePhase the pr-override phase, so a composition test can assert
// every phase body carries the human-review owner wrapper. *agentlib.Agent
// keeps its phase list unexported, so the assembled agent cannot be inspected;
// without this seam a factory that forgot one wrapper stays invisible — which
// is exactly how the unread-HUMAN_REVIEW_OWNER defect reached review.
var (
	CreatePhases        = createPhases
	CreateOverridePhase = createOverridePhase
)
