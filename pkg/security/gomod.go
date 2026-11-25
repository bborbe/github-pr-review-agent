// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package security

import "strings"

// ParseGoMod extracts the module requirements from a go.mod file.
//
// It handles both forms — a `require ( … )` block and a single-line
// `require <module> <version>` — and skips `replace`, `exclude` and `retract`
// directives: those do not name a module the build resolves against OSV.
// Duplicate modules are collapsed, keeping the first occurrence, and a line with
// no `v`-prefixed version is dropped rather than guessed at.
func ParseGoMod(content string) []ModuleVersion {
	modules := make([]ModuleVersion, 0)
	seen := map[string]bool{}
	inRequireBlock := false

	for _, raw := range strings.Split(content, "\n") {
		line := stripComment(raw)
		if line == "" {
			continue
		}
		if inRequireBlock {
			if line == ")" {
				inRequireBlock = false
				continue
			}
			modules = appendUnique(modules, seen, line)
			continue
		}
		rest, opensBlock := requireBody(line)
		if opensBlock {
			inRequireBlock = true
			continue
		}
		if rest != "" {
			modules = appendUnique(modules, seen, rest)
		}
	}
	return modules
}

// stripComment trims a line and drops its trailing `// …` comment.
func stripComment(raw string) string {
	line := strings.TrimSpace(raw)
	if idx := strings.Index(line, "//"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	return line
}

// requireBody returns the text after a `require` keyword and whether that keyword
// opens a `require (` block. A line that is not a require directive returns
// ("", false).
func requireBody(line string) (string, bool) {
	if !strings.HasPrefix(line, "require") {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "require"))
	if rest == "(" {
		return "", true
	}
	return rest, false
}

// appendUnique parses one `<path> <version>` line and appends it unless the
// module is already present or the line is not a usable requirement.
func appendUnique(
	modules []ModuleVersion,
	seen map[string]bool,
	line string,
) []ModuleVersion {
	mod, ok := parseModuleLine(line)
	if !ok || seen[mod.Path] {
		return modules
	}
	seen[mod.Path] = true
	return append(modules, mod)
}

// parseModuleLine reads `<path> <version>` and rejects anything without a
// `v`-prefixed version.
func parseModuleLine(line string) (ModuleVersion, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ModuleVersion{}, false
	}
	if !strings.HasPrefix(fields[1], "v") {
		return ModuleVersion{}, false
	}
	return ModuleVersion{Path: fields[0], Version: fields[1]}, true
}
