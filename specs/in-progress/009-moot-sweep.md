---
status: verifying
approved: "2026-10-28T17:38:48Z"
generating: "2026-10-28T17:40:45Z"
prompted: "2026-10-28T18:00:26Z"
verifying: "2026-10-28T18:37:30Z"
branch: dark-factory/moot-sweep
---

## Summary

- A pull-request review task is created once per pushed commit, and older ones pile up when a branch moves before they run. Each queued task waits its turn before anything checks whether its commit is still the head.
- That check already exists and is correct — but it only runs inside a reviewer pod, so reaching it costs the reviewer slot it is meant to save.
- This change adds a second, pod-less closer: a small long-running service that watches queued review tasks and closes the ones whose commit is no longer the head, without ever creating a pod.
- It reuses the existing routing unchanged, so the two closers can never disagree.
- It is a backstop, not a replacement: the watcher-side change that stops these tasks being created at all is separate work.

## Problem

A reviewer slot is the scarce resource. Review jobs run against a small pod quota, and a queued review task waits for a spawn turn before anything looks at its commit. The agent already knows how to recognise a moot task — the routing lives in `pkg/pr_state_check.go` and is called from `pkg/steps_planning.go:91`, `pkg/steps_checkout_execution.go:152` and `pkg/steps_review.go:101,190,272` — but every one of those call sites sits inside the reviewer's own steps, i.e. inside a pod that has already been created. The check is right and arrives too late; meanwhile an operator-side skill (`pr-review-close-obsolete-tasks`) proves the pod-less route works, but only when a human or agent remembers to run it. Between invocations nothing closes a moot task, so it holds a queue position and a reader's attention. Measured 2026-10-28: 6 of 25 open review tasks were already moot, three of them queued with no job.

## Goal

After this work a moot review task is closed without a reviewer pod ever being created for it.

A small long-running service watches the queued review tasks, and on a fixed five-minute tick applies the existing routing to each one: a task whose commit no longer matches the pull request's head is closed as aborted with a superseded verdict; a task whose pull request has merged or closed is closed as completed; a task whose commit is still the head is left alone; a failure to reach GitHub closes nothing. Every closure it makes is stamped with the closer's own marker and with whether a job had already been assigned, so a closure can always be attributed to the right closer and the two costs — a task closed before any pod existed, and one closed after it was queued behind a full quota — stay separable.

The service is a Deployment in the existing reviewer chart, in the same namespace the reviewer's own workloads use. This spec covers the Go-side work only: the chart template, the per-stage values and the deploy are operator steps, because this repository cannot reach them.

## Non-goals

- Do NOT change the existing routing, its verdict constants, or any of its call sites. The new service imports and reuses it; in-pod behaviour is unchanged.
- Do NOT revert or alter the mid-run cancellation that stops a review already running when the head moves. That behaviour is wanted and is untouched.
- Do NOT close tasks parked in `human_review` or `todo`. They are size-gate parks that no trigger admits; reaching them is a dispatch-policy question owned by a separate task.
- Do NOT add a watcher-side debounce or touch `bborbe/github-pr-watcher`. That is a sibling change's territory.
- Do NOT delete or modify a Kubernetes Job. The service closes task records; it does not manage Jobs.
- Do NOT introduce a new task status. The status vocabulary is closed; "superseded" is `aborted` plus a resolution verdict, as already shipped.
- Do NOT make the tick interval configurable. Five minutes is fixed by the criterion it serves; a knob nobody asked for is a knob that will be set wrong.
- Do NOT widen reviewer concurrency or change the pod quota.
- Do NOT render the chart, edit the per-stage values, bump the chart pin, or deploy from this spec — those are operator steps, listed in Verification.
- Do NOT hand-write `helm` commands; the deploy goes through the deploy repo's Makefile targets.

## Acceptance Criteria

