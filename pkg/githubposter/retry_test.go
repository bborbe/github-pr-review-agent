// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubposter_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/githubposter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// loadFixture reads a testdata fixture verbatim. Plain os.ReadFile keeps the
// fixture readable as data rather than embedding it into the binary.
func loadFixture(name string) string {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}

// loadRateLimitHeader builds an http.Header from the fixture's `key: value`
// lines using Header.Set, so the canonical lookup key matches what resp.Header
// actually produces.
func loadRateLimitHeader() http.Header {
	header := http.Header{}
	for _, line := range strings.Split(strings.TrimSpace(loadFixture("ratelimit-403.headers")), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		header.Set(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	return header
}

var _ = Describe("classifyError", func() {
	DescribeTable(
		"classifies errors",
		func(httpStatus int, header http.Header, body string, err error, want pkg.ErrorClass) {
			Expect(
				githubposter.ClassifyErrorForTest(httpStatus, header, body, err),
			).To(Equal(want))
		},
		Entry("HTTP 200 → NotAFailure", 200, nil, "", nil, pkg.ErrorClassNotAFailure),
		Entry("HTTP 429 → Transient", 429, nil, "", nil, pkg.ErrorClassTransient),
		Entry("HTTP 500 → Transient", 500, nil, "", nil, pkg.ErrorClassTransient),
		Entry("HTTP 503 → Transient", 503, nil, "", nil, pkg.ErrorClassTransient),
		Entry("HTTP 401 → NotTransient", 401, nil, "", nil, pkg.ErrorClassPermanent),
		Entry("HTTP 403 → NotTransient", 403, nil, "", nil, pkg.ErrorClassPermanent),
		Entry("HTTP 404 → NotTransient", 404, nil, "", nil, pkg.ErrorClassPermanent),
		Entry("HTTP 422 → NotTransient", 422, nil, "", nil, pkg.ErrorClassPermanent),
		Entry(
			"status 0 with timeout error → Transient",
			0,
			nil,
			"",
			&net.DNSError{IsTimeout: true},
			pkg.ErrorClassTransient,
		),
		Entry(
			"status 0 with generic error → Transient",
			0,
			nil,
			"",
			errors.New("some error"),
			pkg.ErrorClassTransient,
		),
		Entry(
			"generic error with status 200 → Unknown",
			200,
			nil,
			"",
			errors.New("some error"),
			pkg.ErrorClassUnknown,
		),
		Entry(
			"generic error with status 503 → Transient",
			503,
			nil,
			"",
			errors.New("some error"),
			pkg.ErrorClassTransient,
		),
	)

	It("classifies a rate-limited 403 fixture with remaining=0 as transient", func() {
		Expect(githubposter.ClassifyErrorForTest(
			403, loadRateLimitHeader(), loadFixture("ratelimit-403.body"), nil,
		)).To(Equal(pkg.ErrorClassTransient))
	})

	It("classifies a rate-limited 403 fixture by body alone as transient", func() {
		Expect(githubposter.ClassifyErrorForTest(
			403, nil, loadFixture("ratelimit-403.body"), nil,
		)).To(Equal(pkg.ErrorClassTransient))
	})

	It("classifies a plain 403 without rate-limit signals as permanent", func() {
		Expect(githubposter.ClassifyErrorForTest(
			403, nil, `{"message":"Bad credentials"}`, nil,
		)).To(Equal(pkg.ErrorClassPermanent))
	})

	It("classifies a 429 without rate-limit signals as transient", func() {
		Expect(githubposter.ClassifyErrorForTest(
			429, nil, "", nil,
		)).To(Equal(pkg.ErrorClassTransient))
	})
})

var _ = Describe("retryCall rate-limit handling", func() {
	It("waits until X-RateLimit-Reset before retrying", func() {
		resetSeconds := time.Now().Add(2 * time.Second).Unix()
		header := http.Header{}
		header.Set("X-RateLimit-Reset", strconv.FormatInt(resetSeconds, 10))

		calls := 0
		start := time.Now()
		result := githubposter.RetryCallForTest(
			context.Background(),
			func(ctx context.Context) (bool, int, http.Header, string, error) {
				calls++
				if calls == 1 {
					return false, 403, header, "rate limit", errors.New("rate limit")
				}
				return true, 200, http.Header{}, "", nil
			},
		)
		elapsed := time.Since(start)
		resetTime := time.Unix(resetSeconds, 0)

		Expect(result.Attempts).To(Equal(2))
		Expect(calls).To(Equal(2))
		Expect(elapsed).To(BeNumerically(">=", resetTime.Sub(start)))
	})

	It("returns transient with one attempt when the reset is beyond the deadline", func() {
		header := http.Header{}
		header.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))

		calls := 0
		deadlineCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		result := githubposter.RetryCallForTest(
			deadlineCtx,
			func(ctx context.Context) (bool, int, http.Header, string, error) {
				calls++
				return false, 403, header, "rate limit", errors.New("rate limit")
			},
		)

		Expect(result.Class).To(Equal(pkg.ErrorClassTransient))
		Expect(result.Attempts).To(Equal(1))
		Expect(calls).To(Equal(1))
	})
})
