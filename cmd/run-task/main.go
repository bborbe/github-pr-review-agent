// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command run-task is the local-CLI entry point for maintainer-agent-pr-reviewer.
//
// Reads a markdown task file from disk, runs the agent against it, and
// writes the updated content back to the same file. Mirrors the Kafka
// entry point (../../main.go) but uses file I/O instead of Kafka/CQRS.
package main

import (
	"context"
	"os"
	"path/filepath"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	"github.com/bborbe/github-pr-review-agent/pkg/factory"
	"github.com/bborbe/github-pr-review-agent/pkg/githubauth"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	githubapp "github.com/bborbe/maintainer/githubapp"
	repoallowlist "github.com/bborbe/maintainer/repoallowlist"
)

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"SentryDSN"    display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy"`

	// Claude Code CLI configuration
	ClaudeConfigDir claudelib.ClaudeConfigDir `required:"false" arg:"claude-config-dir" env:"CLAUDE_CONFIG_DIR" usage:"Claude Code config directory" default:"~/.claude"`

	// Agent directory (contains .claude/ with CLAUDE.md and commands)
	AgentDir claudelib.AgentDir `required:"false" arg:"agent-dir" env:"AGENT_DIR" usage:"Agent directory with .claude/ config" default:"agent"`

	// Workdir paths for bare-clone cache and per-task worktrees (default: ~/.cache/maintainer/pr-reviewer/*)
	ReposPath string `required:"false" arg:"repos-path" env:"REPOS_PATH" usage:"Root path for bare-clone cache (default: ~/.cache/maintainer/pr-reviewer/repos)"`
	WorkPath  string `required:"false" arg:"work-path"  env:"WORK_PATH"  usage:"Root path for per-task worktrees (default: ~/.cache/maintainer/pr-reviewer/work)"`

	// Review depth passed to /coding:pr-review (short | standard | full)
	ReviewMode string `required:"false" arg:"review-mode" env:"REVIEW_MODE" usage:"Review depth: short | standard | full" default:"standard"`

	// Environment
	Branch base.Branch `required:"true" arg:"branch" env:"BRANCH" usage:"branch" default:"dev"`

	// Phase to run (framework requires explicit phase)
	Phase domain.TaskPhase `required:"false" arg:"phase" env:"PHASE" usage:"Agent phase: planning | execution | ai_review" default:"execution"`

	// Task file for local development
	TaskFilePath string `required:"true" arg:"task-file" env:"TASK_FILE" usage:"Path to the markdown task file"`

	// GitHub App authentication. The pod mints an installation access token at startup
	// and forwards it to the agent subprocess. App auth is the only supported auth path.
	AppID          int64  `required:"false" arg:"app-id"          env:"APP_ID"           usage:"GitHub App ID (numeric); required for App auth"`
	InstallationID int64  `required:"false" arg:"installation-id" env:"INSTALLATION_ID"  usage:"GitHub App Installation ID (numeric)"`
	PEMKeyFile     string `required:"false" arg:"pem-key-file"    env:"PEM_KEY_FILE"     usage:"Path to the GitHub App private key (PEM file mounted from k8s Secret)"`
	PEMKey         string `required:"false" arg:"pem-key"         env:"PEM_KEY"          usage:"GitHub App private key (PEM) as env var content; mutually exclusive with PEM_KEY_FILE" display:"length"`
	BotLogin       string `required:"false" arg:"bot-login"       env:"BOT_GITHUB_LOGIN" usage:"Bot identity used by githubposter (e.g. ben-s-pull-request-reviewer[bot])"                              default:"ben-s-pull-request-reviewer[bot]"`

	// Repo allowlist — comma-separated host/owner/repo entries; empty means allow-all.
	RepoAllowlist string `required:"false" arg:"repo-allowlist" env:"REPO_ALLOWLIST" usage:"Comma-separated host-qualified repo allowlist (host/owner/repo); empty means allow-all"`

	// Anthropic-compatible provider routing. Setting AnthropicBaseURL + AnthropicAuthToken
	// routes the claude CLI to an alt-provider (e.g. MiniMax via https://api.minimax.io/anthropic).
	// AnthropicModel drives both the `--model` CLI flag and the ANTHROPIC_MODEL env var seen by
	// the claude subprocess.
	AnthropicBaseURL   string                `required:"false" arg:"anthropic-base-url"   env:"ANTHROPIC_BASE_URL"   usage:"Anthropic-compatible API base URL"`
	AnthropicAuthToken string                `required:"false" arg:"anthropic-auth-token" env:"ANTHROPIC_AUTH_TOKEN" usage:"Bearer token for ANTHROPIC_BASE_URL"                                  display:"length"`
	AnthropicModel     claudelib.ClaudeModel `required:"false" arg:"anthropic-model"      env:"ANTHROPIC_MODEL"      usage:"Model name; also exposed to the claude subprocess as ANTHROPIC_MODEL"                  default:"sonnet"`
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	repoAllowlist, err := prpkg.ParseRepoAllowlist(ctx, a.RepoAllowlist)
	if err != nil {
		return err
	}
	// Warn on malformed entries; allow-all and wildcard semantics handled by IsAllowed at match time.
	if validationErr := repoallowlist.Validate(ctx, repoAllowlist); validationErr != nil {
		glog.Warningf(
			"REPO_ALLOWLIST contains malformed entries (will be ignored at match time): %v",
			validationErr,
		)
	}
	glog.V(2).Infof("repo-allowlist count=%d", len(repoAllowlist))

	taskContent, err := os.ReadFile(
		a.TaskFilePath,
	) // #nosec G304 -- filePath from trusted CLI input
	if err != nil {
		return errors.Wrapf(ctx, err, "read task file: %s", a.TaskFilePath)
	}

	reposPath, workPath, err := a.resolveCachePaths(ctx)
	if err != nil {
		return err
	}

	deliverer := factory.CreateFileResultDeliverer(a.TaskFilePath)

	// Resolve auth: mint the GitHub App IAT before factory.RunAgent reads the resolved token.
	hasPEMFile := a.PEMKeyFile != ""
	hasPEMContent := a.PEMKey != ""
	useGitHubApp := a.AppID != 0 && a.InstallationID != 0 && (hasPEMFile || hasPEMContent)
	if !useGitHubApp {
		return errors.Errorf(
			ctx,
			"pr-reviewer auth: GitHub App credentials not configured — set APP_ID, INSTALLATION_ID, and PEM_KEY_FILE (or PEM_KEY)",
		)
	}
	appCfg := githubapp.Config{AppID: a.AppID, InstallationID: a.InstallationID}
	if hasPEMFile {
		appCfg.PEMPath = a.PEMKeyFile
	} else {
		appCfg.PEM = []byte(a.PEMKey)
	}
	resolvedToken, err := githubapp.MintIAT(ctx, appCfg)
	if err != nil {
		return errors.Wrap(ctx, err, "mint github app iat")
	}
	glog.V(2).Infof(
		"pr-reviewer auth mode=github-app app_id=%d installation_id=%d",
		a.AppID, a.InstallationID,
	)

	authSetup := githubauth.NewNoopAuthSetup()
	result, err := factory.RunAgent(ctx, factory.RunConfig{
		ClaudeConfigDir:    a.ClaudeConfigDir,
		AgentDir:           a.AgentDir,
		Model:              a.AnthropicModel,
		GHToken:            resolvedToken,
		AnthropicBaseURL:   a.AnthropicBaseURL,
		AnthropicAuthToken: a.AnthropicAuthToken,
		ReposPath:          reposPath,
		WorkPath:           workPath,
		ReviewMode:         a.ReviewMode,
		RepoAllowlist:      repoAllowlist,
		AuthSetup:          authSetup,
		Phase:              a.Phase,
		TaskContent:        string(taskContent),
		Deliverer:          deliverer,
		BotLogin:           a.BotLogin,
	})
	if err != nil {
		return errors.Wrap(ctx, err, "agent run failed")
	}
	return agentlib.PrintResult(ctx, result)
}

// resolveCachePaths fills in defaults for ReposPath/WorkPath when unset
// (~/.cache/maintainer/pr-reviewer/{repos,work}). The pod entry point requires
// explicit /repos and /work mounts, but local CLI usage benefits from a default.
func (a *application) resolveCachePaths(ctx context.Context) (string, string, error) {
	reposPath := a.ReposPath
	workPath := a.WorkPath
	if reposPath != "" && workPath != "" {
		return reposPath, workPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", errors.Wrap(ctx, err, "resolve user home dir")
	}
	if reposPath == "" {
		reposPath = filepath.Join(home, ".cache", "maintainer", "pr-reviewer", "repos")
	}
	if workPath == "" {
		workPath = filepath.Join(home, ".cache", "maintainer", "pr-reviewer", "work")
	}
	return reposPath, workPath, nil
}
