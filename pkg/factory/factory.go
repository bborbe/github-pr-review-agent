// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factory wires concrete dependencies for the maintainer-agent-pr-reviewer binary.
//
// All factory functions follow the Create* prefix convention and contain
// zero business logic — they compose constructors with config.
package factory

import (
	"net/http"
	"time"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	delivery "github.com/bborbe/agent/delivery"
	"github.com/bborbe/agent/healthcheck"
	libmetrics "github.com/bborbe/agent/metrics"
	"github.com/bborbe/cqrs/base"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/git"
	"github.com/bborbe/github-pr-review-agent/pkg/github"
	"github.com/bborbe/github-pr-review-agent/pkg/githubposter"
	"github.com/bborbe/github-pr-review-agent/pkg/prompts"
	libkafka "github.com/bborbe/kafka"
	libtime "github.com/bborbe/time"
	domain "github.com/bborbe/vault-cli/pkg/domain"
)

const serviceName = "maintainer-agent-pr-reviewer"

// Per-phase tool scopes. Principle: each phase gets the smallest set that
// lets it do its job. Planning + Review are read-only inspection. Execution
// gets broader git access for cross-file reads; posting happens in-process
// via the PrPoster (Go net/http, not gh CLI) after the LLM step completes,
// gated by bot-identity self-check (GET /app slug-derived login) and
// per-repo .maintainer.yaml (prReviewer.autoApprove: bool). The ai_review phase
// independently verifies the post via GET /pulls/{n}/reviews before
// advancing to done.
var (
	planningTools = claudelib.AllowedTools{
		"Read", "Grep", "Glob",
		"Bash(git diff:*)", "Bash(git log:*)", "Bash(git show:*)",
		"Bash(gh pr view:*)", "Bash(gh pr diff:*)", "Bash(gh pr list:*)",
	}
	// Execution-phase allowlist for the inlined /coding:pr-review procedure.
	//
	// Selector mode (the plugin default since coding v0.22.0, commit 5ac8a60
	// "feat!: selector mode is the default dispatcher; remove standard-mode
	// per-owner dispatch") does classify+adjudicate IN-SESSION with zero
	// sub-agent spawns — so the parent execution phase itself must Read files,
	// run the ast-grep mechanical funnel, and shell jq/git rev-parse. The older
	// per-owner-dispatch model needed only Task+git (the parent just fanned out
	// to sub-agents, which did the reading); when the baked plugin flipped to
	// selector-default, that allowlist stopped covering the review and the bot
	// stalled asking for permission it can't get in a non-interactive container.
	//
	// Boundary preserved: everything below is read-only inspection. The ast-grep
	// mechanical funnel is NO LONGER on this allowlist — the agent runs it itself
	// in Go (pkg.FunnelRunner) and injects the findings into the execution prompt.
	// A weak review model wrapped the runner in forms the allowlist could not
	// match (`> redirect`, `bash -c`, `$RUNNER`), got denied, and silently
	// dropped the MUST-tier pass; running the funnel in Go removes both that
	// invocation-form fragility and the model's ability to skip it. No Write/Edit,
	// no network tools (curl/wget/nc), no arbitrary Bash — a malicious PR still
	// cannot exfiltrate the GitHub App token.
	//
	// No checkout-mutating commands: chunk reviews run concurrently in ONE shared
	// worktree, so `git fetch`, `git worktree`, `git branch` and `rm -rf` (plain
	// and `git -C *` forms) are deliberately absent — one chunk running them could
	// mutate or delete the checkout its siblings and the posting step read. Go
	// owns every fetch, worktree add/prune and cleanup (pkg/git, pkg/funnel.go);
	// the review itself never needs them.
	executionTools = claudelib.AllowedTools{
		"Task",
		"Read", "Grep", "Glob",
		"Bash(git diff:*)",
		"Bash(git log:*)",
		"Bash(git show:*)",
		"Bash(git status:*)",
		"Bash(git ls-files:*)",
		"Bash(git rev-parse:*)",
		"Bash(git -C * diff *)",
		"Bash(git -C * log *)",
		"Bash(git -C * show *)",
		"Bash(git -C * status *)",
		"Bash(git -C * ls-files *)",
		"Bash(git -C * rev-parse *)",
		"Bash(git -C * diff)",
		"Bash(git -C * log)",
		"Bash(git -C * show)",
		"Bash(git -C * status)",
		"Bash(git -C * ls-files)",
		"Bash(git -C * rev-parse)",
		"Bash(command -v:*)",
		"Bash(jq:*)",
	}
	reviewTools = claudelib.AllowedTools{
		"Read", "Grep",
	}
)

