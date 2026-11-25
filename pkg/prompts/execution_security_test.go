// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/pkg/prompts"
	"github.com/bborbe/github-pr-review-agent/pkg/security"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BuildExecutionInstructions security section", func() {
	const plugin = "---\ndescription: Test plugin\n---\n# PR Review\n\nProcedure body.\n"

	var (
		ctx    context.Context
		tmpDir string
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		tmpDir, err = os.MkdirTemp("", "prompts-security-*")
		Expect(err).NotTo(HaveOccurred())
		cmdDir := filepath.Join(tmpDir, "plugins", "marketplaces", "coding", "commands")
		Expect(os.MkdirAll(cmdDir, 0750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(cmdDir, "pr-review.md"), []byte(plugin), 0600)).
			To(Succeed())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	build := func(inputs ...prompts.SecurityInput) string {
		instructions, err := prompts.BuildExecutionInstructions(
			ctx,
			claudelib.ClaudeConfigDir(tmpDir),
			"selector",
			"main",
			true,
			`{"findings":[]}`,
			"",
			libtime.Duration(25*time.Minute),
			inputs...,
		)
		Expect(err).NotTo(HaveOccurred())
		return instructions[0].Content
	}

	It("marks security off when no input is supplied", func() {
		workflow := build()

		Expect(workflow).To(ContainSubstring("security**: off"))
		Expect(workflow).NotTo(ContainSubstring("Dependency scan (security Step 7)"))
	})

	It("marks security on and states the scan is clean when there are no findings", func() {
		workflow := build(prompts.SecurityInput{
			Status:       security.StatusScanned,
			FindingsJSON: "[]",
			Modules:      7,
		})

		Expect(workflow).To(ContainSubstring("security**: ON"))
		Expect(workflow).To(ContainSubstring("Dependency scan (security Step 7)"))
		Expect(workflow).To(ContainSubstring("no vulnerable dependencies"))
		Expect(workflow).To(ContainSubstring("do NOT emit a toolchain finding"))
	})

	It("reports a non-Go repo as not applicable and explicitly not blocking", func() {
		workflow := build(prompts.SecurityInput{
			Status:         security.StatusNotApplicable,
			Detail:         "dependency scan: not applicable (no go.mod)",
			RuleSetVersion: "0.65.7",
		})

		Expect(workflow).To(ContainSubstring("no `go.mod`"))
		Expect(workflow).To(ContainSubstring("dependency scan: not applicable (no go.mod)"))
		Expect(workflow).To(ContainSubstring("NOT a blocking finding"))
	})

	// A review of a repo with no go.mod must still carry a security verdict:
	// otherwise most bborbe PRs (nuke is YAML-only) ship with no verdict at all,
	// which is what SC1 measures.
	It("requires the verdict line even when the scan does not apply", func() {
		workflow := build(prompts.SecurityInput{
			Status:         security.StatusNotApplicable,
			Detail:         "dependency scan: not applicable (no go.mod)",
			RuleSetVersion: "0.65.7",
		})

		Expect(workflow).To(ContainSubstring("0 security findings — rule set 0.65.7"))
		Expect(workflow).To(ContainSubstring("every review carries a verdict"))
	})

	It("keeps a failed scan fail-closed and blocking", func() {
		workflow := build(prompts.SecurityInput{
			Status: security.StatusFailed,
			Detail: "osv request failed: connection refused",
		})

		Expect(workflow).To(ContainSubstring("COULD NOT RUN"))
		Expect(workflow).To(ContainSubstring("connection refused"))
		Expect(workflow).To(ContainSubstring("blocking `kind: toolchain` finding"))
		Expect(workflow).To(ContainSubstring("fail-closed"))
	})

	It("injects vulnerable dependencies as toolchain findings", func() {
		workflow := build(prompts.SecurityInput{
			Status:       security.StatusScanned,
			FindingsJSON: `[{"tool":"osv","package":"github.com/a/b","advisory":"GO-2024-0001"}]`,
			Modules:      12,
		})

		Expect(workflow).To(ContainSubstring("12 Go module(s)"))
		Expect(workflow).To(ContainSubstring("GO-2024-0001"))
		Expect(workflow).To(ContainSubstring("kind: toolchain"))
	})

	It("requires the rule-set version on the zero-findings line", func() {
		workflow := build(prompts.SecurityInput{
			Status:         security.StatusScanned,
			FindingsJSON:   "[]",
			RuleSetVersion: "0.65.7",
		})

		Expect(workflow).To(ContainSubstring("0 security findings — rule set 0.65.7"))
		Expect(workflow).To(ContainSubstring("without the version does not count"))
	})

	It("requires provenance on every security finding", func() {
		workflow := build(prompts.SecurityInput{
			Status:         security.StatusScanned,
			FindingsJSON:   "[]",
			RuleSetVersion: "0.65.7",
		})

		Expect(workflow).To(ContainSubstring("Provenance is mandatory"))
		Expect(workflow).To(ContainSubstring("kind: rule"))
		Expect(workflow).To(ContainSubstring("rule_id"))
		Expect(workflow).To(ContainSubstring("invariant_id"))
		Expect(workflow).To(ContainSubstring("must NOT be emitted"))
	})

	It("treats a null findings payload as a clean scan, not as vulnerable dependencies", func() {
		workflow := build(prompts.SecurityInput{
			Status:       security.StatusScanned,
			FindingsJSON: "null",
			Modules:      0,
		})

		Expect(workflow).To(ContainSubstring("no vulnerable dependencies"))
		Expect(workflow).NotTo(ContainSubstring("found vulnerable dependencies"))
	})

	It("says findings were suppressed when citation validation dropped any", func() {
		workflow := build(prompts.SecurityInput{
			Status:       security.StatusScanned,
			FindingsJSON: "[]",
			DroppedCount: 2,
		})

		Expect(workflow).To(ContainSubstring("dropped **2** finding(s)"))
		Expect(workflow).To(ContainSubstring("suppressed"))
	})
})
