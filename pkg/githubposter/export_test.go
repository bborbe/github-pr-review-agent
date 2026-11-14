// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter

import (
	"context"
	"net/http"

	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
)

func init() {
	retryBaseDelay = 0
	retryJitterMs = 0
}

// ClassifyErrorForTest exposes classifyError for unit testing.
func ClassifyErrorForTest(
	httpStatus int,
	header http.Header,
	body string,
	err error,
) prpkg.ErrorClass {
	return classifyError(httpStatus, header, body, err)
}

// RetryCallForTest exposes retryCall so the rate-limit reset timing tests can
// drive the real backoff path without exposing it to production callers.
func RetryCallForTest[T any](
	ctx context.Context,
	call func(ctx context.Context) (T, int, http.Header, string, error),
) CallResult[T] {
	return retryCall(ctx, "test", call)
}

// EventToStateForTest exposes eventToState for unit testing.
func EventToStateForTest(event string) string {
	return eventToState(event)
}

// TruncateBodyForTest exposes truncateBody for unit testing.
func TruncateBodyForTest(b []byte) string {
	return truncateBody(b)
}