// CreateClaudeRunner constructs a ClaudeRunner pre-configured with tools,
// model, working directory, and CLI environment. env is forwarded as-is
// into the Claude CLI subprocess env (caller builds it, e.g. with GH_TOKEN).
func CreateClaudeRunner(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	env map[string]string,
	allowedTools claudelib.AllowedTools,
	usage libmetrics.JobMetrics,
) claudelib.ClaudeRunner {
	return prpkg.NewUsageRecordingRunner(
		claudelib.NewClaudeRunner(claudelib.ClaudeRunnerConfig{
			ClaudeConfigDir:  claudeConfigDir,
			AllowedTools:     allowedTools,
			Model:            model,
			WorkingDirectory: agentDir,
			Env:              env,
		}),
		usage,
	)
}

// CreateKafkaResultDeliverer creates a ResultDeliverer that publishes task
// updates to Kafka via CQRS commands. Uses the passthrough content generator
// — the agent framework's StepRunner already produces the full marshaled
// task in result.Output; the deliverer publishes it as-is.
func CreateKafkaResultDeliverer(
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
	taskID agentlib.TaskIdentifier,
	originalContent string,
	currentDateTime libtime.CurrentDateTimeGetter,
) agentlib.ResultDeliverer {
	return delivery.NewKafkaResultDeliverer(
		syncProducer,
		topicPrefix,
		taskID,
		originalContent,
		delivery.NewPassthroughContentGenerator(),
		currentDateTime,
	)
}

// CreateFileResultDeliverer creates a ResultDeliverer that writes the agent's
// output back to a markdown file (local CLI mode).
func CreateFileResultDeliverer(filePath string) agentlib.ResultDeliverer {
	return delivery.NewFileResultDeliverer(
		delivery.NewPassthroughContentGenerator(),
		filePath,
	)
}

// CreatePrPoster wires a PrPoster backed by a scoped http.Client.
// token is the bot PAT (GH_TOKEN env); botLogin is the bot GitHub login
// (BOT_GITHUB_LOGIN env, default "ben-s-pull-request-reviewer[bot]" if empty). Pure plumbing; no logic.
func CreatePrPoster(
	token, botLogin string,
	currentDateTime libtime.CurrentDateTimeGetter,
) prpkg.PrPoster {
	return githubposter.NewPrPoster(
		&http.Client{Timeout: 15 * time.Second},
		token,
		botLogin,
		currentDateTime,
	)
}

// CreateReviewVerifier wires a ReviewVerifier backed by a scoped http.Client.
// token is the bot PAT; botLogin is the expected bot login.
func CreateReviewVerifier(
	token, botLogin string,
	currentDateTime libtime.CurrentDateTimeGetter,
) prpkg.ReviewVerifier {
	return githubposter.NewReviewVerifier(
		&http.Client{Timeout: 15 * time.Second},
		token,
		botLogin,
		currentDateTime,
	)
}