- [ ] **The service tracks exactly the review tasks that can still be closed.** A `DescribeTable`-driven unit test asserts the in-memory tracker admits a task only when the assignee is the reviewer, the status is in-progress, and the phase is one of planning / execution / ai-review; a terminal task is dropped even if it was previously admitted; a reassigned task is dropped; two events for the same task id leave only the latest; and a payload decoded through the task-event schema path is admitted (so a tracker never fed from the topic fails this row). Evidence: `go test ./pkg/...` exits 0 with every enumerated row green. The decode row is the load-bearing one: it feeds a payload produced through the task-event schema rather than a hand-built struct, so a tracker that is wired to nothing and never fed from the topic fails it. A source grep is **not** evidence here — the string already appears in existing test comments.
- [ ] **The routing is the existing one, not a copy.** The service calls the shipped routing rather than reimplementing it — a task whose commit differs from the head on an open pull request closes as aborted with a superseded verdict naming the observed head; a merged pull request closes as completed with a merged verdict; a closed-unmerged pull request closes as completed with a closed-unmerged verdict; a failure to reach GitHub closes nothing and is retried on the next tick. Evidence: unit rows under `go test ./pkg/...` exit 0 covering all four branches, driven through the existing `PRStateClient` counterfeiter fake; plus **exactly one** routing definition exists repo-wide — `grep -rn 'func prStateCheck(' pkg/ cmd/ | wc -l` returns `1` — and the sweep calls it from a non-comment line, `grep -nE '^[^/]*prStateCheck\(ctx' pkg/moot_sweep.go` returns ≥ 1. A copy defined anywhere makes the count `2`; a call-site grep alone cannot catch that, which is why the count is the load-bearing half.
- [ ] **Every closure is attributable and bucketable.** A closed task carries `closed_by: sweep` and `spawn_state`, where `spawn_state` is `none` when the task had no current job and `job_assigned` when it did; the in-pod path writes neither field. Evidence: unit rows under `go test ./pkg/...` exit 0 asserting both fields for both input shapes, and `! grep -q 'closed_by' pkg/pr_state_check.go` exits 0 (the in-pod path stays unstamped).
- [ ] **Nothing is closed unless GitHub says it is moot.** A task whose commit equals the pull request's current head is never closed, and an errored GitHub call leaves the task untouched and still tracked. Evidence: unit rows under `go test ./pkg/...` exit 0 for (a) current-head task → no command sent, (b) GitHub error → no command sent and the tracker still holds the task.
- [ ] **A sweep pass touches only the tasks it closes.** After a pass over a task set where some are moot and some are current, the non-closed tasks' records are byte-identical. Evidence: unit row under `go test ./pkg/...` exit 0 asserting an unchanged record for every non-closed task.
- [ ] **`make precommit` exits 0 and the changelog names the change.** Evidence: `make precommit` exit 0 AND `grep -ci 'moot' CHANGELOG.md` returns ≥ 1. ⚠️ **The probe is `moot`, not `sweep`, and it is deliberately unscoped.** This file has no `## Unreleased` heading — the top section is the released version — so a heading-scoped grep returns zero on a correct tree (a defect this repository already fixed once in spec 004). And `sweep` already occurs **once** on master in a released entry, so it would pass with no changelog entry written at all; `moot` reads 0 on master and is therefore discriminating.
- [ ] **Post-Deploy (Rung-2):** the service is running on nuke dev and has closed at least one moot task with no reviewer job for it. Evidence: the Deployment reads `READY 1/1`; its pod log carries at least one completed sweep pass naming a task id; and for that task `kubectlnukedev -n development-sandbox get jobs -l agent.benjamin-borbe.de/task-id=<id>` returns nothing inside the 30-minute job-retention window, while the task's own record carries `closed_by: sweep` with `spawn_state: none`. ⚠️ **The namespace is `development-sandbox`, not `agent`** — the Config's `jobNamespace` is `development-sandbox`, and `-n agent` returns "No resources found" for *every* task id, which would make this probe pass vacuously even for a task whose reviewer pod is running. **Positive control in the same run:** the same selector returns ≥ 1 job for a task known to have spawned inside the window, so a wrong namespace or label fails loudly instead of reading as pass. A scan of the tasks it closed in that window finds none whose commit matches its pull request's head.
  - `deploy_check:` `kubectlnukedev -n agent get deploy/moot-sweep -o jsonpath='{.spec.template.spec.containers[0].image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git tag --sort=-v:refname | head -1)`
