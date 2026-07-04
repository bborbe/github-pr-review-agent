// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	prurl "github.com/bborbe/maintainer/prurl"
)

// planningOutput is the parsed shape of the ## Plan JSON block.
type planningOutput struct {
	Concerns []struct{} `json:"concerns"`
}

// maxPlanningAttempts is the hardcoded cap on Claude planning calls per
// invocation. Malformed-JSON responses are retried up to this many times;
// AgentStatusFailed is returned only after all attempts fail. Not configurable.
const maxPlanningAttempts = 3

// planningStep runs Claude to produce the ## Plan section, then branches:
// - concerns empty → POST LGTM via PrPoster → write ## Verdict → done
// - concerns non-empty → advance to the execution phase
type planningStep struct {
	runner          claudelib.ClaudeRunner
	instructions    claudelib.Instructions
	prPoster        PrPoster // nil = skip posting (cmd/run-task mode)
	botLogin        string
	currentDateTime libtime.CurrentDateTimeGetter
}

// NewPlanningStep constructs the planning-phase step.
// prPoster may be nil (local CLI mode).
func NewPlanningStep(
	runner claudelib.ClaudeRunner,
	instructions claudelib.Instructions,
	prPoster PrPoster,
	botLogin string,
	currentDateTime libtime.CurrentDateTimeGetter,
) agentlib.Step {
	return &planningStep{
		runner:          runner,
		instructions:    instructions,
		prPoster:        prPoster,
		botLogin:        botLogin,
		currentDateTime: currentDateTime,
	}
}

// Name implements agentlib.Step.
func (s *planningStep) Name() string { return "pr-plan" }

// ShouldRun always returns true. Idempotency is handled inside Run: if a
// ## Plan section already exists in the body (e.g. left over from a previous
// trigger whose execution phase failed and got retriggered), the step skips
// the claude call but still parses the existing plan and publishes the
// routing decision (Done + NextPhase=execution|done|human_review). Returning
// false here would skip the routing too and the phase would silently
// short-circuit to done — see the trading#136 retrigger incident
// (2026-05-25), where the controller reset trigger_count, the stale ## Plan
// remained, the planning step was skipped, no execution ran, and the task
// got marked phase: done without any review posted.
func (s *planningStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run handles two paths:
//   - ## Plan already present → re-parse and re-route from the existing body
//     without re-calling claude (preserves idempotency for cost reasons).
//   - ## Plan missing → call claude with the planning prompt, write ## Plan,
//     parse concerns, route.
//
// Routes: empty concerns → LGTM POST → done; non-empty → execution phase.
func (s *planningStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	if section, exists := md.FindSection("## Plan"); exists {
		glog.V(2).Infof("planning: ## Plan already present — re-routing without claude")
		return s.routeFromPlan(ctx, md, section.Body)
	}

	taskContent, err := md.Marshal(ctx)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "planning marshal task")
	}

	prURL := ExtractPRURL(md)
	ref, _ := md.Frontmatter.String("ref")
	glog.V(2).Infof("planning: starting pr_url=%q ref=%s", prURL, ref)

	prompt := claudelib.BuildPrompt(s.instructions.String(), nil, taskContent)

	var lastParseErr error
	for attempt := 1; attempt <= maxPlanningAttempts; attempt++ {
		runResult, runErr := s.runner.Run(ctx, prompt)
		if runErr != nil {
			// Transport error (nil result + err) is NOT retried — controller territory.
			glog.V(2).Infof("planning: claude failed nextPhase=human_review err=%v", runErr)
			return &agentlib.Result{
				Status:  agentlib.AgentStatusFailed,
				Message: fmt.Sprintf("planning claude run failed: %v", runErr),
			}, nil
		}

		if _, parseErr := parsePlanningConcerns(ctx, runResult.Result); parseErr != nil {
			lastParseErr = parseErr
			if attempt < maxPlanningAttempts {
				glog.V(2).Infof(
					"planning: attempt %d/%d malformed JSON, retrying err=%v",
					attempt, maxPlanningAttempts, parseErr,
				)
				continue
			}
			// Exhausted all attempts.
			glog.V(2).Infof(
				"planning: malformed JSON after %d attempts, not persisting err=%v",
				maxPlanningAttempts, parseErr,
			)
			return &agentlib.Result{
				Status: agentlib.AgentStatusFailed,
				Message: fmt.Sprintf(
					"planning: malformed JSON after %d attempts: %v",
					maxPlanningAttempts,
					lastParseErr,
				),
			}, nil
		}

		// Parseable — persist this response and route.
		md.ReplaceSection(agentlib.Section{
			Heading: "## Plan",
			Body:    runResult.Result,
		})
		return s.routeFromPlan(ctx, md, runResult.Result)
	}

	// Unreachable — the loop returns on every path. Kept for compiler completeness.
	return &agentlib.Result{
		Status: agentlib.AgentStatusFailed,
		Message: fmt.Sprintf(
			"planning: malformed JSON after %d attempts: %v",
			maxPlanningAttempts,
			lastParseErr,
		),
	}, nil
}

// routeFromPlan parses concerns from a ## Plan body (freshly produced by
// claude or read back from the vault on retrigger) and returns the routing
// decision. Centralised so the "## Plan exists" and "claude just produced
// ## Plan" paths produce identical Result values.
func (s *planningStep) routeFromPlan(
	ctx context.Context,
	md *agentlib.Markdown,
	planBody string,
) (*agentlib.Result, error) {
	concerns, parseErr := parsePlanningConcerns(ctx, planBody)
	if parseErr != nil {
		// Malformed JSON in ## Plan is a planning failure — escalate.
		glog.V(2).Infof("planning: parse failed nextPhase=human_review err=%v", parseErr)
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: "human_review",
			Message:   fmt.Sprintf("planning: failed to parse ## Plan JSON: %v", parseErr),
		}, nil
	}

	if len(concerns) == 0 {
		// Empty concerns — LGTM path.
		return s.postLGTMAndDone(ctx, md)
	}

	// Non-empty concerns — advance to the execution phase (canonical name per
	// spec 032; do NOT revert to "in_progress" — the agentlib frontmatter validator
	// rejects that stale literal and the task silently short-circuits to done).
	glog.V(2).Infof("planning: %d concerns nextPhase=execution", len(concerns))
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: string(domain.TaskPhaseExecution),
	}, nil
}

