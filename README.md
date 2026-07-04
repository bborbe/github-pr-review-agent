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
6. Post a structured review — `APPROVE` / `REQUEST_CHANGES` (gated by
   `autoApprove`) or a plain `COMMENT` — and clean up the clone.

## Run modes

| Mode | Entry | Use |
|---|---|---|
| Kubernetes Job | `main.go` (`/main` in the image) | Env-driven; spawned by the agent-task-executor from a Kafka task. This is how it runs in production. |
| Local CLI | `cmd/run-task` | Takes a PR URL as a positional arg; flag-based config. For local runs / debugging. |

```bash
go run ./cmd/run-task https://github.com/owner/repo/pull/42
go run ./cmd/run-task -v --comment-only https://github.com/owner/repo/pull/42
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
| `BITBUCKET_TOKEN` | Bitbucket Server bearer token (Bitbucket PRs only) |

Per-repo behavior is driven by the target repo's `.maintainer.yaml`
(`prReviewer.autoApprove`) — read from the PR head. See
[`bborbe/maintainer`](https://github.com/bborbe/maintainer) for that schema.

## Verdict contract

The Claude Code review must emit a JSON block:

```json
{"verdict": "approve|request-changes", "reason": "<one-liner>"}
```

Fallback: a heuristic section-header scan (`## Must Fix`, `## Blocking`).
Horizontal rules (`---`) are not treated as must-fix content.

## Smoke-test PR

**https://github.com/bborbe/maintainer/pull/2** — `test: delete-this-pr-never`.
Permanent test fixture (trivial diff). Use it for any local or k8s smoke test.
**Do not close, do not merge.**

## Layout

```
.                    lib imported from github.com/bborbe/maintainer
├── main.go          Kubernetes Job entry (env-driven; /main in the image)
├── cmd/
│   ├── run-task/    local CLI (PR URL as positional arg)
│   ├── cli/         supporting CLI
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