- [ ] **Post-Deploy (Rung-3):** the same observations on nuke prod, with the negative check that the service closed only the right tasks — no task whose commit equals its pull request's head reads aborted or superseded after the first pass. Evidence: the prod Deployment reads `READY 1/1`, its log carries a sweep pass naming a task id, that task carries `closed_by: sweep` + `spawn_state: none` with no job — queried in `development-sandbox`, the Config's `jobNamespace`, with the same positive control as Rung-2 — and a scan of everything it closed finds no current-head task among them.
  - `deploy_check:` `kubectlnukeprod -n agent get deploy/moot-sweep -o jsonpath='{.spec.template.spec.containers[0].image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git tag --sort=-v:refname | head -1)`

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — fmt, lint, vet, test, vuln, license all clean
- `make test` — unit and integration suites pass
- `grep -rn 'func prStateCheck(' pkg/ cmd/ | wc -l` — returns `1`; exactly one routing definition exists
- `grep -nE '^[^/]*prStateCheck\(ctx' pkg/moot_sweep.go` — ≥ 1; the sweep calls the shared function from code, not a comment
- `! grep -q 'closed_by' pkg/pr_state_check.go` — exits 0; the in-pod path stays unstamped
- `grep -ci 'moot' CHANGELOG.md` — the changelog names the change (unscoped, and `moot` not `sweep`: this file has no `## Unreleased` heading and `sweep` already appears on master)

### Operator-executable (runs on the host after PR merge, spec verification ladder)

These steps are the operator's because this repository cannot reach the chart or the deploy repo.

- Add the Deployment template to the `github-pr-reviewer` chart in `bborbe/helm` (`charts/github-pr-reviewer/templates/`), named **`moot-sweep`** (the name both `deploy_check` lines read), with its image entry in that chart's `values.yaml`. The five-minute tick is **hardcoded in the template** — the chart has no interval concept today and this spec forbids introducing one (Non-goals). Bump the chart version.
- Add the per-stage entry to `bborbe/nuke`'s `github-pr-reviewer/values-{dev,prod}.yaml` (the chart itself carries a single `values.yaml` and no per-stage files), and bump the OCI pin in `bborbe/nuke`'s `github-pr-reviewer/helmfile.yaml` to the new chart version.
- **Bump the mirrored image tag in `bborbe/nuke`'s `github-pr-reviewer/Makefile`** — `AGENT_TAG_dev` and `AGENT_TAG_prod`, and add the sweep's own image to `MIRROR_IMAGES` if it ships under a separate name. `MIRROR_IMAGES` is built from those variables, so `make mirror` pushes **only** the tags named there; leaving them behind makes the pod `ImagePullBackOff` on a tag the registry never received — the exact failure the Makefile's own header documents. This step is easy to miss and makes the two below it fail.
- `helm template` on the chart — the rendered output contains a Deployment whose namespace resolves to `agent`.
- From `~/Documents/workspaces/nuke/github-pr-reviewer`: `BRANCH=dev make mirror && BRANCH=dev make apply`, then `BRANCH=master make mirror && BRANCH=master make apply`. Prod is `BRANCH=master`, never `BRANCH=prod`, and `make mirror` is not optional — it is what puts the image where the cluster pulls it from.
- `kubectlnukedev -n agent get deploy | grep moot-sweep` and the prod equivalent — both read `READY 1/1`.
- `kubectlnukedev -n agent logs <sweep-pod> | grep 'sweep pass'` — at least one completed pass naming a task id.

## Desired Behavior

1. A moot review task is closed within one tick (five minutes) of the service observing it, and no reviewer job is created for it.
2. A closed task records which closer closed it and whether a job had already been assigned, so the two costs stay separable.
3. A task whose commit still matches its pull request's head is never closed, and its record is untouched by a sweep pass.
4. A failure to reach GitHub closes nothing, and the task stays tracked for the next tick.
5. A merged or closed-unmerged pull request routes to completed, never to superseded.
6. Only review tasks that are assigned to the reviewer, in progress, and in a pre-terminal phase are tracked at all.

## Constraints

