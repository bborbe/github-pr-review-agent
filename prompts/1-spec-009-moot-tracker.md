---
status: draft
spec: [009-moot-sweep]
created: "2026-10-28T18:20:00Z"
branch: dark-factory/moot-sweep
---

# Track the review tasks that can still be closed

<summary>
- A new in-memory registry holds only the pull-request review tasks that are still open enough to be closed.
- A task is admitted only when it is assigned to the reviewer, its status is in progress, and its phase is planning, execution, or ai-review.
- A task that becomes terminal is dropped from the registry even if it was admitted earlier, so the registry does not accumulate dead entries.
- A task that is reassigned to someone else is dropped the same way.
- Two updates for the same task id leave only the newest payload in the registry.
- The registry is fed by the exact JSON payload the Kafka task-event topic carries, so a registry that is never wired to the topic cannot pass its own tests.
- The registry is safe for concurrent use by a consumer goroutine and a sweep goroutine.
- Nothing else changes: no task status is added, no routing is touched, no new dependency is introduced.
</summary>

<objective>
Create `pkg/moot_tracker.go`: a small, concurrency-safe, in-memory set of review tasks that are eligible to be closed, keyed by task id, fed from the decoded task-event payload. It is the first of three prompts that build the pod-less moot-task closer; this prompt only establishes the tracked set.
</objective>

<context>
Read `CLAUDE.md` for project conventions (Ginkgo v2 / Gomega table style, external test packages, `make precommit`).

Read these files fully:
- `pkg/pr_state_check.go` — the existing routing this work reuses. You do not modify it. Note `PRStateClient`, `ResolutionOutput`, and the verdict constants; they are the vocabulary a closed task ends up with.
- `pkg/pr_state_watch.go` — shows the house style for a small unexported struct with an exported constructor and `libtime`/`glog` usage.
- `pkg/pr_state_check_test.go` — the test style this prompt's tests must match: `package pkg_test`, Ginkgo `Describe`/`DescribeTable`, `github.com/bborbe/github-pr-review-agent/mocks` fakes.
- `pkg/pkg_suite_test.go` — the single suite entry point (`TestSuite`); do not add a second one.
- `pkg/export_test.go` — the existing shims. You will not need a shim here because every symbol this prompt's tests touch is exported.
- `CHANGELOG.md` — read only; this prompt writes no changelog entry (prompt 3 does).

Verified library contracts you may rely on (do not re-derive them from training data):
- `agentlib "github.com/bborbe/agent"`:
  - `type Task struct { base.Object[base.Identifier]; TaskIdentifier TaskIdentifier; Vault string; Frontmatter TaskFrontmatter; Content TaskContent }`
  - `type TaskIdentifier string`
  - `type TaskFrontmatter map[string]interface{}` with accessors `Assignee() TaskAssignee`, `Status() domain.TaskStatus`, `Phase() *domain.TaskPhase`, `CurrentJob() string`
  - `type TaskAssignee string`
  - `type TaskContent string`
- `domain "github.com/bborbe/vault-cli/pkg/domain"`: `TaskStatusInProgress = "in_progress"`, `TaskPhasePlanning = "planning"`, `TaskPhaseExecution = "execution"`, `TaskPhaseAIReview = "ai_review"`. `TaskFrontmatter.Status()` and `.Phase()` normalise the legacy aliases (`todo` → `next`, `current` → `in_progress`, `in_progress` phase → `execution`), so compare against the canonical constants only.
- `base "github.com/bborbe/cqrs/base"`: `ParseEvent(ctx context.Context, value interface{}) (Event, error)` produces the wire shape published on the task-event topic (`Event` is `map[FieldName]interface{}`); `json.Unmarshal` of that map back into `agentlib.Task` is the decode path the consumer uses.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-patterns.md` — public interface + private struct + `New*` constructor
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo v2 `DescribeTable`, external test package, coverage ≥ 80 % for new code
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md` — GoDoc comments start with the name and describe behaviour
</context>

<requirements>
1. Create `pkg/moot_tracker.go` in `package pkg` with the standard license header (copy the four-line header from `pkg/pr_state_check.go`).

2. Add the reviewer identity constant. The assignee string is verified: it is the `Config.spec.assignee` of the reviewer, `pr-reviewer-agent` (see `cmd/run-task/dummy-task.md` and `helm/values.yaml` in `github.com/bborbe/maintainer`):
   ```go
   // mootSweepAssignee is the task assignee the reviewer claims. Only tasks
   // assigned to this agent are eligible for the pod-less moot closer.
   const mootSweepAssignee = agentlib.TaskAssignee("pr-reviewer-agent")
   ```

