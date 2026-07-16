// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/github-pr-review-agent/pkg/prompts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BuildExecutionInstructions", func() {
	var (
		ctx        context.Context
		tmpDir     string
		cmdDir     string
		fakePlugin string
	)

	fakePlugin = "---\ndescription: Test plugin\nallowed-tools: Task\n---\n# PR Review\n\nProcedure body line 1.\nProcedure body line 2.\n"

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		tmpDir, err = os.MkdirTemp("", "prompts-test-*")
		Expect(err).NotTo(HaveOccurred())

		cmdDir = filepath.Join(tmpDir, "plugins", "marketplaces", "coding", "commands")
		Expect(os.MkdirAll(cmdDir, 0750)).To(Succeed())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	writePlugin := func(content string) {
		Expect(
			os.WriteFile(filepath.Join(cmdDir, "pr-review.md"), []byte(content), 0600),
		).To(Succeed())
	}

	Describe("happy path", func() {
		It("returns two instructions with correct content", func() {
			writePlugin(fakePlugin)

			instructions, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"main",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(instructions).To(HaveLen(2))
			Expect(instructions[0].Name).To(Equal("workflow"))
			Expect(instructions[1].Name).To(Equal("output-format"))

			workflow := instructions[0].Content
			Expect(workflow).To(ContainSubstring("TARGET_BRANCH**: main"))
			Expect(workflow).To(ContainSubstring("mode**: standard"))
			Expect(workflow).To(ContainSubstring("Procedure body line 1."))
			Expect(workflow).NotTo(ContainSubstring("description: Test plugin"))
			Expect(workflow).To(ContainSubstring("Final step — emit verdict JSON"))
			Expect(workflow).To(ContainSubstring("Severity map"))
			Expect(workflow).To(ContainSubstring("Verdict roll-up"))
		})
	})

	Describe("container-path steer (selector-mode allowlist)", func() {
		It(
			"steers the model to the literal runner + guide path, not the $RUNNER/$GUIDE shell var",
			func() {
				writePlugin(fakePlugin)

				instructions, err := prompts.BuildExecutionInstructions(
					ctx,
					claudelib.ClaudeConfigDir(tmpDir),
					"selector",
					"main",
				)
				Expect(err).NotTo(HaveOccurred())

				workflow := instructions[0].Content
				pluginRoot := filepath.Join(tmpDir, "plugins", "marketplaces", "coding")
				// Runner invoked by literal path so factory.executionTools can match it.
				Expect(workflow).To(ContainSubstring(pluginRoot + "/scripts/ast-grep-runner.sh"))
				// Guide read by literal path (Read tool), probe skipped.
				Expect(workflow).To(ContainSubstring(pluginRoot + "/docs/selector-mode-guide.md"))
				// Steer lands before the inlined procedure body.
				Expect(workflow).To(ContainSubstring("Container tool paths"))
				Expect(strings.Index(workflow, "Container tool paths")).
					To(BeNumerically("<", strings.Index(workflow, "Procedure body line 1.")))
			},
		)
	})

	Describe("frontmatter stripping", func() {
		It("strips YAML frontmatter keys and preserves body", func() {
			writePlugin(fakePlugin)

			instructions, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"main",
			)
			Expect(err).NotTo(HaveOccurred())

			workflow := instructions[0].Content
			Expect(workflow).NotTo(ContainSubstring("description:"))
			Expect(workflow).NotTo(ContainSubstring("allowed-tools:"))
			Expect(workflow).To(ContainSubstring("Procedure body line 1."))
			Expect(workflow).To(ContainSubstring("Procedure body line 2."))
		})
	})

	Describe("no frontmatter", func() {
		It("assembles body unchanged when no leading frontmatter", func() {
			noFrontmatter := "# PR Review\n\nBody without frontmatter.\n"
			writePlugin(noFrontmatter)

			instructions, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"main",
			)
			Expect(err).NotTo(HaveOccurred())

			workflow := instructions[0].Content
			Expect(workflow).To(ContainSubstring("Body without frontmatter."))
		})
	})

	Describe("plugin missing", func() {
		It("returns an error containing 'read plugin command file'", func() {
			// no file written — cmdDir exists but pr-review.md does not
			_, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"main",
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("read plugin command file"))
		})
	})

	Describe("empty baseRef", func() {
		It("returns an error containing 'base_ref'", func() {
			_, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"standard",
				"",
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("base_ref"))
		})
	})

	Describe("empty reviewMode", func() {
		It("returns an error containing 'reviewMode'", func() {
			_, err := prompts.BuildExecutionInstructions(
				ctx,
				claudelib.ClaudeConfigDir(tmpDir),
				"",
				"main",
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("reviewMode"))
		})
	})
})