// CreateAgent assembles the full 3-phase pr-reviewer agent with per-phase
// tool scopes and per-phase prompts:
//
//   - planning: read-only diff inspection → ## Plan (JSON)
//   - execution: read + cross-file inspection → ## Review (JSON); posts review to GitHub via PrPoster
//   - ai_review: minimal read-only fresh-context verifier → ## Verdict (JSON);
//     verdict=pass → done, otherwise → human_review; verifier confirms review
//     persisted on GitHub (nil verifier skips verification)
//
// maxDuration is the soft REVIEW_MAX_DURATION budget threaded into every phase
// step. The execution step gets a nil runner (runClaude builds a fresh one).
// chunkConfig carries the chunking thresholds threaded into the execution step;
// at or below its engage threshold the review runs once, unscoped.
func CreateAgent(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	env map[string]string,
	repoManager git.RepoManager,
	reviewMode string,
	repoAllowlist []string,
	prPoster prpkg.PrPoster,
	verifier prpkg.ReviewVerifier,
	currentDateTime libtime.CurrentDateTimeGetter,
	maxDuration libtime.Duration,
	chunkConfig prpkg.ReviewChunkConfig,
	prStatePollInterval libtime.Duration,
	usage libmetrics.JobMetrics,
) *agentlib.Agent {
	return agentlib.NewAgent(createPhases(
		claudeConfigDir,
		agentDir,
		model,
		ghToken,
		env,
		repoManager,
		reviewMode,
		repoAllowlist,
		prPoster,
		verifier,
		currentDateTime,
		maxDuration,
		chunkConfig,
		prStatePollInterval,
		usage,
	)...)
}

// createPhases builds the three review phases CreateAgent composes. It is split
// out so a composition test can assert that every phase body carries the
// human-review owner stamp: *agentlib.Agent keeps its phase list unexported, so
// a factory that forgets one wrapper is otherwise unobservable from a test.
func createPhases(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	env map[string]string,
	repoManager git.RepoManager,
	reviewMode string,
	repoAllowlist []string,
	prPoster prpkg.PrPoster,
	verifier prpkg.ReviewVerifier,
	currentDateTime libtime.CurrentDateTimeGetter,
	maxDuration libtime.Duration,
	chunkConfig prpkg.ReviewChunkConfig,
	prStatePollInterval libtime.Duration,
	usage libmetrics.JobMetrics,
) []agentlib.Phase {
	botLogin := ResolveBotLogin(env)
	// The human owner stamped onto every human_review routing. Empty disables the
	// stamp, which is the unconfigured default — see ResolveHumanReviewOwner. The
	// warning for an unset value lives at the caller (main), not here: a Create*
	// factory stays pure composition, with no conditional and no I/O.
	humanReviewOwner := ResolveHumanReviewOwner(env)
	tokenCheck := prpkg.NewGHTokenCheckStep(ghToken)
	// Every phase gets the PR-URL preflight so a URL-less task cannot burn a
	// full review and then fail in the post step with failure_step=
	// pr_url_extraction (2026-10-24 prod incident, task 55d44221). Prepended
	// rather than folded into a phase's own step because a run may enter at
	// `execution`, skipping the planning prompt that already specifies
	// needs_input for a missing URL.
	prURLCheck := prpkg.NewPRURLPreflightStep()
	// Live GitHub PR-state client shared by every phase's prStateCheck
	// pre/post-flight (gh CLI wrapper, reuses the ghToken auth path).
	prStateClient := github.NewGHClient(ghToken)
	planningPhase := agentlib.NewPhase(
		"planning",
		tokenCheck,
		prURLCheck,
		prpkg.NewHumanReviewOwnerStep(prpkg.NewPlanningStep(
			CreateClaudeRunner(claudeConfigDir, agentDir, model, env, planningTools, usage),
			prompts.BuildPlanningInstructions(),
			maxDuration,
			prStateClient,
		), humanReviewOwner),
	)
	executionStep := prpkg.NewHumanReviewOwnerStep(prpkg.NewCheckoutExecutionStep(
		repoManager,
		claudeConfigDir,
		agentDir,
		model,
		env,
		executionTools,
		reviewMode,
		repoAllowlist,
		prPoster,
		prpkg.NewFunnelRunner(claudeConfigDir),
		currentDateTime,
		nil, // runner — production builds a fresh ClaudeRunner in runClaude
		maxDuration,
		prStateClient,
		chunkConfig,
		prStatePollInterval,
		usage,
	), humanReviewOwner)
	reviewStep := prpkg.NewHumanReviewOwnerStep(prpkg.NewReviewStep(
		CreateClaudeRunner(claudeConfigDir, agentDir, model, env, reviewTools, usage),
		prPoster,
		prompts.BuildReviewInstructions(),
		verifier,
		ghToken,
		botLogin,
		maxDuration,
		prStateClient,
		prStatePollInterval,
	), humanReviewOwner)
	return []agentlib.Phase{
		planningPhase,
		agentlib.NewPhase(domain.TaskPhaseExecution, tokenCheck, prURLCheck, executionStep),
		agentlib.NewPhase("ai_review", tokenCheck, prURLCheck, reviewStep),
	}
}

