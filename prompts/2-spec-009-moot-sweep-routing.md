---
status: draft
spec: [009-moot-sweep]
created: "2026-10-28T18:22:00Z"
branch: dark-factory/moot-sweep
---

# Close a moot task through the shipped routing, stamped and attributable

<summary>
- A new sweep component decides, per tracked task, whether the task is moot, and closes it when it is.
- The decision is made by the routing that already ships — it is called, never copied.
- A task whose commit is no longer the pull request's head closes as aborted with a superseded verdict naming the observed head.
- A merged pull request closes as completed with a merged verdict; a closed-unmerged pull request closes as completed with a closed-unmerged verdict.
- A task whose commit is still the head is never closed, and its stored record is not touched.
- A failure to reach GitHub closes nothing and leaves the task tracked for the next pass.
- Every closure records which closer did it and whether a job had already been assigned, so the two costs stay separable.
- The in-pod path is not modified and does not start writing those two fields.
- One pass over a mixed task set writes only the tasks it closes.
</summary>

<objective>
Create `pkg/moot_sweep.go`: the decision-and-write half of the pod-less moot closer. It converts a tracked task-event payload into a task document, applies the existing `prStateCheck` routing unchanged, stamps every closure with `closed_by` and `spawn_state`, and publishes the closed record through the same CQRS command path the reviewer pod already uses. It is prompt 2 of 3; prompt 1 shipped the tracker, prompt 3 wires the entrypoint.
</objective>

<context>
Read `CLAUDE.md` for project conventions (error wrapping with `github.com/bborbe/errors`, `glog` with `V(n)`, `make precommit`).

Read these files fully:
- `pkg/pr_state_check.go` — the routing you call. `func prStateCheck(ctx context.Context, md *agentlib.Markdown, client PRStateClient) (*agentlib.Result, error)` is unexported and lives in `package pkg`, which is why this file must live in `package pkg` too. It returns a non-nil `*agentlib.Result` only when it decided the review is moot, and by then it has already written `status`, `phase: done` and the `## Resolution` section into the markdown. On a GitHub failure it returns `(nil, nil)` — a no-op. It is idempotent on an already-terminal task.
- `pkg/pr_state_check_test.go` — the routing's own tests; use them as the reference for what each branch writes.
- `pkg/moot_tracker.go` — prompt 1's `MootTracker` (`Observe`, `Remove`, `List`). `SweepPass` reads from it.
- `pkg/pr_state_watch.go` — house style for a small unexported struct with `libtime.CurrentDateTimeGetter` and `glog`.
- `mocks/pr-state-client.go` — the counterfeiter fake `mocks.PRStateClient` (`PRStateReturns(state, mergedAt, headRefOid, err)`).
- `CHANGELOG.md` — read only; this prompt writes no changelog entry (prompt 3 does).

Verified library contracts you may rely on (do not re-derive them from training data):
- `agentlib "github.com/bborbe/agent"`:
  - `ParseMarkdown(ctx context.Context, content string) (*Markdown, error)`; `(*Markdown).Marshal(ctx context.Context) (string, error)`; `type Markdown struct { Frontmatter TaskFrontmatter; Preamble string; Sections []Section }`
  - `ExtractSection[T any](ctx context.Context, md *Markdown, heading string) (*T, error)` — used by the tests to read `## Resolution`
  - `type Task struct { base.Object[base.Identifier]; TaskIdentifier TaskIdentifier; Vault string; Frontmatter TaskFrontmatter; Content TaskContent }`
  - `TaskV1SchemaID` (`cdb.SchemaID`, group `agent`, kind `task`, version `v1`)
- `delivery "github.com/bborbe/agent/delivery"`: `ParseMarkdownFrontmatter(content string) (map[string]any, string)` splits a marshalled document back into frontmatter map + body.
- `base "github.com/bborbe/cqrs/base"`: `Object[I ObjectIdentifier]{Identifier I; Created libtime.DateTime; Modified libtime.DateTime}`; `type Identifier string`; `NewRequestID() RequestID`; `NewCommandCreator(requestIDChan <-chan RequestID) CommandCreator` with `NewCommand(operation CommandOperation, initiator iam.Initiator, id EventID, data Event) Command`; `UpdateOperation`; `ParseEvent(ctx, value) (Event, error)`.
- `cdb "github.com/bborbe/cqrs/cdb"`: `type CommandObject struct { Command base.Command; SchemaID SchemaID }`; `type CommandObjectSender interface { SendCommandObject(ctx context.Context, commandObject CommandObject) error; SendCommandObjects(ctx context.Context, commandObjects CommandObjects) error }`.
- `cqrsiam "github.com/bborbe/cqrs/iam"`: `type Initiator string`.
- `libtime "github.com/bborbe/time"`: `CurrentDateTimeGetter.Now() libtime.DateTime`.
- `agentlib.TaskFrontmatter.CurrentJob() string` returns the K8s Job name the executor records in the `current_job` frontmatter field when it publishes its spawn notification. Empty means no job was ever assigned to this task.

