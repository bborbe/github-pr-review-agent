---
status: completed
summary: 'Added a best-effort COMMENT superseded notice that fires from closeMoot when a review actually ran and the PR head moved, records notified/notify_error on ## Resolution, and never blocks closure.'
execution_id: github-pr-review-agent-superseded-notice-exec-040-superseded-notice
dark-factory-version: v0.198.17
created: "2026-11-19T14:29:48Z"
queued: "2026-11-19T14:29:48Z"
started: "2026-11-19T14:38:45Z"
completed: "2026-11-19T14:46:42Z"
---

<summary>
- A review that is abandoned because the pull request's head moved now leaves a visible comment review on the PR naming the commit it ran against and the head that replaced it.
- The notice is emitted by the same closure routing that records why a review was abandoned, so it fires both from a running review and from the periodic sweep.
- It fires only when the review actually ran; a queued record closed before any pod existed posts nothing.
- It posts no verdict and cannot satisfy or block branch protection; it reuses the failure marker's endpoint and permission.
- The closure record captures whether the notice was delivered, so a failed notice is visible rather than silent.
</summary>

<objective>
Make a superseded review visible to the person waiting for it: when a PR's head moves past the commit a review was running against, the PR author currently sees an unreviewed PR with no explanation. Leave a best-effort comment review on the PR naming the commit the review ran against and the head that replaced it, without posting a verdict.
</objective>

<context>
Read `CLAUDE.md` for project conventions and build commands.
Read `docs/dod.md` § "Review-Posting Safety" for the posting rules the failure marker already established.
Read `docs/pr-post-back.md` § "Always-Post Review Invariant" and § "Failure Routing".

Read these files before changing anything:
- `pkg/pr_state_check.go` — the `PRStateClient` interface, `ResolutionOutput`, `prStateCheckCancelled`, and `closeMoot`. `closeMoot` is the single place all three moot verdicts are written, and every entry point (`steps_review.go` ×3, `steps_planning.go`, `steps_checkout_execution.go`, `moot_sweep.go`, `pr_state_watch.go`) funnels through it.
- `pkg/github/client.go` — the `Client` interface, `SubmitReview` (the `gh pr review` shape and `exec.CommandContext` pattern to mirror), and `PostComment`.
- `pkg/githubposter/poster.go` — `PostFailureMarker` and `buildFailureMarkerBody` (the bounded-body pattern to mirror). The marker is posted by `pkg/steps_checkout_execution.go:1234`; the superseded notice is NOT a second failure marker and must not be routed through `PrPoster`.
- `pkg/moot_sweep.go` — `SweepTask` calls `prStateCheck(ctx, md, s.prState)`; the sweep has a `PRStateClient` and no `PrPoster`, which is why the notifier belongs on `PRStateClient`.
- `pkg/pr_state_check_test.go` and `pkg/export_test.go` — the existing Ginkgo suite, `PRStateCheckForTest`, and the re-export pattern.
- `pkg/githubposter/export_test.go` — the re-export pattern to mirror for the new body builder.
- `CHANGELOG.md` — the top section after the preamble is currently `## v0.19.1`.

Reference this coding guide (it lives in the YOLO container, not on the host):
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`
</context>

<requirements>
1. **Extend `PRStateClient` in `pkg/pr_state_check.go`** with one method:

   ```go
   NotifySuperseded(ctx context.Context, prURL, ref, headRefOid, runID string) error
   ```

   Its doc comment must state: it posts a best-effort `COMMENT` review naming the ref the review ran against and the head that replaced it; it posts no verdict, so it cannot satisfy or block branch protection; a failure is recorded on `## Resolution` and never blocks the closure.

2. **Extend `ResolutionOutput`** in `pkg/pr_state_check.go` with two fields:

   - `Notified *bool` with json tag `notified,omitempty` — a pointer so a `false` is written rather than omitted, keeping "not attempted" (nil) distinguishable from "attempted and failed" (false).
   - `NotifyError string` with json tag `notify_error,omitempty` — the bounded failure string when `Notified` is false. Cap it at 500 bytes (truncate on a rune boundary or simply by byte slice followed by a valid-UTF-8 check) so a multi-KB stderr cannot bloat `## Resolution`.

   Merged and closed-unmerged resolutions leave both zero (`Notified` nil).

