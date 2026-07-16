// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

// DeliverStartupFailure exposes the package-private helper for direct testing
// without standing up the full RunAgent pipeline (which depends on installing
// the bborbe/coding plugin into CLAUDE_CONFIG_DIR — heavy + flaky in tests).
var DeliverStartupFailure = deliverStartupFailure

// ExecutionToolsFor exposes the package-private execution-phase allowlist
// builder so tests can assert the selector-mode tool scope + anti-injection
// boundary.
var ExecutionToolsFor = executionToolsFor