3. Define the tracker interface, constructor, and private struct:
   ```go
   // MootTracker holds the review tasks that are still eligible to be closed.
   // It is fed from the task-event topic and read once per sweep pass. It is
   // safe for concurrent use.
   type MootTracker interface {
       // Observe admits task when it is still closeable, evicts the entry when
       // it is not (terminal, reassigned, wrong phase), and otherwise replaces
       // any previously stored payload for the same task id.
       Observe(ctx context.Context, task agentlib.Task)
       // Remove drops the entry for id. A no-op when the id is unknown.
       Remove(id agentlib.TaskIdentifier)
       // List returns a snapshot of the tracked tasks, ordered by task id so a
       // sweep pass is deterministic.
       List() []agentlib.Task
   }

   // NewMootTracker returns an empty MootTracker.
   func NewMootTracker() MootTracker {
       return &mootTracker{tasks: make(map[agentlib.TaskIdentifier]agentlib.Task)}
   }

   type mootTracker struct {
       mu    sync.Mutex
       tasks map[agentlib.TaskIdentifier]agentlib.Task
   }
   ```
   Do NOT add a `//counterfeiter:generate` annotation: no test in this batch needs a fake of this interface (the sweep tests in prompt 2 use the real tracker with faked dependencies), and `make precommit` regenerates `mocks/` from scratch.

4. Add the admission predicate as an unexported function so the three conditions live in one place:
   ```go
   // isTrackableReviewTask reports whether task is a review task that is still
   // in a phase a closer may act on: assigned to the reviewer, status
   // in_progress, phase planning / execution / ai_review.
   func isTrackableReviewTask(task agentlib.Task) bool
   ```
   Implementation: `task.Frontmatter.Assignee() == mootSweepAssignee` AND `task.Frontmatter.Status() == domain.TaskStatusInProgress` AND `task.Frontmatter.Phase() != nil` AND the dereferenced phase is one of `domain.TaskPhasePlanning`, `domain.TaskPhaseExecution`, `domain.TaskPhaseAIReview`. A missing or unparseable phase is not trackable.

5. Implement the three methods on `*mootTracker`, each holding `t.mu` for its whole body:
   - `Observe`: when `isTrackableReviewTask(task)` is false, `delete(t.tasks, task.TaskIdentifier)` and log at `glog.V(2)` naming the task id, the assignee, the status, and the phase (use the raw frontmatter values; a nil phase logs as `""`). Otherwise store `t.tasks[task.TaskIdentifier] = task`. A terminal or reassigned task that was previously admitted must therefore be evicted, not merely ignored.
   - `Remove`: `delete(t.tasks, id)`.
   - `List`: build a `[]agentlib.Task` of capacity `len(t.tasks)`, append every stored value, `sort.Slice` it by `TaskIdentifier`, return it. The returned slice is a snapshot; the map is not exposed.