3. **Change `closeMoot`'s signature** to accept `client PRStateClient` and `prURL string`, and update its three call sites in `prStateCheckCancelled`. Its behaviour becomes:

   - For `verdict == ResolutionVerdictSuperseded` only, and only when the review actually ran, call `client.NotifySuperseded(ctx, prURL, ref, headRefOid, runID)` **before** marshalling the section.
   - "The review actually ran" means `md.Frontmatter.String("job_started_at") != ""` **or** `cancelledDuring != ""`. Use the string non-empty check, so a malformed timestamp still counts as "set".
   - `ref := md.Frontmatter.String("ref")`, `runID := md.Frontmatter.String("task_identifier")`, and `headRefOid` is the value passed in.
   - Record the outcome in `Notified` (true/false) and `NotifyError` (the capped error string, or empty).
   - A nil `client` means no notice — return without panicking. There is no separate notifier object.
   - A notify failure must be logged at `Warning` level and must **not** change the returned `Result`, the status, or the phase. The closure still happens.
   - For merged and closed-unmerged verdicts, and for a superseded record whose review never ran, no notice is attempted and `Notified` stays nil.
   - The existing idempotency guard is unchanged: a record that already carries `## Resolution` is skipped before any of this, so it is never re-notified.

4. **Implement `NotifySuperseded` on `pkg/github.Client`** (`pkg/github/client.go`), adding it to the `Client` interface and the `ghClient` struct:

   - Run `exec.CommandContext(ctx, "gh", "pr", "review", prURL, "--comment", "--body", body)` — the reviews API with `event: COMMENT`, exactly the endpoint class the failure marker uses. Do **not** use `gh pr comment` (that is the Issues API and would need a permission the App does not hold). Cancellation must flow through `ctx`, as it does in `SubmitReview`.
   - Set `GH_TOKEN` from `c.token` exactly as `SubmitReview` does, and wrap a failure with `errors.Wrapf` including the trimmed stderr, mirroring `SubmitReview`.
   - Compose the body in an unexported builder `buildSupersededNoticeBody(ref, headRefOid, runID string) string`, hard-capped at 2000 characters (mirror `maxFailureMarkerBody` / `truncateFailureMarkerBody`). The body must:
     - open with the sentence `Automated review superseded — no review was posted.`
     - state that the review ran against `<ref>` and the PR head moved to `<headRefOid>` before it finished;
     - state that the current head needs a fresh review and will be reviewed again if it is re-queued — do **not** promise unconditionally that a review will follow, since the agent cannot guarantee a re-queue;
     - end with machine-readable lines `run: <runID>`, `reviewed_ref: <ref>`, `current_head: <headRefOid>`;
     - never embed a response body, a raw error, or `## Diagnostics` content.
   - The function must be tolerant of an empty `ref`, `headRefOid` or `runID` — it must not panic and must not invent values.

5. **Regenerate the counterfeiter fakes.** `pkg/pr_state_check.go` carries the `PRStateClient` directive and `pkg/github/client.go` carries the `Client` directive; both fakes must gain `NotifySuperseded`. Use `make generate` (`go generate -mod=mod ./...`). Do not hand-edit generated files.

6. **Add Ginkgo tests** in `pkg/pr_state_check_test.go` (external `pkg_test` package, using `mocks.GitHubClient`, matching the existing style), covering all of:

   - a superseded record with `job_started_at` set → `NotifySupersededCallCount() == 1`, and the recorded `ref`/`headRefOid` arguments match;
   - a superseded record with no `job_started_at` and no `cancelled_during` → `NotifySupersededCallCount() == 0`, and `## Resolution` is still written;
   - a superseded record cancelled mid-run (`cancelled_during` set, `job_started_at` empty) → `NotifySupersededCallCount() == 1`;
   - a merged PR → `NotifySupersededCallCount() == 0`;
   - a closed-unmerged PR → `NotifySupersededCallCount() == 0`;
   - a record that already carries `## Resolution` → `NotifySupersededCallCount() == 0`;
   - `NotifySuperseded` returning an error → the result is still `AgentStatusDone` with `NextPhase: "done"`, status is still written, and the `## Resolution` carries `notified: false` with a non-empty `notify_error`;
   - a successful notice → `## Resolution` carries `notified: true`.

