---
status: completed
spec: [bug-nuke-dev-gx]
summary: Normalized task identifiers (trim + lowercase) before UUID validation and worktree-path construction in EnsureWorktree, with tests for uppercase, whitespace, empty, and malformed IDs plus a CHANGELOG entry.
execution_id: a28a85e4-ee1e-5f87-a2d0-c9ac512354b5-exec-037-fix-canonicalize-task-id
dark-factory-version: dev
created: "2026-11-17T17:59:55Z"
queued: "2026-11-17T18:06:24Z"
started: "2026-11-17T18:06:26Z"
completed: "2026-11-17T18:18:43Z"
---

<summary>
- A task whose identifier is an uppercase UUID no longer aborts before any git work runs.
- The identifier is normalized to the canonical lowercase form before it is validated.
- A retrigger of the same task with different casing reuses the same worktree instead of failing or creating a second one.
- Malformed, empty, and path-traversal identifiers are still rejected.
- The lowercase-only UUID check remains in place as a second line of defense.
- Existing worktree behaviour for lowercase identifiers is unchanged.
- A changelog entry records the fix.
</summary>

<objective>
Sentry issue `NUKE-DEV-GX`: `EnsureWorktree` reads the task identifier verbatim from task frontmatter and rejects any uppercase UUID at `pkg/git/repo_manager.go:152` before cloning or creating a worktree. Canonicalize the identifier (trim whitespace, lowercase) before validation and before the worktree path is built, keep the lowercase UUID regex as the defense-in-depth gate, and cover uppercase / malformed / empty identifiers with tests.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Go, Ginkgo v2 / Gomega, external `*_test` packages, `github.com/bborbe/errors`, `glog`).

Read these files before changing anything:
- `pkg/git/repo_manager.go` — the defect site. `taskIDRegexp` (line ~94) is lowercase-only; `EnsureWorktree` (line ~144) validates `taskID` at ~152 and builds `worktreePath := filepath.Join(r.cfg.WorkPath, taskID)` at ~161. `strings` is already imported.
- `pkg/git/repo_manager_test.go` — the existing `Describe("EnsureWorktree")` block, the `taskID` / `taskID2` constants, the `testOrigin` helper, and the existing `Context("invalid taskID")` case.
- `pkg/git/workdir_config.go` — documents the worktree layout `<WorkPath>/<task_id>`.
- `pkg/steps_checkout_execution.go` — `extractRequiredFrontmatter` (~line 1316) reads `task_identifier` from frontmatter verbatim; `ensureWorktree` (~line 165) passes it straight to `RepoManager.EnsureWorktree`. This is why the raw value can be uppercase.
- `CHANGELOG.md` — currently has no `## Unreleased` section; it runs from the preamble straight to `## v0.18.2`.
- `docs/dod.md` — requires a `CHANGELOG.md` entry under `## Unreleased`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 / Gomega conventions (Describe / Context / It / DescribeTable, `GinkgoT().TempDir()`).
</context>

<requirements>
1. **Normalize the task ID at the top of `EnsureWorktree` in `pkg/git/repo_manager.go`.** Keep the existing `isValidBranchName(ref)` check first, unchanged. Immediately after it, replace the current raw regex check with:

   ```go
   // Task frontmatter carries the task_identifier verbatim (Sentry NUKE-DEV-GX:
   // an uppercase UUID), while the worktree layout and taskIDRegexp are
   // canonical lowercase. Normalize before validating and before the path join;
   // the regex stays as the defense-in-depth gate for malformed / empty /
   // traversal input.
   rawTaskID := taskID
   taskID = strings.ToLower(strings.TrimSpace(taskID))
   if !taskIDRegexp.MatchString(taskID) {
       return "", errors.Errorf(ctx, "invalid task ID (must be UUID): %s", rawTaskID)
   }
   ```

   The error message must keep the exact prefix `invalid task ID (must be UUID): ` and must print the original (`rawTaskID`) value, so an operator sees exactly what the frontmatter carried. Do not change the existing `isValidBranchName` error.

2. **Leave `taskIDRegexp` unchanged** — it stays the lowercase-only `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` defense-in-depth gate. Only case and surrounding whitespace are normalized; a non-hyphenated, `urn:uuid:`-prefixed, or otherwise non-canonical identifier must still be rejected.

3. **Build the worktree path from the canonical value.** `worktreePath := filepath.Join(r.cfg.WorkPath, taskID)` now receives the lowercased, trimmed value — no other change in the function. Do not introduce a second join or a separate path variable.

4. **Do not change the `RepoManager` interface or any caller.** `EnsureWorktree(ctx context.Context, cloneURL, ref, taskID string) (string, error)` keeps its signature. `pkg/steps_checkout_execution.go` keeps passing the raw frontmatter value. Because the interface is unchanged, `mocks/repo-manager.go` must not change.

5. **Do not add a `github.com/google/uuid` dependency.** It is currently an indirect dependency; importing it would require `go mod tidy` + `go mod vendor` churn, and `uuid.Parse` accepts non-canonical inputs (no-hyphen form, `urn:uuid:` prefix, braces) — a wider accepted-input surface than this bug needs. `strings` is already imported, so `strings.ToLower(strings.TrimSpace(...))` is the minimal fix.