6. Write `pkg/moot_tracker_test.go` in `package pkg_test` (Ginkgo v2 / Gomega, no new suite file). Structure it as:

   a. Helpers (the test file imports `encoding/json`, `base "github.com/bborbe/cqrs/base"`, `agentlib "github.com/bborbe/agent"`, `pkg "github.com/bborbe/github-pr-review-agent/pkg"` and the Ginkgo/Gomega dot-imports):
   ```go
   func newReviewTask(id, assignee, status, phase string) agentlib.Task {
       return agentlib.Task{
           TaskIdentifier: agentlib.TaskIdentifier(id),
           Frontmatter: agentlib.TaskFrontmatter{
               "assignee": assignee,
               "status":   status,
               "phase":    phase,
           },
           Content: agentlib.TaskContent(
               "https://github.com/bborbe/maintainer/pull/2\n\nsome content\n",
           ),
       }
   }
   ```

   b. A `DescribeTable("admission", ...)` whose row function is
   `func(task agentlib.Task, expected bool)` and which does
   `tracker := pkg.NewMootTracker(); tracker.Observe(ctx, task); Expect(tracker.List()).To(HaveLen(n))`
   where `n` is `1` for `expected == true` and `0` otherwise. Entries (assignee `pr-reviewer-agent` unless stated):
   - `status: in_progress`, `phase: planning` → admitted
   - `status: in_progress`, `phase: execution` → admitted
   - `status: in_progress`, `phase: in_progress` (legacy alias, normalises to `execution`) → admitted
   - `status: in_progress`, `phase: ai_review` → admitted
   - `status: in_progress`, `phase: todo` → rejected
   - `status: in_progress`, `phase: human_review` → rejected
   - `status: in_progress`, `phase: done` → rejected
   - `status: in_progress`, `phase: ""` → rejected
   - `status: next`, `phase: execution` → rejected
   - `status: completed`, `phase: execution` → rejected
   - `status: aborted`, `phase: execution` → rejected
   - `status: ""`, `phase: execution` → rejected
   - assignee `github-releaser-agent`, `status: in_progress`, `phase: execution` → rejected
   - assignee `""`, `status: in_progress`, `phase: execution` → rejected

   c. The load-bearing decode row, as its own `It` (NOT a hand-built struct — it must traverse the wire format the consumer sees):
   ```go
   It("admits a task decoded from the task-event schema payload", func() {
       original := newReviewTask("task-1", "pr-reviewer-agent", "in_progress", "execution")
       event, err := base.ParseEvent(ctx, original)
       Expect(err).NotTo(HaveOccurred())
       payload, err := json.Marshal(event)
       Expect(err).NotTo(HaveOccurred())

       var decoded agentlib.Task
       Expect(json.Unmarshal(payload, &decoded)).To(Succeed())
       Expect(decoded.TaskIdentifier).To(Equal(agentlib.TaskIdentifier("task-1")))

       tracker := pkg.NewMootTracker()
       tracker.Observe(ctx, decoded)
       Expect(tracker.List()).To(HaveLen(1))
       Expect(tracker.List()[0].Frontmatter.Assignee()).To(Equal(agentlib.TaskAssignee("pr-reviewer-agent")))
   })
   ```
   Add a comment above it stating why it is separate from the table: the table rows are hand-built structs, so only this row fails when the tracker is never fed from the topic's payload shape.

   d. `It` blocks for the lifecycle rules:
   - terminal after admitted: observe an admitted task, then observe the same id with `status: completed` → `List()` is empty.
   - reassigned after admitted: observe an admitted task, then observe the same id with assignee `github-releaser-agent` → `List()` is empty.
   - latest wins: observe id `task-1` with `phase: planning`, then the same id with `phase: ai_review` → `List()` has length 1 and its `Frontmatter.Phase()` is `ai_review`.
   - two distinct ids → `List()` has length 2 and the ids are in ascending order (insert them out of order).
   - `Remove` of a tracked id → empty; `Remove` of an unknown id → no panic, remaining entries unchanged.

7. Do not modify `pkg/pr_state_check.go`, `pkg/pr_state_watch.go`, `pkg/export_test.go`, or any existing test file. Do not add a changelog entry.
</requirements>

<constraints>
- The tracker is fed only from the task-event payload; do not add a file reader, a Kubernetes informer, or any other source of tasks.
- Do NOT add a task status, phase, or verdict constant. The status vocabulary is closed; "superseded" is `aborted` plus a resolution verdict and is written by the existing routing, not here.
- Do NOT change the existing routing, its verdict constants, or any of its call sites. `prStateCheck` and `closeMoot` stay unexported, unmodified and unique.
- Do NOT export `mootTracker` or `isTrackableReviewTask`.
- Do NOT add a counterfeiter annotation for `MootTracker`.
- Do NOT add any new module dependency. `sync`, `sort`, `context`, `encoding/json`, `github.com/bborbe/agent`, `github.com/bborbe/cqrs/base`, `github.com/bborbe/vault-cli/pkg/domain`, `github.com/golang/glog` are all already in `go.mod`.
- New code needs ≥ 80 % statement coverage; every admission row and every lifecycle rule above must be exercised.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

Then confirm the specific behaviour:
- `go test -mod=mod ./pkg/... -count=1` exits 0.
- `go test -mod=mod ./pkg/... -count=1 -coverprofile=/tmp/moot-tracker.out && go tool cover -func=/tmp/moot-tracker.out | grep moot_tracker` shows every function in `pkg/moot_tracker.go` at ≥ 80 % (or covered by the package total).
- `grep -c 'func isTrackableReviewTask' pkg/moot_tracker.go` returns `1`.
- `grep -c 'counterfeiter:generate' pkg/moot_tracker.go` returns `0`.
</verification>
