// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package github

// BuildSupersededNoticeBodyForTest exposes buildSupersededNoticeBody so the
// table tests in github_test can assert the bounded body contract without
// exposing it to production callers.
func BuildSupersededNoticeBodyForTest(ref, headRefOid, runID string) string {
	return buildSupersededNoticeBody(ref, headRefOid, runID)
}
