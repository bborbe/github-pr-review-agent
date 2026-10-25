---
status: completed
summary: Passed egress proxy variables through to token-bearing git subprocesses in cmdEnv, with slice and subprocess contract tests plus a changelog entry
execution_id: github-pr-review-agent-proxyenv-exec-030-fix-git-proxy-env-for-token-path
dark-factory-version: v0.198.7
created: "2026-10-25T22:20:00Z"
queued: "2026-10-25T20:45:08Z"
started: "2026-10-25T20:46:48Z"
completed: "2026-10-25T20:50:55Z"
---

# Keep the proxy environment on every git command that runs with a token

<summary>
- Git commands lose every proxy setting whenever a GitHub token is present, so a sandboxed review job cannot reach the network even though its own API calls succeed.
- The cause is the helper that builds the environment for git children: with a token set it keeps only three variables, and the proxy variables are not among them.
- With no token the child inherits everything, which is why the same image clones in one configuration and fails in the other.
- After the fix the child keeps its restricted environment and additionally receives the proxy settings when the agent process has them.
- A proxy setting that is unset stays absent instead of being passed as an empty value.
- The no-token path is unchanged.
- Tests pin all three behaviours: proxy carried with a token, no empty proxy keys when unset, unchanged when there is no token.
- The changelog records the bug and the fact that every token-bearing git operation was affected.
- The full pre-commit suite stays green.
</summary>

<objective>
Git operations that run with a GitHub token must honour the pod's egress proxy again, without widening the environment the agent hands to its git children. A sandbox that admits only the proxy must be able to clone with a token exactly as it does without one.
</objective>

<context>
Read `CLAUDE.md` for project conventions (build/test targets, changelog policy, `make precommit`).

Read these files:
- `pkg/git/repo_manager.go` — `(r *repoManager) cmdEnv()` at ~line 64 is the bug: it returns `nil` when `ghToken == ""` and otherwise a three-entry allowlist (`HOME`, `PATH`, `GH_TOKEN`). All four git call sites in the file (`cloneBare`, `EnsureWorktree`, `PruneAllWorktrees`, `runGitCmd`) assign `cmd.Env = r.cmdEnv()`.
- `pkg/git/export_test.go` — `CmdEnv(m RepoManager) []string` exposes `cmdEnv()` to the external test package. Use it; do not add a new export.
- `pkg/git/repo_manager_test.go` — the existing `Describe("cmdEnv (subprocess env for git invocations)")` block (~line 219) already covers the nil-token and token cases. Extend it.
- `docs/dod.md` — the `## Unreleased` placement rule and the changelog requirement.
- `CHANGELOG.md` — currently has no `## Unreleased` section; the preamble ends before `## v0.14.3`.
- `pkg/githubauth/github_auth_setup.go` — `defaultExecFunc` (~line 91) carries the same three-variable allowlist that `cmdEnv`'s comment names as its mirror. Leave it unchanged: its only caller is `gh auth setup-git`, which makes no network call, so the proxy gap is benign there. Do not extract a shared helper in this prompt.
</context>

<requirements>
1. In `pkg/git/repo_manager.go`, extend `(r *repoManager) cmdEnv()`. Keep `if r.ghToken == "" { return nil }` exactly as it is. In the token branch keep the existing three entries (`HOME`, `PATH`, `GH_TOKEN`) and additionally append, for each of `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `http_proxy`, `https_proxy`, `no_proxy`, a `KEY=value` entry only when `os.Getenv(KEY)` is non-empty. An unset variable must produce no entry at all — never `KEY=`.
2. Update the two doc comments that now understate the environment: the `cmdEnv` comment (~lines 56-63, currently "returns the allowlisted env for git subprocesses: HOME ..., PATH ..., GH_TOKEN ...") and the env sentence in `NewRepoManager`'s comment (~lines 37-46, currently "ghToken is exported as GH_TOKEN in every git subprocess env"). Both must name the proxy pass-through and keep the existing rationale (unrelated pod secrets stay out).
3. Do not touch any call site — `cloneBare`, `EnsureWorktree`, `PruneAllWorktrees` and `runGitCmd` already assign `cmd.Env = r.cmdEnv()`. Do not change `pkg/git/git.go`: its `worktreeManager` sets no `cmd.Env`, so it inherits the full parent environment and is not affected by this bug.
4. Extend the existing `Describe("cmdEnv (subprocess env for git invocations)")` block in `pkg/git/repo_manager_test.go` (external package `git_test`, Ginkgo v2 / Gomega, `git.CmdEnv(m)`) with three cases:
   - token set and `HTTPS_PROXY` + `https_proxy` present: the slice contains both values verbatim, and still contains `HOME=`, `PATH=` and `GH_TOKEN=<token>`.
   - token set and no proxy variable present: no entry starts with any of the six proxy key names (not even `KEY=`).
   - no token: the return is still `nil` (already covered — keep it green).
   Set the process environment with `GinkgoT().Setenv(...)`, which restores the previous value automatically; do not use a bare `os.Setenv` without cleanup, since specs in one process share the environment.
5. Add a subprocess contract check that the slice is actually what a child receives: with `cmd := exec.CommandContext(ctx, "sh", "-c", "echo \"$HTTPS_PROXY\"")` and `cmd.Env = git.CmdEnv(m)` for a token-bearing manager, the output must equal the proxy value. The slice assertions in requirement 4 are necessary but not sufficient — this is the case that proves the child sees it.
6. Add the changelog entry per `docs/dod.md`: create `## Unreleased` below the preamble block and above the newest `## vX.Y.Z` section — never between the `# Changelog` title and the preamble. The bullet names the bug and the blast radius: every git operation that ran with a token ignored the egress proxy, so a sandbox that admits only the proxy refused the clone in milliseconds while the same pod's API calls succeeded.
7. Self-check before finishing: re-run the verification command, then walk requirements 1-6 against the diff.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT widen the child environment to `os.Environ()` or `append(os.Environ(), ...)`; that is exactly the hardening `cmdEnv` exists to prevent.
- Do NOT change the `ghToken == ""` → `nil` contract.
- Keep the change to `pkg/git/repo_manager.go`, `pkg/git/repo_manager_test.go` and `CHANGELOG.md`. No new dependency.
- Existing tests must still pass.
- Out of scope: a proxy that terminates TLS with a private CA would additionally need `SSL_CERT_FILE` / `CURL_CA_BUNDLE` / `GIT_SSL_CAINFO`. The observed failure was a connect refusal, so do not add them here.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license). The repo runs `workflow: direct` with no `hideGit`, so `.git` is present in the container and `make` resolves `ROOTDIR` normally.
</verification>
