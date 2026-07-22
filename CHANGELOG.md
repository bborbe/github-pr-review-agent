# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

- fix: code-level fail-closed gate on the funnel-failed path. When the Go-side mechanical funnel could not run (`funnel.Ran == false`) and the review model still returns `approve`, `postAndRoute` now overrides the verdict to `request-changes` (reason `mechanical funnel did not run`, recognised by `isFailClosedReason`) instead of trusting the prompt-only "you MUST NOT approve" instruction — which leaned on the same weak model this fix exists to work around. Mirrors the existing unparseable-verdict fail-close. `funnel.Ran` is threaded through `runClaude` → `postAndRoute`; tests cover both directions (funnelRan=false approve→request-changes, funnelRan=true approve preserved).

## v0.3.5

- fix: run the ast-grep mechanical funnel in the agent (Go) and inject its findings into the execution prompt, instead of steering the review model to invoke the runner. The execution prompt prescribed `ast-grep-runner.sh … > /tmp/pr-review-findings.json` — but that `>` redirect (plus the model's own `; echo EXIT:$?` and `bash -c '…'` wrappers) makes a compound command the `Bash(runner.sh:*)` allowlist entry can never match, so every runner call was denied. A weak model (MiniMax-M2.7, quant dev+prod) then concluded "funnel unavailable" and silently did a judgment-only review — dropping the entire MUST-tier mechanical pass on every PR, invisibly. Reproduced on quant dev (go-skeleton#58): runner ground truth was 6 findings; the bot posted a judgment-only review sourced from code comments, not the runner. New `pkg.FunnelRunner` diff-scopes to the PR's changed files and execs the runner deterministically; `BuildExecutionInstructions` injects the authoritative JSON (model must consume, not re-run) or, on funnel failure, a fail-closed status that forbids a silent `approve`. The runner path is removed from `factory.executionTools` (the model no longer invokes it — smaller attack surface). Because the injected findings carry PR-author-controlled strings (matched text from the diff), the runner output is validated as well-formed JSON (fail-closed on garbage / a compromised runner) and any markdown code-fence sequence is neutralised before embedding, so a crafted PR cannot break out of the prompt's ```json block and inject directives. Fixes the bypass model-independently (M2.7 or M3).

## v0.3.4

- fix: extract the verdict JSON by ```json fence boundaries instead of byte-level brace matching. `findLastJSONVerdictBlock` walked braces without string-awareness, so a stray brace or unescaped quote inside a JSON string *value* — common when the review prose describes parser code — mis-extracted the block, failed `json.Unmarshal`, and fail-closed an `approve` verdict to `request-changes` → GitHub state `CHANGES_REQUESTED` → blocked auto-merge and forced admin overrides. Observed on bborbe/github-update-go-agent#5 (valid JSON, a lone `'}'` in a string fooled the brace walker into grabbing prose) and #3 (unescaped inner `"` made the fenced block invalid JSON). New `findFencedJSONVerdictBlock` extracts the last ```json fenced block containing a `verdict` field (fence-delimited → immune to braces/quotes in string values); the brace walk survives only as a fallback for bare, unfenced JSON. When a fenced block is still invalid JSON, `recoverFencedVerdict` reads the literal `verdict` field verbatim — it can surface only the value the model actually wrote, never invent one, so it cannot flip a genuine `request-changes` to `approve`. Regression fixtures are the real #3/#5/#6 review bodies captured from the GitHub API (`pkg/testdata/`). The counter-example #6 (Dockerfile review, no braces/quotes in string values) already worked and stays `approve`.

## v0.3.3

- fix: install `python3` and `jq` in the Dockerfile alpine stage so the ast-grep mechanical funnel (`scripts/ast-grep-runner.sh`, Step 4a of `/coding:pr-review`) can run. The image shipped `ast-grep` but neither `python3` (the runner shells `python3 -c` for millisecond timestamps at lines 82/254, unconditionally under `set -euo pipefail`) nor `jq` (used throughout for JSON assembly) — so the runner died at line 82 with `python3: command not found` (exit 127) before any scan, silently skipping the entire MUST-tier mechanical rule pass on every review while judgment-tier coverage still completed. Reproduced end-to-end in an alpine:3.23 container mirroring the image packages: current state → exit 127, zero findings; `+python3 +jq` → exit 0, 66 YAMLs run, real MUST findings, `errors: []`. Surfaced on octopus dev during v0.3.2 selector-allowlist verification (`Seibert-Data/test-dev#1`).

## v0.3.2

- fix: restore real reviews under selector mode. The `/coding:pr-review` default dispatcher switched to selector mode (in-session classify+adjudicate, zero sub-agent spawns) in coding v0.22.0; the execution-phase `--allowedTools` allowlist (`factory.executionTools`) still assumed the old per-owner-dispatch model (`Task` + git only), so the review could not `Read` files, run the ast-grep mechanical funnel, or shell `jq`/`git rev-parse` — and the non-interactive container stalled, posting "I need your approval to proceed" as the review with a false `CHANGES_REQUESTED`. Expand `executionTools` with `Read`/`Grep`/`Glob`, `Bash(git rev-parse:*)`, `Bash(command -v:*)`, `Bash(jq:*)`, and the ast-grep runner's literal container path — all read-only, no network tools, so the anti-injection boundary holds. The assembled execution header now steers the model to invoke the runner/guide by literal path instead of the plugin's `$RUNNER`/`$GUIDE` shell variable (which an allowlist entry cannot match).

## v0.3.1

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