- The routing is imported, not reimplemented. Any divergence between the two closers is a defect by definition.
- **The sweep's decision logic lives in `package pkg`**, alongside the routing it reuses. `prStateCheck` and `closeMoot` are unexported and must not be exported, duplicated or shadowed; a `cmd/moot-sweep` that cannot reach them must move the logic into `pkg`, not re-derive it. `cmd/moot-sweep` is a thin entrypoint that wires configuration and starts the tick loop — it contains no routing decision. The decision logic file is **`pkg/moot_sweep.go`** (frozen: the evidence commands name it).
- `closed_by` and `spawn_state` are additive frontmatter fields. They must not collide with, or be inferred from, any existing field, and the in-pod path must not start writing them.
- The task status vocabulary is closed; the change adds no status value.
- The service must never create, delete or modify a Kubernetes Job.
- A GitHub error is a no-op, never a closure. Only a positive verdict from GitHub closes anything.
- The tick interval is a fixed five minutes.
- The service writes task records through the existing command path, the same one the reviewer's own pod uses — no new write mechanism.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| GitHub rate-limits or times out for a task | That task is skipped this tick, nothing is closed, the task stays tracked | Next tick retries; sustained failures are visible as repeated skip log lines |
| The event consumer restarts or lags | The in-memory view is rebuilt from the topic; a task missed during the gap is picked up when its next event arrives | Consumer group offset resumes; a task with no further events is closed by the in-pod path when it eventually spawns |
| Crash mid-write of a closure | The task is either closed or not; the write is not partially applied | Next tick re-evaluates; the routing is idempotent on an already-closed task |
| The in-pod closer and the sweep race on the same task | Both compute the same verdict; the second write is a no-op because the task is already terminal | None needed; the shared routing's idempotency guard covers it |
| The sweep closes a task that is still current | Must not happen — the current-head branch is asserted in two ACs and re-checked on prod | Reopen the task and investigate the head comparison before the next deploy |
| The topic is empty or the consumer never connects | No closures; the service is running but idle | Visible as a running Deployment with no sweep-pass log lines — the post-deploy ACs catch it |
| The tracker grows without bound | Memory is bounded by the number of live review tasks; a terminal task is dropped on transition | If growth is observed, the tracker is keyed by task id and a terminal event evicts it; a task that never emits a terminal event is the defect to investigate |

## Security / Abuse Cases

The service mutates shared task records on the strength of a message it did not author, so the write scope is the control, not the read scope.

- **A payload naming a task the tracker never admitted produces no write.** Admission is the only gate; an event for an untracked or filtered-out task is dropped, never acted on.
- **Only the record the event names is ever written.** No pass writes to a task id it did not receive, and no bulk or wildcard update exists.
- **A replayed or duplicated event cannot produce a second closure.** The routing is idempotent on an already-terminal task, so a duplicate delivery is a no-op rather than a second write.
- **A GitHub failure is never treated as permission to close.** Only a positive verdict closes anything; an error is a no-op, so an unreachable or spoofed-failure state cannot clear a live task.
- **No credentials are introduced beyond what the reviewer already holds.** The service reuses the existing GitHub client and the existing task-write command path; it adds no new secret, and its Deployment carries no broader permission than the reviewer's.

## Suggested Decomposition

Prompts should be generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Task-event consumer and in-memory tracker | 1, 6 | 1 | — |
| 2 | Routing reuse, closure stamping, no-op on error | 2, 3, 4, 5 | 2, 3, 4, 5 | prompt 1 |
| 3 | Service entrypoint and fixed five-minute tick loop | 1 | 1, 6 | prompts 1, 2 |

Rationale: prompt 1 establishes the tracked set; prompt 2 makes the decision and the write; prompt 3 only wires a ticker around them, so it needs both.

## Do-Nothing Option

Every moot review task keeps waiting out the queue before anything looks at it, and each one spends a reviewer slot on a commit nobody will merge. The cost is bounded but recurring: measured on 2026-10-28, three tasks were queued against a reviewer whose pod quota sat at 9 of 9 within the same hour. The operator-side skill already exists and already works, so doing nothing is not "no closer" — it is "a closer that runs only when someone remembers", which is the state that produced the measurement. The alternative — extending the watcher instead — is the sibling change and does not cover tasks that already exist.