// postLGTMAndDone posts an LGTM COMMENT review and writes ## Verdict.
func (s *planningStep) postLGTMAndDone(
	ctx context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	prURLStr := ExtractPRURL(md)
	if prURLStr == "" {
		return s.handleEmptyPRURL(ctx, md)
	}
	if !isGitHubPRURL(prURLStr) {
		glog.V(2).Infof("planning: non-github PR URL nextPhase=done url=%s", prURLStr)
		return &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "done"}, nil
	}
	prInfo, parseErr := prurl.ParsePRURL(ctx, prURLStr)
	if parseErr != nil {
		glog.V(2).
			Infof("planning: PR URL parse failed nextPhase=human_review url=%q err=%v", prURLStr, parseErr)
		return &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "human_review",
			Message: fmt.Sprintf(
				"planning: failed to parse PR URL %q: %v",
				prURLStr,
				parseErr,
			)}, nil
	}
	if prInfo.Platform != prurl.PlatformGitHub {
		glog.V(2).Infof("planning: non-github platform nextPhase=done platform=%s", prInfo.Platform)
		return &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "done"}, nil
	}
	ref, _ := md.Frontmatter.String("ref")
	jobRunTime := s.currentDateTime.Now()
	if s.prPoster != nil {
		result := s.prPoster.PostLGTM(ctx, *prInfo, ref, "", s.botLogin)
		appendDiagnosticsSection(
			md,
			buildDiagnosticBlock(time.Time(jobRunTime), md.Frontmatter.TriggerCount(), result),
		)
		if result.Outcome != "success" && result.Class != ErrorClassNotAFailure {
			glog.V(2).
				Infof("planning: LGTM POST failed nextPhase=human_review outcome=%s class=%s http=%d err=%s",
					result.Outcome, result.Class, result.HTTPStatus, result.ErrorMessage)
			return &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "human_review",
				Message: fmt.Sprintf("planning: LGTM POST failed: %s", result.ErrorMessage)}, nil
		}
		writePlanningVerdict(md, result.ReviewID, "COMMENT")
		glog.V(2).Infof("planning: LGTM POST success nextPhase=done review_id=%d", result.ReviewID)
		return &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "done"}, nil
	}
	glog.V(2).Infof("planning: nil poster (local mode) nextPhase=done")
	return &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "done"}, nil
}

// handleEmptyPRURL handles the case where no GitHub PR URL is found.
// If any non-GitHub PR URL exists, skip posting and return done.
// If no PR URL at all, escalate to human_review.
func (s *planningStep) handleEmptyPRURL(
	_ context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	if hasAnyPRURL(md) {
		glog.V(2).Infof("planning: non-github PR URL present nextPhase=done")
		return &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "done"}, nil
	}
	glog.V(2).Infof("planning: no PR URL nextPhase=human_review")
	return &agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: "human_review",
		Message: "planning: no GitHub PR URL found — cannot post LGTM"}, nil
}

// parsePlanningConcerns extracts the concerns array from the ## Plan JSON body.
// The JSON may be wrapped in ```json ... ``` fences. Returns an error if the
// JSON cannot be parsed or the concerns field is absent.
func parsePlanningConcerns(ctx context.Context, body string) ([]struct{}, error) {
	trimmed := strings.TrimSpace(body)
	// Strip ```json fences.
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	var p planningOutput
	if err := json.Unmarshal([]byte(trimmed), &p); err != nil {
		return nil, errors.Wrapf(ctx, err, "parse ## Plan JSON")
	}
	return p.Concerns, nil
}

// writePlanningVerdict writes the ## Verdict section after an LGTM POST.
func writePlanningVerdict(md *agentlib.Markdown, reviewID int64, postedEvent string) {
	body := fmt.Sprintf("review_id: %d\nevent: %s\n", reviewID, postedEvent)
	md.ReplaceSection(agentlib.Section{Heading: "## Verdict", Body: body})
}

// isGitHubPRURL returns true if the URL looks like a GitHub PR URL.
// This is used to distinguish GitHub URLs (which we post to) from
// non-GitHub URLs (which we skip). It uses the same regex as ExtractPRURL
// so the check is consistent with what we extract.
func isGitHubPRURL(rawURL string) bool {
	return githubPRURLPattern.MatchString(rawURL)
}

// anyPRURLPattern matches any PR URL (GitHub, Bitbucket, etc.) in arbitrary text.
var anyPRURLPattern = regexp.MustCompile(`https?://[^\s]+/pull/\d+`)

// hasAnyPRURL returns true if the markdown preamble or any section before the first
// H2 heading contains a PR URL (of any platform). This is used to distinguish
// "no PR URL" (escalate to human_review) from "non-GitHub PR URL" (skip posting).
func hasAnyPRURL(md *agentlib.Markdown) bool {
	if anyPRURLPattern.MatchString(md.Preamble) {
		return true
	}
	for _, sec := range md.Sections {
		if strings.HasPrefix(sec.Heading, "## ") {
			break
		}
		if anyPRURLPattern.MatchString(sec.Heading + "\n" + sec.Body) {
			return true
		}
	}
	return false
}
