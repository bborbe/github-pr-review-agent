// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter

import (
	"context"
	stderrors "errors"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	errors "github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
)

// errPhantomPOST is the sentinel returned by verify-GET closures when POST returned 200
// but the review is absent in the subsequent listing. Classified transient so retryCall retries.
var errPhantomPOST = stderrors.New("phantom-POST sentinel")

// retryBaseDelay is the wait before a retry attempt. Overridden to 0 in tests via export_test.go.
var retryBaseDelay = 5 * time.Second

// retryJitterMs is the maximum random jitter in milliseconds added to the base delay.
var retryJitterMs = 1000

// CallResult carries the outcome of a single retryCall invocation.
type CallResult[T any] struct {
	Value          T
	HTTPStatus     int
	ResponseHeader http.Header
	ResponseBody   string
	Err            error
	Attempts       int
	Class          prpkg.ErrorClass
}

// classifyError maps (httpStatus, header, body, err) to an ErrorClass.
// The phantom-POST sentinel is treated as transient so retryCall retries the verify-GET.
// A 403/429 carrying a rate-limit signal is transient: the reset tells retryCall when
// to retry, whereas a plain 403 (bad token, missing permission) stays permanent.
func classifyError(httpStatus int, header http.Header, body string, err error) prpkg.ErrorClass {
	if errors.Is(err, errPhantomPOST) {
		return prpkg.ErrorClassTransient
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return prpkg.ErrorClassTransient
	}
	var ne net.Error
	if err != nil && stderrors.As(err, &ne) {
		return prpkg.ErrorClassTransient
	}
	if httpStatus == 0 {
		return prpkg.ErrorClassTransient
	}
	if httpStatus >= 500 || httpStatus == 429 {
		return prpkg.ErrorClassTransient
	}
	if isRateLimited(httpStatus, header, body) {
		return prpkg.ErrorClassTransient
	}
	if httpStatus == 401 || httpStatus == 403 || httpStatus == 404 || httpStatus == 422 {
		return prpkg.ErrorClassPermanent
	}
	if err != nil {
		return prpkg.ErrorClassUnknown
	}
	return prpkg.ErrorClassNotAFailure
}

// isRateLimited reports whether a 403/429 response is a GitHub rate limit rather
// than a credential or permission failure. Either an exhausted remaining counter
// or a rate-limit phrase in the body qualifies. 401/404/422 are never rate limits.
func isRateLimited(status int, header http.Header, body string) bool {
	if status != 403 && status != 429 {
		return false
	}
	if header.Get("X-RateLimit-Remaining") == "0" {
		return true
	}
	return strings.Contains(strings.ToLower(body), "rate limit")
}

// rateLimitResetWait returns the duration until the X-RateLimit-Reset instant.
// An absent, empty, or unparsable header yields zero so the caller falls back to
// the fixed base delay. A reset in the past also yields zero.
func rateLimitResetWait(header http.Header) time.Duration {
	raw := header.Get("X-RateLimit-Reset")
	if raw == "" {
		return 0
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	// Direct clock read is deliberate: retryCall is a free generic function with
	// no constructor to inject a clock through (matching pkg/funnel.go).
	return time.Until(time.Unix(seconds, 0))
}

// retryCall executes call at most twice: once immediately, and once after a short
// backoff if the first result is transient. label is kept for documentation purposes.
func retryCall[T any](
	ctx context.Context,
	label string,
	call func(ctx context.Context) (T, int, http.Header, string, error),
) CallResult[T] {
	_ = label // kept in signature for documentation; errors come from the call closure
	value, status, header, body, err := call(ctx)
	class := classifyError(status, header, body, err)
	if err == nil {
		return CallResult[T]{
			Value:          value,
			HTTPStatus:     status,
			ResponseHeader: header,
			ResponseBody:   body,
			Attempts:       1,
			Class:          class,
		}
	}
	if class != prpkg.ErrorClassTransient {
		return CallResult[T]{
			HTTPStatus:     status,
			ResponseHeader: header,
			ResponseBody:   body,
			Err:            err,
			Attempts:       1,
			Class:          class,
		}
	}

	// #nosec G404 -- jitter for retry backoff, not security-sensitive
	n := rand.Intn(retryJitterMs + 1) //nolint:gosec
	jitter := time.Duration(n) * time.Millisecond
	wait := retryBaseDelay + jitter
	// Extend the wait to the rate-limit reset when that lies further out, so the
	// retry does not burn its second attempt inside the exhausted window.
	if resetWait := rateLimitResetWait(header); resetWait > wait {
		wait = resetWait
	}
	select {
	case <-ctx.Done():
		return CallResult[T]{
			HTTPStatus:     status,
			ResponseHeader: header,
			ResponseBody:   body,
			Err:            ctx.Err(),
			Attempts:       1,
			Class:          prpkg.ErrorClassTransient,
		}
	case <-time.After(wait):
	}

	value2, status2, header2, body2, err2 := call(ctx)
	class2 := classifyError(status2, header2, body2, err2)
	return CallResult[T]{
		Value:          value2,
		HTTPStatus:     status2,
		ResponseHeader: header2,
		ResponseBody:   body2,
		Err:            err2,
		Attempts:       2,
		Class:          class2,
	}
}
