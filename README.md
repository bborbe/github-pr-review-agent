# github-pr-review-agent

Autonomous GitHub / Bitbucket **PR review agent**. Given a PR URL it clones the
repo, runs a Claude Code review inside a `claude-yolo` container, and posts the
result back as a PR review with a verdict (`approve` / `request-changes`).

Part of the `bborbe` agent-maintenance fleet: the shared library lives in
[`bborbe/maintainer`](https://github.com/bborbe/maintainer) (imported as
`github.com/bborbe/maintainer`), the Helm chart ships there too, and the
published image is `docker.io/bborbe/github-pr-review-agent`. Extracted from the
former `bborbe/maintainer` monorepo (`agent/pr-reviewer`).

## How it works

1. Parse the PR URL → platform (GitHub / Bitbucket), owner, repo, PR number.
2. Fetch PR metadata (source + target branch) via the platform REST API.
3. Clone the repo at the PR head into a scratch dir.
4. Run the Claude Code review (`/pr-review <target-branch>`) inside the
   `claude-yolo` container against the diff.
5. Parse the JSON verdict from the review output.
6. Post a structured review — `APPROVE` / `REQUEST_CHANGES`, the verdict
   decides (spec 060; `autoApprove` does not gate the posted event) — and clean up the clone.

## Run modes

| Mode | Entry | Use |
|---|---|---|
| Kubernetes Job | `main.go` (`/main` in the image) | Env-driven; spawned by the agent-task-executor from a Kafka task. This is how it runs in production. |
| Local CLI | `cmd/run-task` | Reads a task file (`--task-file`), runs `--phase`, writes result back to the same file. `--skip-post` suppresses GitHub writes. |
| Local review | `cmd/cli` | Takes a PR URL positional arg; `--comment-only` posts a COMMENT without auto-approve gate. For local runs / debugging. |

```bash
go run ./cmd/run-task --task-file=/path/to/task.md --phase=execution --skip-post
go run ./cmd/cli --comment-only https://github.com/owner/repo/pull/42
```

## Configuration

Env-driven (Kubernetes) — key variables:

| Var | Purpose |
|---|---|
| `APP_ID` / `INSTALLATION_ID` | GitHub App identity (`Ben's Pull Request Reviewer`) |
| `PEM_KEY` | GitHub App private key (mounted from a Secret) |
| `REPO_ALLOWLIST` | Repos the agent may review (e.g. `github.com/bborbe/*,!github.com/bborbe/go-skeleton`) |
| `BOT_GITHUB_LOGIN` | The App's bot login, used to detect its own prior reviews |
| `REVIEW_MODE` | Review depth (e.g. `selector`) |
| `REVIEW_MAX_DURATION` | Soft time budget per Claude phase run (default `25m`, floor `60s`); an overrun routes the phase to `human_review` with a budget-naming message and salvages any streamed partial into the task's `## Salvage` section (never a retry, never posted to GitHub); keep below the K8s Job `ActiveDeadlineSeconds` |
| `REVIEW_CHUNK_ENGAGE_ADDITIONS` | Total reviewable added lines above which a PR is reviewed in chunks (default `500`, must be ≥ 1) |
| `REVIEW_CHUNK_MAX_ADDITIONS` | Maximum added lines per review chunk (default `300`, must be ≥ 1) |
| `REVIEW_CHUNK_MAX_FILES` | Maximum changed files per review chunk (default `15`, must be ≥ 1) |
| `BITBUCKET_TOKEN` | Bitbucket Server bearer token (Bitbucket PRs only) |

The target repo's `.maintainer.yaml` (`prReviewer.autoApprove`) is read from
the PR head, but the posted event is decided by the verdict alone (spec 060) —
`autoApprove` does not gate it. See
[`bborbe/maintainer`](https://github.com/bborbe/maintainer) for that schema.

## Verdict contract

The Claude Code review must emit a JSON block:

```json
{"verdict": "approve|request-changes", "reason": "<one-liner>"}
```

Fallback: a heuristic section-header scan (`## Must Fix`, `## Blocking`).
Horizontal rules (`---`) are not treated as must-fix content.

**A run that produced no review posts nothing.** When the review output has no
review content at all — an empty body, or a bare tool-error marker such as
`(tool 'WebFetch' unavailable)` — `ParseVerdict` fail-closes to `request-changes`,
but posting that would publish a blocking review the model never wrote, which blocks
the merge exactly like a real change request. `postAndRoute` suppresses the post for
exactly that case (`pkg.IsNoReviewReason(reason, body)`), records an `outcome: skipped`
diagnostic, and routes the task to `human_review`.

`no verdict block` alone is deliberately *not* the signal: `ParseVerdict` returns it
just as readily for a real review whose model omitted the JSON block — for example a
body whose only content is a `### Must Fix` section. That review did happen and still
posts its fail-closed `request-changes`; suppressing it would hide real blocking
findings. Every other fail-closed reason (funnel did not run, concerns not verified,
blocking finding present, malformed JSON, unknown verdict) likewise still posts. See
[`docs/pr-post-back.md`](docs/pr-post-back.md) § Always-Post Review Invariant, which
also records the known gap on the chunked-review path.

The image also denies `WebSearch`/`WebFetch` for every session
(`claude-config/settings.json` → `permissions.deny`), because a vLLM-backed backend
rejects the `server_tool_use` block the CLI emits when `WebSearch` runs, which is what
produced the no-verdict output above. `--allowedTools` cannot do this: it is
permission-only and never removes a tool from the toolset.

## Smoke-test PR

**https://github.com/bborbe/maintainer/pull/2** — `test: delete-this-pr-never`.
Permanent test fixture (trivial diff). Use it for any local or k8s smoke test.
**Do not close, do not merge.**

## Layout

```
.                    lib imported from github.com/bborbe/maintainer
├── main.go          Kubernetes Job entry (env-driven; /main in the image)
├── cmd/
│   ├── run-task/    local CLI (task-file driven, --skip-post available)
│   ├── cli/         PR URL driven local review CLI
│   └── mint-iat/    GitHub App installation-token smoke tool
├── pkg/             URL parse, config, git clone, GitHub/Bitbucket clients,
│                    App auth, review runner, verdict parser, posters
├── docs/            architecture.md · github-app-setup.md · pr-post-back.md
└── helm chart + shared lib live in bborbe/maintainer
```

See [`docs/architecture.md`](docs/architecture.md) for the three-phase design
(planning → execution → review) and the verdict rubric, and
[`docs/github-app-setup.md`](docs/github-app-setup.md) for the App auth flow.

## Build

```bash
make precommit          # fmt, generate, test, lint, vet, vuln, license
VERSION=vX.Y.Z make buca # build + push docker.io/bborbe/github-pr-review-agent:vX.Y.Z
```