7. **Add contract tests for the new boundaries**, which the unit rows above cannot reach:

   - `pkg/github/export_test.go` — re-export `buildSupersededNoticeBody` (mirror `pkg/githubposter/export_test.go`). Add a table test in `pkg/github/client_test.go` covering: empty inputs (no panic, no invented values), the required opening sentence, the three machine-readable keys, and the 2000-character cap with an over-long `runID`.
   - `pkg/github/client_test.go` — add the `NotifySuperseded` error-path and context-cancellation tests in the same style `SubmitReview` already uses, with the same NOTE documenting that the success-path argv is not exercised without executor injection.

8. **Amend `docs/dod.md` § "Review-Posting Safety"** so the comment-only exception bullet also names the superseded notice: a `COMMENT` review carrying no verdict is permitted for the existing comment-only paths, the failure marker, **and the superseded notice**; it states the notice is best-effort, carries no verdict, never changes routing/phase/status, and is not a demotion of the verdict path.

9. **Add a "Superseded notice" section to `docs/pr-post-back.md`** describing: the trigger (`OPEN` + `ref != headRefOid`, and the review ran); the delivery (`gh pr review --comment`, i.e. `POST /repos/{owner}/{repo}/pulls/{n}/reviews` with `event: COMMENT` — not the Issues API); the bounded body and its machine-readable keys; the fact it fires from both the in-pod path and the moot-sweep; that a failure is recorded as `notified: false` on `## Resolution` and never blocks the closure; and that it posts no verdict, so branch protection is unaffected.

10. **Add a `## Unreleased` entry to `CHANGELOG.md`** — heading exactly `## Unreleased`, below the preamble and above `## v0.19.1`, one bullet in the file's existing style describing the superseded notice.
</requirements>

<constraints>
- Do NOT use the Issues API (`gh pr comment`) in the notice path — the notice must be a `COMMENT` review so it needs only the App's existing `pull_requests:write`. The pre-existing `PostComment` helper stays untouched.
- Do NOT route the notice through `PrPoster` or reuse `PostFailureMarker` — the moot-sweep has no poster, and the notice is not a failure marker.
- Do NOT change `prStateCheck`'s exported signature or the six existing call sites — only `closeMoot` gains parameters, and it is unexported.
- Do NOT post a notice for merged or closed-unmerged verdicts.
- Do NOT let a notice failure change the `Result`, the status, the phase, or the returned error.
- Do NOT re-notify a record that already carries `## Resolution`.
- Do NOT hand-edit files under `mocks/`.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
- `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).
- `for f in pkg/pr_state_check.go pkg/github/client.go mocks/pr-state-client.go mocks/github-client.go; do grep -q 'NotifySuperseded' "$f" || exit 1; done` — the method exists in the interface, the implementation and both fakes.
- `! sed -n '/func (c \*ghClient) NotifySuperseded/,/^}/p' pkg/github/client.go | grep -q 'gh pr comment'` — the notice path does not touch the Issues API. Scoped to the new function because the pre-existing `PostComment` helper's error string contains `gh pr comment` and must stay untouched.
- `grep -q 'notified' pkg/pr_state_check.go` — the resolution records the delivery outcome.
- `for f in docs/dod.md docs/pr-post-back.md; do grep -q 'superseded notice' "$f" || exit 1; done` — both docs name it.
- `grep -c '^## Unreleased$' CHANGELOG.md` — must print `1`.
- `go test ./pkg/... -run TestSuite` — the suite entry is `TestSuite` (`pkg/pkg_suite_test.go`); the new cases pass.
- Before finishing, re-run `<verification>` and confirm it passes; walk each acceptance criterion against the change.
</verification>
