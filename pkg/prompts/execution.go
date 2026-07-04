// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
)

//go:embed execution_output-format.md
var executionOutputFormat string

const prefilledArgsHeaderTemplate = "## Pre-filled arguments\n\n" +
	"The procedure below describes a `/coding:pr-review` slash command that takes\n" +
	"`<target-branch>` and a mode argument. Those arguments have already been\n" +
	"resolved for this run — do NOT prompt for them, do NOT re-derive them:\n\n" +
	"- **TARGET_BRANCH**: %s\n" +
	"- **mode**: %s\n\n" +
	"Now follow the procedure below as if the slash command had been invoked with\n" +
	"those arguments. The procedure references sub-agents via the `Task` tool;\n" +
	"dispatch them as written.\n\n" +
	"---\n\n"

const verdictTranslationFooter = "---\n\n" +
	"## Final step — emit verdict JSON\n\n" +
	"After Step 7 (Manual Review) completes and the consolidated report is\n" +
	"produced, ALSO emit a JSON verdict matching the agent's frozen schema (see\n" +
	"`<output-format>`).\n\n" +
	"Severity map (deterministic):\n" +
	"- Must Fix finding → comment severity \"critical\", contributes to verdict \"request-changes\"\n" +
	"- Should Fix finding → comment severity \"major\", contributes to verdict \"request-changes\"\n" +
	"- Nice to Have finding → comment severity \"nit\"\n" +
	"- The severity \"minor\" is reserved for LLM judgment on findings that\n" +
	"  genuinely don't fit a plugin bucket; the deterministic map never emits it.\n\n" +
	"Verdict roll-up (binary — exactly one of two values):\n" +
	"- Any Must Fix present → verdict \"request-changes\"\n" +
	"- Any Should Fix present → verdict \"request-changes\"\n" +
	"- Only Nice to Have, or nothing flagged → verdict \"approve\"\n\n" +
	"Each comment must pin to a real `file` and `line` from the report. If a\n" +
	"finding has no coordinates, fold it into `summary` instead of emitting an\n" +
	"un-pinned comment. Preserve the plugin's bucket label verbatim in the\n" +
	"comment `message` for traceability.\n"

// BuildExecutionInstructions assembles the execution-phase prompt by reading
// the /coding:pr-review plugin file at runtime, stripping its YAML frontmatter,
// prepending a pre-filled-arguments header, and appending a verdict-translation
// footer so the inlined plugin procedure runs as native instructions.
func BuildExecutionInstructions(
	ctx context.Context,
	claudeConfigDir claudelib.ClaudeConfigDir,
	reviewMode string,
	baseRef string,
) (claudelib.Instructions, error) {
	if baseRef == "" {
		return nil, errors.New(ctx, "base_ref is empty")
	}
	if reviewMode == "" {
		return nil, errors.New(ctx, "reviewMode is empty")
	}

	pluginPath := filepath.Join(
		string(claudeConfigDir),
		"plugins",
		"marketplaces",
		"coding",
		"commands",
		"pr-review.md",
	)
	raw, err := os.ReadFile(pluginPath) // #nosec G304 -- path constructed from trusted config dir
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "read plugin command file path=%s", pluginPath)
	}

	header := fmt.Sprintf(prefilledArgsHeaderTemplate, baseRef, reviewMode)
	assembled := header + stripFrontmatter(string(raw)) + verdictTranslationFooter
	return claudelib.Instructions{
		{Name: "workflow", Content: assembled},
		{Name: "output-format", Content: executionOutputFormat},
	}, nil
}

// stripFrontmatter removes a leading YAML frontmatter block delimited by
// "---\n" ... "\n---\n". If no leading frontmatter is present, the input
// is returned unchanged.
func stripFrontmatter(s string) string {
	const delim = "---\n"
	if !strings.HasPrefix(s, delim) {
		return s
	}
	rest := s[len(delim):]
	end := strings.Index(rest, "\n"+delim)
	if end < 0 {
		return s
	}
	return rest[end+len("\n"+delim):]
}