The write path is the one the reviewer's own pod uses — `delivery/kafkaResultDeliverer.DeliverResult` in `github.com/bborbe/agent@v0.129.0/delivery/result-deliverer.go` (read it, ~lines 195-230): build an `agentlib.Task`, `base.ParseEvent` it, and send a `cdb.CommandObject{Command: <update command>, SchemaID: agentlib.TaskV1SchemaID}` through a `cdb.CommandObjectSender`. Do not invent a second write mechanism.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-cqrs.md` — command-object sender idiom
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `errors.Wrapf`/`errors.Wrap`, never `fmt.Errorf`, never `context.Background()` inside `pkg/`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` — non-blocking `select` on `ctx.Done()` in a loop that does I/O per iteration
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo table tests, external test package
</context>

<requirements>
1. Create `pkg/moot_sweep.go` in `package pkg` with the standard license header.

2. Add the fixed tick constant. It is a compile-time constant, never a config field:
   ```go
   // MootSweepInterval is the fixed interval between sweep passes. Five minutes
   // is fixed by the criterion the closer serves; it is deliberately not
   // configurable.
   const MootSweepInterval = 5 * time.Minute
   ```

3. Define the sweep interface, constructor, and private struct:
   ```go
   // MootSweep applies the shipped PR-state routing to tracked review tasks and
   // closes the ones that are moot.
   type MootSweep interface {
       // SweepTask evaluates one tracked task. It returns true when the task was
       // closed (and the closure was published), false when the task is still
       // current or the routing could not reach GitHub.
       SweepTask(ctx context.Context, task agentlib.Task) (bool, error)
       // SweepPass evaluates every task the tracker currently holds and returns
       // the ids it closed, in the order it closed them. A failure on one task
       // is logged and skipped; it never aborts the pass.
       SweepPass(ctx context.Context) ([]agentlib.TaskIdentifier, error)
   }

   // NewMootSweep returns a MootSweep that routes through prStateCheck and
   // publishes closures through sender.
   func NewMootSweep(
       tracker MootTracker,
       prState PRStateClient,
       sender cdb.CommandObjectSender,
       currentDateTime libtime.CurrentDateTimeGetter,
   ) MootSweep {
       return &mootSweep{
           tracker:         tracker,
           prState:         prState,
           sender:          sender,
           currentDateTime: currentDateTime,
       }
   }

   type mootSweep struct {
       tracker         MootTracker
       prState         PRStateClient
       sender          cdb.CommandObjectSender
       currentDateTime libtime.CurrentDateTimeGetter
   }
   ```
   Do NOT add a `//counterfeiter:generate` annotation for `MootSweep`: prompt 3 uses the concrete constructor and the tests use the real sweep with a faked `PRStateClient` and a faked `cdb.CommandObjectSender`.

4. Add the markdown builder. The event's `Content` is the markdown body **after** the frontmatter by contract, so parse the body and attach the event's frontmatter — and COPY the map, because the tracker hands out the stored task and mutating its map in place would corrupt the tracked record:
   ```go
   func buildMootMarkdown(ctx context.Context, task agentlib.Task) (*agentlib.Markdown, error) {
       md, err := agentlib.ParseMarkdown(ctx, string(task.Content))
       if err != nil {
           return nil, errors.Wrapf(ctx, err, "parse task content")
       }
       fm := make(agentlib.TaskFrontmatter, len(task.Frontmatter))
       for k, v := range task.Frontmatter {
           fm[k] = v
       }
       md.Frontmatter = fm
       return md, nil
   }
   ```

