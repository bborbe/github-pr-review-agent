// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"time"

	libargument "github.com/bborbe/argument/v2"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// White-box (package main) specs for the REVIEW_MAX_DURATION argument/v2
// resolution on the real application struct — the struct-tag → typed-field
// boundary. They run under the Ginkgo suite bootstrapped in main_test.go
// (same pattern as auth_resolve_test.go): Ginkgo v2.32.1 rejects a second
// RunSpecs call in one binary (RerunningSuite → os.Exit(1)), so no suite is
// registered here.
var _ = Describe("review max duration resolution", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("resolves to 1500s (25m) when unset", func() {
		defaults, err := libargument.DefaultValues(ctx, &application{})
		Expect(err).NotTo(HaveOccurred())
		app := &application{}
		err = libargument.Fill(ctx, app, defaults)
		Expect(err).NotTo(HaveOccurred())
		Expect(app.MaxReviewDuration.Duration()).To(Equal(1500 * time.Second))
	})

	It("equals the parsed value when set", func() {
		app := &application{}
		err := libargument.ParseEnv(ctx, app, []string{"REVIEW_MAX_DURATION=30m"})
		Expect(err).NotTo(HaveOccurred())
		Expect(app.MaxReviewDuration.Duration()).To(Equal(1800 * time.Second))
	})

	It("fails startup on an unparseable value", func() {
		app := &application{}
		err := libargument.ParseEnv(ctx, app, []string{"REVIEW_MAX_DURATION=abc"})
		Expect(err).To(HaveOccurred())
	})

	It("parses a below-floor value but fails validation at startup", func() {
		app := &application{}
		err := libargument.ParseEnv(ctx, app, []string{"REVIEW_MAX_DURATION=5s"})
		Expect(err).NotTo(HaveOccurred())
		Expect(app.MaxReviewDuration.Duration()).To(Equal(5 * time.Second))
		err = prpkg.ValidateReviewMaxDuration(ctx, app.MaxReviewDuration)
		Expect(err).To(HaveOccurred())
	})
})

// White-box (package main) specs for the REVIEW_CHUNK_* argument/v2 resolution
// on the real application struct — the struct-tag → typed-field boundary. They
// assert the default:"..." tags themselves, not DefaultReviewChunkConfig.
var _ = Describe("review chunk env knobs", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	validate := func(app *application) error {
		return prpkg.ValidateReviewChunkConfig(ctx, prpkg.ReviewChunkConfig{
			EngageAdditions: app.ReviewChunkEngageAdditions,
			MaxAdditions:    app.ReviewChunkMaxAdditions,
			MaxFiles:        app.ReviewChunkMaxFiles,
		})
	}

	It("resolves the three defaults when unset", func() {
		defaults, err := libargument.DefaultValues(ctx, &application{})
		Expect(err).NotTo(HaveOccurred())
		app := &application{}
		Expect(libargument.Fill(ctx, app, defaults)).To(Succeed())
		Expect(app.ReviewChunkEngageAdditions).To(Equal(500))
		Expect(app.ReviewChunkMaxAdditions).To(Equal(300))
		Expect(app.ReviewChunkMaxFiles).To(Equal(15))
		Expect(validate(app)).To(Succeed())
	})

	DescribeTable("a zero value parses but fails startup validation naming its env var",
		func(envVar string, read func(app *application) int) {
			// Seed the defaults first so only the row's variable is zeroed;
			// ParseEnv on a bare struct would leave the other two at 0 and the
			// validator would reject a different field.
			defaults, err := libargument.DefaultValues(ctx, &application{})
			Expect(err).NotTo(HaveOccurred())
			app := &application{}
			Expect(libargument.Fill(ctx, app, defaults)).To(Succeed())
			Expect(libargument.ParseEnv(ctx, app, []string{envVar + "=0"})).To(Succeed())
			Expect(read(app)).To(Equal(0))
			err = validate(app)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(envVar))
		},
		Entry("engage additions",
			"REVIEW_CHUNK_ENGAGE_ADDITIONS",
			func(app *application) int { return app.ReviewChunkEngageAdditions }),
		Entry("max additions",
			"REVIEW_CHUNK_MAX_ADDITIONS",
			func(app *application) int { return app.ReviewChunkMaxAdditions }),
		Entry("max files",
			"REVIEW_CHUNK_MAX_FILES",
			func(app *application) int { return app.ReviewChunkMaxFiles }),
	)
})
