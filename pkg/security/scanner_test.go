// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package security_test

import (
	"context"
	stderrors "errors"
	"io/fs"

	"github.com/bborbe/github-pr-review-agent/pkg/security"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeClient struct {
	findings []security.Finding
	err      error
	got      []security.ModuleVersion
}

func (f *fakeClient) Findings(
	_ context.Context,
	modules []security.ModuleVersion,
) ([]security.Finding, error) {
	f.got = modules
	return f.findings, f.err
}

var _ = Describe("ParseGoMod", func() {
	It("reads a require block and a single-line require", func() {
		modules := security.ParseGoMod(`module example.com/x

go 1.27.4

require (
	github.com/a/b v1.2.3
	github.com/c/d v0.4.5 // indirect
)

require github.com/e/f v2.0.0+incompatible
`)
		Expect(modules).To(Equal([]security.ModuleVersion{
			{Path: "github.com/a/b", Version: "v1.2.3"},
			{Path: "github.com/c/d", Version: "v0.4.5"},
			{Path: "github.com/e/f", Version: "v2.0.0+incompatible"},
		}))
	})

	It("skips replace and exclude directives", func() {
		modules := security.ParseGoMod(`module example.com/x

require github.com/a/b v1.2.3

replace github.com/a/b => ../local

exclude github.com/c/d v0.1.0
`)
		Expect(modules).To(Equal([]security.ModuleVersion{
			{Path: "github.com/a/b", Version: "v1.2.3"},
		}))
	})

	It("collapses duplicate modules", func() {
		modules := security.ParseGoMod(`module example.com/x

require github.com/a/b v1.2.3

require github.com/a/b v1.9.9
`)
		Expect(modules).To(HaveLen(1))
		Expect(modules[0].Version).To(Equal("v1.2.3"))
	})
})

var _ = Describe("Scanner", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("reports not-applicable when the worktree has no go.mod", func() {
		client := &fakeClient{}
		scanner := security.NewScannerWithClient(client, func(string) (string, error) {
			return "", fs.ErrNotExist
		})

		result := scanner.Scan(ctx, "/worktree")

		Expect(result.Status).To(Equal(security.StatusNotApplicable))
		Expect(result.Detail).To(ContainSubstring("not applicable"))
		Expect(result.Findings).To(BeEmpty())
		Expect(client.got).To(BeNil())
	})

	It("returns the client's findings for a scanned module set", func() {
		client := &fakeClient{findings: []security.Finding{{
			Tool:     "osv",
			Package:  "github.com/a/b",
			Version:  "v1.2.3",
			Advisory: "GO-2024-0001",
			Message:  "a summary",
		}}}
		scanner := security.NewScannerWithClient(client, func(string) (string, error) {
			return "module example.com/x\n\nrequire github.com/a/b v1.2.3\n", nil
		})

		result := scanner.Scan(ctx, "/worktree")

		Expect(result.Status).To(Equal(security.StatusScanned))
		Expect(result.Modules).To(Equal(1))
		Expect(result.Findings).To(HaveLen(1))
		Expect(result.Findings[0].Advisory).To(Equal("GO-2024-0001"))
		Expect(client.got).To(Equal([]security.ModuleVersion{
			{Path: "github.com/a/b", Version: "v1.2.3"},
		}))
	})

	It("reports failed — never not-applicable — when go.mod exists but cannot be read", func() {
		client := &fakeClient{}
		scanner := security.NewScannerWithClient(client, func(string) (string, error) {
			return "", stderrors.New("permission denied")
		})

		result := scanner.Scan(ctx, "/worktree")

		Expect(result.Status).To(Equal(security.StatusFailed))
		Expect(result.Detail).To(ContainSubstring("permission denied"))
	})

	It("reports failed when the client errors", func() {
		client := &fakeClient{err: stderrors.New("osv unreachable")}
		scanner := security.NewScannerWithClient(client, func(string) (string, error) {
			return "module example.com/x\n\nrequire github.com/a/b v1.2.3\n", nil
		})

		result := scanner.Scan(ctx, "/worktree")

		Expect(result.Status).To(Equal(security.StatusFailed))
		Expect(result.Detail).To(ContainSubstring("osv unreachable"))
	})

	It("reports scanned with zero findings when go.mod declares no requirements", func() {
		client := &fakeClient{}
		scanner := security.NewScannerWithClient(client, func(string) (string, error) {
			return "module example.com/x\n\ngo 1.27.4\n", nil
		})

		result := scanner.Scan(ctx, "/worktree")

		Expect(result.Status).To(Equal(security.StatusScanned))
		Expect(result.Modules).To(Equal(0))
		Expect(result.Findings).To(BeEmpty())
	})
})