5. Add the spawn-state helper:
   ```go
   // mootSpawnState reports whether a job had already been assigned when the
   // task was closed: "job_assigned" when the executor recorded a current job,
   // "none" otherwise.
   func mootSpawnState(task agentlib.Task) string
   ```
   Returns `"job_assigned"` when `task.Frontmatter.CurrentJob() != ""`, else `"none"`.

6. Implement `SweepTask` in this exact order:
   ```go
   func (s *mootSweep) SweepTask(ctx context.Context, task agentlib.Task) (bool, error) {
       md, err := buildMootMarkdown(ctx, task)
       if err != nil {
           return false, err
       }
       result, err := prStateCheck(ctx, md, s.prState)
       if err != nil {
           return false, err
       }
       if result == nil {
           glog.V(2).Infof(
               "moot-sweep: task=%s still current — not closed", task.TaskIdentifier,
           )
           return false, nil
       }
       if err := s.writeClosure(ctx, task, md); err != nil {
           return false, err
       }
       glog.V(2).Infof(
           "moot-sweep: closed task=%s spawn_state=%s",
           task.TaskIdentifier,
           mootSpawnState(task),
       )
       return true, nil
   }
   ```
   The `prStateCheck(ctx, ...)` call must be a plain call in code, on its own line, in this file — the spec's evidence command greps `pkg/moot_sweep.go` for it. Do not wrap it in a helper in another file and do not duplicate the routing.

7. Implement `writeClosure` — the stamp plus the existing command path:
   ```go
   func (s *mootSweep) writeClosure(
       ctx context.Context,
       task agentlib.Task,
       md *agentlib.Markdown,
   ) error {
       md.Frontmatter["closed_by"] = "sweep"
       md.Frontmatter["spawn_state"] = mootSpawnState(task)

       full, err := md.Marshal(ctx)
       if err != nil {
           return errors.Wrap(ctx, err, "marshal task")
       }
       fmMap, body := delivery.ParseMarkdownFrontmatter(full)

       now := s.currentDateTime.Now()
       updated := agentlib.Task{
           Object: base.Object[base.Identifier]{
               Identifier: base.Identifier(base.NewRequestID()),
               Created:    now,
               Modified:   now,
           },
           TaskIdentifier: task.TaskIdentifier,
           Vault:          task.Vault,
           Frontmatter:    agentlib.TaskFrontmatter(fmMap),
           Content:        agentlib.TaskContent(body),
       }

       event, err := base.ParseEvent(ctx, updated)
       if err != nil {
           return errors.Wrap(ctx, err, "parse task event")
       }
       requestIDCh := make(chan base.RequestID, 1)
       requestIDCh <- base.NewRequestID()
       commandObject := cdb.CommandObject{
           Command: base.NewCommandCreator(requestIDCh).NewCommand(
               base.UpdateOperation,
               cqrsiam.Initiator("agent"),
               "",
               event,
           ),
           SchemaID: agentlib.TaskV1SchemaID,
       }
       if err := s.sender.SendCommandObject(ctx, commandObject); err != nil {
           return errors.Wrap(ctx, err, "send task update")
       }
       return nil
   }
   ```
   Notes: the new `Identifier` mirrors what the reviewer's deliverer does (a fresh id per published command). `base.NewRequestID()` is used instead of importing `github.com/google/uuid`, which stays an indirect dependency.

8. Implement `SweepPass`:
   ```go
   func (s *mootSweep) SweepPass(ctx context.Context) ([]agentlib.TaskIdentifier, error) {
       tasks := s.tracker.List()
       closed := make([]agentlib.TaskIdentifier, 0, len(tasks))
       for _, task := range tasks {
           select {
           case <-ctx.Done():
               return closed, ctx.Err()
           default:
           }
           wasClosed, err := s.SweepTask(ctx, task)
           if err != nil {
               glog.V(2).Infof(
                   "moot-sweep: task=%s skipped err=%v", task.TaskIdentifier, err,
               )
               continue
           }
           if !wasClosed {
               continue
           }
           s.tracker.Remove(task.TaskIdentifier)
           closed = append(closed, task.TaskIdentifier)
       }
       return closed, nil
   }
   ```
   A task is removed from the tracker only after its closure was published successfully. A GitHub error, a marshal error, or a send error leaves the task tracked for the next pass.