6. **Add tests in `pkg/git/repo_manager_test.go`** inside the existing `Describe("EnsureWorktree")` block. Use the real `manager` built from `NewRepoManager` and the real `origin` from `newTestOrigin()` — these are real git operations in temp dirs, no mocks. Add:

   a. **Uppercase UUID (the Sentry value).** Call `EnsureWorktree(ctx, origin.URL, "feature-branch", "9B4C7E12-6D3A-4F58-A1C9-3E7B5D2F8A64")`; assert no error, the returned path equals `filepath.Join(workDir, "9b4c7e12-6d3a-4f58-a1c9-3e7b5d2f8a64")`, the directory exists, and `git -C <path> rev-parse --abbrev-ref HEAD` prints `feature-branch` (proves a real worktree was created at the canonical path, not merely an empty dir).

   b. **Case-insensitive idempotency.** Create the worktree with the lowercase form, then call again with `strings.ToUpper(...)` of the same ID; assert both calls return the same path and no error. This is the retrigger path that used to abort.

   c. **Whitespace tolerance.** Call with `"  " + taskID + "\n"` (the existing lowercase `taskID` constant) and assert the returned path equals `filepath.Join(workDir, taskID)` with no error.

   d. **Empty identifier.** Call with `""`; assert an error containing `invalid task ID` and that no directory was created under `workDir`.

   e. **Malformed identifiers.** Add a `DescribeTable` (or equivalent) over `""`, `"NOT-A-UUID"`, `"../../etc/passwd"`, `"9b4c7e12-6d3a-4f58-a1c9"` (truncated), and `"9b4c7e12-6d3a-4f58-a1c9-3e7b5d2f8a6z"` (non-hex); each must return an error containing `invalid task ID`.

   Keep the existing `Context("invalid taskID")` with `"not-a-uuid"` intact.

7. **Add the CHANGELOG entry.** Create `## Unreleased` directly below the preamble block and directly above `## v0.18.2` — never between the `# Changelog` title and the preamble. Add one `fix:` bullet stating that an uppercase `task_identifier` from task frontmatter is now normalized to the canonical lowercase form before UUID validation and worktree-path construction, so the run no longer aborts before cloning, while malformed / empty identifiers are still rejected by the unchanged lowercase regex.

8. **Do not change the README or `docs/`.** This alters neither a run mode, a configuration field, nor verdict behaviour, and the documented worktree layout `<WorkPath>/<task_id>` is unchanged.
</requirements>

<constraints>
- An uppercase UUID read from task frontmatter must not abort the run; it must be canonicalized before validation and before the worktree path join.
- The lowercase-only regex stays in place as defense-in-depth — do not delete it or loosen it to `[0-9a-fA-F]`.
- Malformed, empty, and path-traversal identifiers must still be rejected.
- Normalization is case and surrounding whitespace only — do not broaden the accepted UUID format.
- Errors use `github.com/bborbe/errors` with context wrapping (no `fmt.Errorf`, no bare `return err`).
- Tests use Ginkgo v2 / Gomega in the external `git_test` package.
- The `RepoManager` interface and `mocks/repo-manager.go` must not change.
- Do not add `github.com/google/uuid` or otherwise change `go.mod` / `vendor/`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).
- `go test ./pkg/git/... -count=1` — must pass, including the new uppercase / malformed / empty cases.
- `grep -n 'taskIDRegexp' pkg/git/repo_manager.go` — must still show the regex declared and used in `EnsureWorktree` (not removed).
- `grep -n 'strings.ToLower(strings.TrimSpace(taskID))' pkg/git/repo_manager.go` — must print one line.
- `grep -c '9B4C7E12-6D3A-4F58-A1C9-3E7B5D2F8A64' pkg/git/repo_manager_test.go` — must be at least 1.
- `grep -n '## Unreleased' CHANGELOG.md` — must print one line above `## v0.18.2`.
</verification>

<!--
Open questions for the human reviewer (resolved in the requirements above, surfaced here for audit):

1. Canonicalization scope: only case and surrounding whitespace are normalized. Non-hyphenated
   32-hex UUIDs, `urn:uuid:` prefixes, and brace-wrapped forms are still rejected. If the task
   controller can emit any of those, this fix will not cover them and the regex gate is the wrong
   place to widen — flag it before approving.
2. `uuid.Parse` was considered (the spec's alternative) and rejected: it would add a direct
   dependency plus vendor churn and would widen the accepted input surface. If the maintainer
   prefers canonical re-serialization via `uuid.Parse(...).String()`, that is a different change.
3. The raw (possibly uppercase) identifier is still what callers log in `steps_checkout_execution.go`
   auth-failure diagnostics; only the worktree path is canonical. This keeps the logs faithful to
   the frontmatter. If log/task correlation needs the canonical form, that is a follow-up.
4. The spec names only `pkg/git/repo_manager.go`; no other site validates a task identifier
   (`extractRequiredFrontmatter` reads it but does not validate), so no caller change is included.
-->
