# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

- fix(security): bump `golang.org/x/text` v0.38.0 → v0.40.0 to clear CVE-2026-56852 (infinite loop on invalid input) and restore a green `make precommit` baseline.

## v0.3.0

- feat: add `pr-override` task type. A trusted-author PR carrying the `override-review` label (emitted by github-pr-watcher) routes to a new code-only, single-phase override agent that posts an `APPROVE` at the PR head SHA via the new `PrPoster.PostOverrideApprove` — a fresh bot APPROVE supersedes the bot's own false-positive `CHANGES_REQUESTED` for reviewDecision, so any write-access user can merge without admin. No Claude, no clone, no container. CI status checks still apply (the label clears only the review requirement). Registered as a second task type in `CreateAgentProvider` alongside `pr-review` + `healthcheck`; `PostOverrideApprove` posts unconditionally (no autoApprove gate, no WorkDir, no prior-review dismissal), mirroring `PostLGTM`.

## v0.2.0

- fix: remove the planning-phase "no concerns → LGTM" shortcut. Planning is now pure triage: every GitHub PR advances to the execution phase for a real (checkout + deep) review that posts an earned `APPROVE`/`REQUEST_CHANGES` verdict. Previously a shallow planning pass with empty concerns posted a `COMMENT` "no concerns flagged" review without ever running the real review — a rubber-stamp that also failed to satisfy a required-approving-review merge gate (a COMMENT is not an approval). Tasks with no GitHub PR URL now escalate to `human_review` (subsumes the old non-GitHub-platform terminal case) instead of posting an LGTM. Removed the now-dead `postLGTMAndDone`/`handleEmptyPRURL`/`isGitHubPRURL`/`hasAnyPRURL`/`writePlanningVerdict` planning helpers and simplified `NewPlanningStep` (no longer takes a poster/botLogin/clock). `PrPoster.PostLGTM` is retained but deprecated.

## v0.1.3

- fix: planning-step JSON parser now tolerates conversational prose around the `## Plan` block. `parsePlanningConcerns` previously only stripped ```json fences at the very start, so any model that narrates before the fence (DeepSeek/vLLM emits e.g. "Now I have the full picture…") produced `invalid character 'N'`, failed 3× retries, and marked the review task `failed` with no verdict posted. New `extractJSONObject` locates the JSON via the first ```json/``` fenced block, else the first `{`…last `}` span. Unblocks real-diff reviews on non-Anthropic endpoints (real Anthropic emits clean JSON, so this was latent on quant).

## v0.1.2

- fix: forward `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU,FABLE}_MODEL` (new `--anthropic-default-*-model` args / env) into the claude subprocess env, so spawned review sub-agents (which request opus/sonnet/haiku) resolve to the configured model instead of the default `claude-sonnet-*`. Needed against non-Anthropic endpoints (DeepSeek/vLLM) where the default aliases 404 — the top-level `--model` worked but sub-agents crashed. Empty = unset (no-op on Anthropic).
- chore: bump Go 1.26.4 → 1.26.5 (GO-2026-5856); ignore unmaintained-openpgp GO-2026-5932 in `VULNCHECK_IGNORE` + `.trivyignore`.

## v0.1.1

- refactor: import the shared library from its new root module path `github.com/bborbe/maintainer` (was `github.com/bborbe/maintainer/lib`) and bump to `@v0.45.0`. The maintainer repo flattened `lib/` to its root to match the `bborbe/agent` layout. No behavior change.

## v0.1.0

- Extracted from the `bborbe/maintainer` monorepo (`agent/pr-reviewer`) into a
  standalone publish-only repository. The shared code now comes from the versioned
  `github.com/bborbe/maintainer/lib` module instead of a local `replace`. Builds and
  publishes `docker.io/bborbe/github-pr-review-agent:<version>` via `make buca`.