9. Write `pkg/moot_sweep_test.go` in `package pkg_test`. Fixtures (keep these exact values — the routing matches the PR URL in the preamble and the `ref` in the frontmatter):
   ```go
   const (
       sweepPRURL = "https://github.com/bborbe/maintainer/pull/2"
       sweepRef   = "abc123"
       sweepHead  = "def456"
   )

   func newSweepTask(id, currentJob string) agentlib.Task {
       return agentlib.Task{
           TaskIdentifier: agentlib.TaskIdentifier(id),
           Vault:          "maintainer",
           Frontmatter: agentlib.TaskFrontmatter{
               "assignee":    "pr-reviewer-agent",
               "status":      "in_progress",
               "phase":       "execution",
               "ref":         sweepRef,
               "current_job": currentJob,
           },
           Content: agentlib.TaskContent(sweepPRURL + "\n\nsome content\n"),
       }
   }
   ```
   Build the sweep with `pkg.NewMootSweep(tracker, fakePRState, fakeSender, libtime.NewCurrentDateTime())`, where `fakePRState = &mocks.PRStateClient{}` and `fakeSender = &cqrsmocks.CDBCommandObjectSender{}` (import `cqrsmocks "github.com/bborbe/cqrs/mocks"`; the local package is also called `mocks`, so alias it).

   a. `DescribeTable("routing", ...)` with row function
   `func(state, head, mergedAt string, expectClosed bool, expectStatus, expectVerdict string)`.
   Every `Entry` passes all six values; the not-closed row passes empty strings for the last two.
   - `("OPEN", sweepRef, "", false, "", "")` → not closed: the head equals the fixture's `ref: abc123`, so `SweepTask` returns `(false, nil)` and `fakeSender.SendCommandObjectCallCount()` is `0`
   - `("OPEN", "999999", "", true, "aborted", "superseded")` → closed; the resolution's `head_ref_oid` is `999999`
   - `("MERGED", sweepHead, "2026-10-28T10:00:00Z", true, "completed", "merged")` → closed
   - `("CLOSED", sweepHead, "", true, "completed", "closed_unmerged")` → closed
   For every closed row assert, on the single `fakeSender.SendCommandObjectArgsForCall(0)` command object:
   ```go
   Expect(commandObject.SchemaID).To(Equal(agentlib.TaskV1SchemaID))
   Expect(commandObject.Command.Operation).To(Equal(base.UpdateOperation))
   var written agentlib.Task
   Expect(commandObject.Command.Data.MarshalInto(ctx, &written)).To(Succeed())
   Expect(written.TaskIdentifier).To(Equal(agentlib.TaskIdentifier("task-1")))
   Expect(written.Vault).To(Equal("maintainer"))
   Expect(written.Frontmatter["status"]).To(Equal(expectedStatus))
   Expect(written.Frontmatter["phase"]).To(Equal("done"))
   Expect(written.Frontmatter["closed_by"]).To(Equal("sweep"))
   ```
   and read the resolution back through the shipped extractor:
   ```go
   writtenMD, err := agentlib.ParseMarkdown(ctx, string(written.Content))
   Expect(err).NotTo(HaveOccurred())
   resolution, err := agentlib.ExtractSection[pkg.ResolutionOutput](ctx, writtenMD, "## Resolution")
   Expect(err).NotTo(HaveOccurred())
   Expect(resolution.Verdict).To(Equal(expectedVerdict))
   ```

   b. `DescribeTable("spawn_state", ...)`: the same closed row run with `currentJob` `""` → `spawn_state` is `"none"`, and with `currentJob` `"pr-reviewer-agent-task-1-20261028120000"` → `spawn_state` is `"job_assigned"`. Assert both fields on the written task.

   c. GitHub error: import the standard library as `stderrors "errors"`, set `fakePRState.PRStateReturns("", "", "", stderrors.New("boom"))`, observe one task, then run `SweepPass`. Assert `SweepTask` returns `(false, nil)` for that task, `fakeSender.SendCommandObjectCallCount()` is `0`, the returned closed ids are empty, and `tracker.List()` still has length 1.

   d. Send error: `fakeSender.SendCommandObjectReturns(stderrors.New("send failed"))`, observe one task, run `SweepPass`. Assert `SweepTask` returns `(false, <non-nil error>)`, the returned closed ids are empty, and `tracker.List()` still has length 1 — a failed publish leaves the task tracked.

   e. A pass touches only what it closes. Build two tasks with distinct PR URLs so one fake can answer both:
   - `task-moot` — `newSweepTask("task-moot", "")` unchanged: content URL `.../pull/2`, frontmatter `ref: abc123`
   - `task-current` — start from the helper and override two fields:
     ```go
     currentTask := newSweepTask("task-current", "")
     currentTask.Frontmatter["ref"] = "def456"
     currentTask.Content = agentlib.TaskContent(
         "https://github.com/bborbe/maintainer/pull/3\n\nsome content\n",
     )
     ```

   Set the stub so the answer depends on the URL:
   ```go
   fakePRState.PRStateStub = func(
       _ context.Context, prURL string,
   ) (string, string, string, error) {
       switch prURL {
       case "https://github.com/bborbe/maintainer/pull/2":
           return "OPEN", "", "999999", nil
       default:
           return "OPEN", "", "def456", nil
       }
   }
   ```
   Observe both tasks in one tracker, then run `SweepPass` once. Assert:
   - the returned closed ids equal `[]agentlib.TaskIdentifier{"task-moot"}`
   - `fakeSender.SendCommandObjectCallCount()` is `1`
   - `tracker.List()` has length 1 and `tracker.List()[0]` equals the original `task-current` value (`Expect(tracker.List()[0]).To(Equal(currentTask))`) — the non-closed record is byte-identical.

