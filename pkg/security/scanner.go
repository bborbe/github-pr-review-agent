// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package security runs the dependency scan that the `/coding:pr-review
// --security` procedure requires (its Step 7) inside the agent itself.
//
// It lives in Go rather than in the review prompt for the same reason the
// mechanical funnel does (see pkg.FunnelRunner): the review runs headless under
// a fixed tool allowlist, and a model-invoked scanner is wrapped in forms the
// allowlist cannot match and then silently dropped. govulncheck is not an option
// either — the runtime image carries no Go toolchain (the Dockerfile copies only
// /main out of the build stage), so it cannot run at any mode. Querying OSV over
// HTTPS needs nothing but net/http.
//
// The result is injected into the execution prompt; the model consumes it and
// never shells out.
package security

import (
	"context"
	"os"
	"time"
)

// Result status values. The prompt injection branches on Status, so a scan that
// could not run is never rendered as a clean one.
const (
	// StatusScanned means the scan ran; Findings may legitimately be empty.
	StatusScanned = "scanned"
	// StatusNotApplicable means the worktree carries no go.mod. The review must
	// still say so out loud — a silent skip is indistinguishable from a clean
	// pass — but it is not a blocking finding.
	StatusNotApplicable = "not-applicable"
	// StatusFailed means the scan could not complete. The prompt injection turns
	// this into a blocking `kind: toolchain` finding: the procedure's Step 7 is
	// fail-closed, never a silent skip.
	StatusFailed = "failed"
)

// Finding is one dependency vulnerability, shaped for the review's
// `kind: toolchain` output. Advisory carries the OSV id (e.g. GO-2024-1234);
// Aliases carry the CVE/GHSA identifiers when OSV knows them.
type Finding struct {
	Tool     string   `json:"tool"`
	Package  string   `json:"package"`
	Version  string   `json:"version"`
	Advisory string   `json:"advisory"`
	Aliases  []string `json:"aliases,omitempty"`
	Message  string   `json:"message"`
}

// Result is the whole outcome of a dependency scan, JSON-serialisable so the
// caller can inject it verbatim into the execution prompt.
type Result struct {
	Status   string    `json:"status"`
	Detail   string    `json:"detail,omitempty"`
	Modules  int       `json:"modules"`
	Findings []Finding `json:"findings"`
}

// ModuleVersion is one module requirement read from go.mod.
type ModuleVersion struct {
	Path    string
	Version string
}

// Scanner scans one checked-out worktree for vulnerable dependencies.
//
// A Scanner never returns an error: a scan that could not run is a Result with
// StatusFailed, because the caller must inject the failure into the review rather
// than abort it.
type Scanner interface {
	Scan(ctx context.Context, worktreePath string) Result
}

// OSVClient resolves vulnerable modules. Split from Scanner so tests exercise the
// scan logic without the network.
type OSVClient interface {
	Findings(ctx context.Context, modules []ModuleVersion) ([]Finding, error)
}

// NewScanner constructs the production Scanner, querying the public OSV API.
// timeout bounds each individual HTTP round trip, not the whole scan.
func NewScanner(timeout time.Duration) Scanner {
	return &scanner{client: newOSVClient(timeout), readFile: readFile}
}

// NewScannerWithClient constructs a Scanner over an injected OSV client and
// file reader. Either may be nil, in which case the production default is used.
func NewScannerWithClient(client OSVClient, readFileFn func(string) (string, error)) Scanner {
	if client == nil {
		client = newOSVClient(defaultTimeout)
	}
	if readFileFn == nil {
		readFileFn = readFile
	}
	return &scanner{client: client, readFile: readFileFn}
}

type scanner struct {
	client   OSVClient
	readFile func(path string) (string, error)
}

// Scan reads go.mod from the worktree, queries OSV for every required module and
// returns the findings.
func (s *scanner) Scan(ctx context.Context, worktreePath string) Result {
	content, err := s.readFile(worktreePath + "/go.mod")
	if err != nil {
		return Result{
			Status: StatusNotApplicable,
			Detail: "dependency scan: not applicable (no go.mod)",
		}
	}
	modules := ParseGoMod(content)
	if len(modules) == 0 {
		return Result{
			Status:  StatusScanned,
			Detail:  "go.mod declares no module requirements",
			Modules: 0,
		}
	}
	findings, err := s.client.Findings(ctx, modules)
	if err != nil {
		return Result{
			Status: StatusFailed,
			Detail: err.Error(),
		}
	}
	return Result{
		Status:   StatusScanned,
		Modules:  len(modules),
		Findings: findings,
	}
}

func readFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