// CreateAgentProvider wires the per-task-type dispatch table for maintainer-agent-pr-reviewer.
// TaskTypePRReview routes to the 3-phase domain agent built by CreateAgent.
// TaskTypeHealthcheck routes to a liveness agent that reuses the Claude runner factory.
// Pure plumbing; no conditional, no error.
func CreateAgentProvider(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	env map[string]string,
	repoManager git.RepoManager,
	reviewMode string,
	repoAllowlist []string,
	currentDateTime libtime.CurrentDateTimeGetter,
	maxDuration libtime.Duration,
	chunkConfig prpkg.ReviewChunkConfig,
	prStatePollInterval libtime.Duration,
	usage libmetrics.JobMetrics,
) agentlib.AgentProvider {
	botLogin := ResolveBotLogin(env)
	poster := CreatePrPoster(ghToken, botLogin, currentDateTime)
	verifier := CreateReviewVerifier(ghToken, botLogin, currentDateTime)
	domainAgent := CreateAgent(
		claudeConfigDir,
		agentDir,
		model,
		ghToken,
		env,
		repoManager,
		reviewMode,
		repoAllowlist,
		poster,
		verifier,
		currentDateTime,
		maxDuration,
		chunkConfig,
		prStatePollInterval,
		usage,
	)
	healthcheckRunner := CreateClaudeRunner(
		claudeConfigDir,
		agentDir,
		model,
		env,
		claudelib.AllowedTools{},
		usage,
	)
	livenessAgent := healthcheck.NewAgent(healthcheck.NewClaudeStep(healthcheckRunner))

	// pr-override: a second task type on this same service. The override action
	// is deterministic (post an APPROVE at head SHA so a false-positive review
	// no longer blocks merge), so it is a code-only single-phase agent — no
	// Claude, no clone — reusing the poster already built above. The phase is
	// named "execution" (a valid domain.TaskPhase, matching the frontmatter the
	// watcher emits — the vault-cli validator rejects unknown phase literals);
	// task_type pr-override, not the phase name, is what routes here, so there
	// is no collision with the review agent's execution phase.
	overrideAgent := agentlib.NewAgent(
		createOverridePhase(ghToken, poster, botLogin, ResolveHumanReviewOwner(env)),
	)

	return agentlib.NewAgentProvider(serviceName, map[agentlib.TaskType]*agentlib.Agent{
		agentlib.TaskTypePRReview:    domainAgent,
		agentlib.TaskTypeHealthcheck: livenessAgent,
		prpkg.TaskTypePROverride:     overrideAgent,
	})
}

// createOverridePhase builds the pr-override phase. Split from
// CreateAgentProvider for the same composition-test reason as createPhases.
func createOverridePhase(
	ghToken string,
	poster prpkg.PrPoster,
	botLogin string,
	humanReviewOwner string,
) agentlib.Phase {
	return agentlib.NewPhase(
		domain.TaskPhaseExecution,
		prpkg.NewGHTokenCheckStep(ghToken),
		prpkg.NewHumanReviewOwnerStep(
			prpkg.NewOverrideStep(poster, botLogin),
			humanReviewOwner,
		),
	)
}

// CreateDeliverer builds the Kafka result deliverer used by the Kafka
// entry point. The caller owns the SyncProducer lifecycle and must close it
// after the deliverer is no longer needed.
func CreateDeliverer(
	syncProducer libkafka.SyncProducer,
	taskID agentlib.TaskIdentifier,
	topicPrefix base.TopicPrefix,
	originalContent string,
	currentDateTime libtime.CurrentDateTimeGetter,
) agentlib.ResultDeliverer {
	return CreateKafkaResultDeliverer(
		syncProducer,
		topicPrefix,
		taskID,
		originalContent,
		currentDateTime,
	)
}