10. Do not modify `pkg/pr_state_check.go`, `pkg/moot_tracker.go`, `pkg/export_test.go`, `mocks/`, or any existing test file.
</requirements>

<constraints>
- The routing is imported, not reimplemented. Call `prStateCheck`; do not add a second function named `prStateCheck`, do not shadow it, do not copy its switch. `grep -rn 'func prStateCheck(' pkg/ cmd/ | wc -l` must stay `1`.
- `prStateCheck` and `closeMoot` stay unexported and unmodified. The `closed_by` / `spawn_state` stamping happens only in `pkg/moot_sweep.go` — `! grep -q 'closed_by' pkg/pr_state_check.go` must exit 0.
- `closed_by` and `spawn_state` are additive frontmatter fields. Do not infer them from, or collide them with, any existing field.
- A GitHub error is a no-op, never a closure. Only a positive verdict from GitHub closes anything.
- The task status vocabulary is closed; this prompt adds no status value.
- The sweep must never create, delete or modify a Kubernetes Job.
- The tick interval is the fixed `MootSweepInterval`; do not add a config field or a constructor parameter for it.
- Do NOT add a new module dependency. `github.com/bborbe/agent/delivery`, `github.com/bborbe/cqrs/{base,cdb,iam}`, `github.com/bborbe/time` are all already in `go.mod`; `github.com/bborbe/cqrs/mocks` is a package of an existing module.
- New code needs ≥ 80 % statement coverage; every branch above (four routing rows, two spawn states, GitHub error, send error, mixed pass) must be exercised.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

Then confirm the spec's evidence commands (run from the repo root):
- `go test -mod=mod ./pkg/... -count=1` exits 0.
- `grep -rn 'func prStateCheck(' pkg/ cmd/ | wc -l` returns `1` — exactly one routing definition exists repo-wide.
- `grep -nE '^[^/]*prStateCheck\(ctx' pkg/moot_sweep.go` returns ≥ 1 — the sweep calls the shared function from code, not a comment.
- `! grep -q 'closed_by' pkg/pr_state_check.go` exits 0 — the in-pod path stays unstamped.
- `grep -c 'closed_by' pkg/moot_sweep.go` returns ≥ 1.
- `go test -mod=mod ./pkg/... -count=1 -coverprofile=/tmp/moot-sweep.out && go tool cover -func=/tmp/moot-sweep.out | grep moot_sweep` shows every function at ≥ 80 % (or covered by the package total).
</verification>

<!-- OPEN QUESTIONS FOR THE AUDITOR
1. `spawn_state` is derived from the `current_job` frontmatter field the executor writes via its spawn notification (see `agentlib.TaskFrontmatter.CurrentJob()` and the agent CHANGELOG entry "executor publishes spawn notification ... writing `current_job` and `job_started_at`"). If a deployment's executor does not write `current_job`, every closure reads `none` and the Rung-2/Rung-3 evidence would be vacuous on that axis. The spec's AC names both values; this prompt implements the only source available.
2. `## Resolution` is read back in the tests through `agentlib.ExtractSection[pkg.ResolutionOutput]`, which is the same typed contract the routing writes, so the assertion cannot drift from the writer.
-->
