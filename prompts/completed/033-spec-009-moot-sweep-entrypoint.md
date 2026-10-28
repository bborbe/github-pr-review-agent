---
status: completed
spec: [009-moot-sweep]
summary: Added the cmd/moot-sweep entrypoint wiring the tracker and sweep to Kafka, GitHub App auth and probe/metrics HTTP, with a compile smoke test, its Makefile and Dockerfile build path, plus changelog and README updates
execution_id: github-pr-review-agent-moot-sweep-exec-033-spec-009-moot-sweep-entrypoint
dark-factory-version: v0.198.7
created: "2026-10-28T18:24:00Z"
queued: "2026-10-28T18:16:26Z"
started: "2026-10-28T18:28:36Z"
completed: "2026-10-28T18:37:29Z"
branch: dark-factory/moot-sweep
---

# The moot-sweep service entrypoint and its fixed five-minute tick

<summary>
- A new long-running binary consumes the task-event topic and feeds the in-memory tracker from it.
- Every five minutes it runs one sweep pass over everything the tracker holds and logs what it closed, naming the task ids.
- The interval is a compile-time constant; there is no flag, no environment variable and no config field for it.
- The service writes through the same Kafka command producer the reviewer pod already uses, with the same topic prefix.
- It mints the same GitHub App installation token the reviewer mints, and adds no other credential.
- It serves health, readiness and metrics endpoints on a fixed listen address, so the Deployment's probes can pass.
- The binary compiles and is covered by the repo's compile smoke test, and has a Makefile like the other `cmd/` tools.
- The changelog names the change so it is discoverable from the release notes.
- The existing entrypoints and the whole existing test suite are untouched.
</summary>

<objective>
Create `cmd/moot-sweep/main.go`: a thin entrypoint that wires the tracker (prompt 1) and the sweep (prompt 2) to Kafka, GitHub and an HTTP probe server, and runs a fixed five-minute tick loop. Also add the binary's compile smoke test, its Makefile, and the changelog entry. It is prompt 3 of 3 and depends on prompts 1 and 2 having shipped.
</objective>

<context>
Read `CLAUDE.md` for project conventions and `docs/dod.md` for the changelog placement rule.

Read these files fully:
- `main.go` — the pattern to copy: the `application` struct with `argument` tags, `service.Main(ctx, app, &app.SentryDSN, &app.SentryProxy)`, `libkafka.NewSyncProducerWithName`, `libkafka.Brokers`, `base.TopicPrefix`, and `resolveAuth` (mints the GitHub App installation token with `githubapp.MintIAT`). Do NOT modify this file.
- `cmd/run-task/main.go` — the second entrypoint; confirm the `application`/`Run` shape and that you must not change it.
- `cmd/run-task/main_test.go` — the compile smoke test pattern (`gexec.Build("github.com/bborbe/github-pr-review-agent/cmd/run-task", "-mod=mod")`) and the single suite function.
- `cmd/mint-iat/Makefile` — copy it verbatim to the new command directory.
- `pkg/moot_tracker.go` — `prpkg.NewMootTracker() prpkg.MootTracker` with `Observe(ctx, task)`, `Remove(id)`, `List()`.
- `pkg/moot_sweep.go` — `prpkg.NewMootSweep(tracker, prState, sender, currentDateTime) prpkg.MootSweep` with `SweepTask` / `SweepPass`, and `prpkg.MootSweepInterval`.
- `pkg/github/client.go` — `github.NewGHClient(token)` returns a `github.Client` that satisfies `pkg.PRStateClient` (`PRState`, `PRDiff`) and passes the token to the `gh` CLI.
- `CHANGELOG.md` — the preamble ends at `...adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).` and the first released section is `## v0.14.6`. There is no `## Unreleased` heading.
- `.dark-factory.yaml` — `workflow: direct`, no `hideGit`.

Verified library contracts you may rely on (do not re-derive them from training data):
- `agentlib "github.com/bborbe/agent"`: `type Task` (the event payload); `TaskV1SchemaID` — a `cdb.SchemaID` with `.EventTopic(prefix base.TopicPrefix) libkafka.Topic` producing `<prefix>-agent-task-v1-event` (or `agent-task-v1-event` with an empty prefix).
- `libkafka "github.com/bborbe/kafka"`: `type Brokers`, `type Topic`, `type Consumer interface { Consume(ctx context.Context) error }`, `type MessageHandlerFunc func(ctx context.Context, msg *sarama.ConsumerMessage) error`, `CreateSaramaClient(ctx, brokers) (SaramaClient, error)`, `NewSimpleConsumer(saramaClient SaramaClient, topic Topic, initalOffset Offset, messageHandler MessageHandler, logSamplerFactory log.SamplerFactory) Consumer`, `OffsetOldest`, `NewSyncProducerWithName(ctx, brokers, name) (SyncProducer, error)`.
- `cdb "github.com/bborbe/cqrs/cdb"`: `NewCommandObjectSender(syncProducer libkafka.SyncProducer, prefix base.TopicPrefix, logSamplerFactory log.SamplerFactory) CommandObjectSender`.
- `log "github.com/bborbe/log"`: `DefaultSamplerFactory`.
- `libhttp "github.com/bborbe/http"`: `NewServer(addr string, router http.Handler, optionFns ...func(*ServerOptions)) run.Func`.
- `service "github.com/bborbe/service"`: `Main(ctx, app, sentryDSN *string, sentryProxy *string) int`, `Run(ctx context.Context, funcs ...run.Func) error` (cancels every other function when one finishes).
- `githubapp "github.com/bborbe/maintainer/githubapp"`: `MintIAT(ctx, Config{AppID int64; InstallationID int64; PEMPath string; PEM []byte}) (string, error)`.
- `promhttp "github.com/prometheus/client_golang/prometheus/promhttp"`: `Handler() http.Handler`.

Read the coding plugin guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-k8s-binary-conventions.md` — `service.Main`, `application` struct with `argument` tags, `display:"length"` on secrets, `/healthz` + `/readiness` + `/metrics`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-cqrs.md` — the command-sender idiom this service writes through
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-concurrency-patterns.md` — `service.Run` / `run.CancelOnFirstFinish*` instead of raw `go func()`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `errors.Wrapf`, never `fmt.Errorf`
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — entry format, prefix rules, `## Unreleased` placement
</context>

<requirements>
1. Create `cmd/moot-sweep/main.go` (`package main`) with the standard license header. Copy `cmd/mint-iat/Makefile` to `cmd/moot-sweep/Makefile` verbatim (it includes `../../../../Makefile.variables` and stubs `buca`/`apply` so the parent recursion succeeds).

1b. Ship the binary as an image. The root `Dockerfile` builds exactly one binary (`go build … -o /main`, `ENTRYPOINT ["/main", "-v=2"]`) and `Makefile.docker` tags a single `$(SERVICE)` from it, so nothing in the repo can currently produce a `moot-sweep` image — and without one the Deployment has nothing to run and the spec's two post-deploy criteria are unreachable. Extend the build so `cmd/moot-sweep` gets its own image: either parameterise the `go build` target and `ENTRYPOINT` on a build argument (`SERVICE`/`BINARY`, defaulting to today's `/main` so the existing image is byte-identical), or add `cmd/moot-sweep/Dockerfile` alongside. Edit the build files only — do **not** run `docker build`, `make buca` or `make build` (those are operator-side), and `make precommit` must stay green. Keep the existing image's output unchanged: this requirement adds a second build path, it does not alter the first.

2. Entry point, mirroring `main.go`:
   ```go
   func main() {
       app := &application{}
       os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
   }
   ```

3. The `application` struct, with exactly these fields and tags (no other config knob — in particular no interval, no concurrency, no allowlist field):
   ```go
   type application struct {
       SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"SentryDSN"    display:"length"`
       SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy"`

       // KafkaBrokers is the broker list the sweep consumes task events from and
       // publishes closures to.
       KafkaBrokers libkafka.Brokers `required:"true" arg:"kafka-brokers" env:"KAFKA_BROKERS" usage:"Comma separated list of Kafka brokers"`

       // TopicPrefix must match the reviewer's, so the sweep reads the same event
       // topic and writes to the same command topic.
       TopicPrefix base.TopicPrefix `required:"false" arg:"topic-prefix" env:"TOPIC_PREFIX" usage:"Kafka topic prefix for CQRS topic construction"`

       // Listen drives the probe and metrics server.
       Listen string `required:"false" arg:"listen" env:"LISTEN" usage:"HTTP listen address (healthz/readiness/metrics)" default:":9090"`

       // GitHub App authentication, identical to the reviewer's.
       AppID          int64  `required:"false" arg:"app-id"          env:"APP_ID"           usage:"GitHub App ID (numeric); required for App auth"`
       InstallationID int64  `required:"false" arg:"installation-id" env:"INSTALLATION_ID"  usage:"GitHub App Installation ID (numeric)"`
       PEMKeyFile     string `required:"false" arg:"pem-key-file"    env:"PEM_KEY_FILE"     usage:"Path to the GitHub App private key (PEM file mounted from k8s Secret)"`
       PEMKey         string `required:"false" arg:"pem-key"         env:"PEM_KEY"          usage:"GitHub App private key (PEM) as env var content; mutually exclusive with PEM_KEY_FILE" display:"length"`
   }
   ```

4. Import aliases used throughout the file: `agentlib "github.com/bborbe/agent"`, `base "github.com/bborbe/cqrs/base"`, `cdb "github.com/bborbe/cqrs/cdb"`, `libhttp "github.com/bborbe/http"`, `libkafka "github.com/bborbe/kafka"`, `log "github.com/bborbe/log"`, `prpkg "github.com/bborbe/github-pr-review-agent/pkg"`, `github "github.com/bborbe/github-pr-review-agent/pkg/github"`, `run "github.com/bborbe/run"`, `libsentry "github.com/bborbe/sentry"`, `service "github.com/bborbe/service"`, `libtime "github.com/bborbe/time"`, `githubapp "github.com/bborbe/maintainer/githubapp"`, `sarama "github.com/IBM/sarama"`, `promhttp "github.com/prometheus/client_golang/prometheus/promhttp"`, `"github.com/golang/glog"`, `"github.com/bborbe/errors"`.

5. `func (a *application) Run(ctx context.Context, _ libsentry.Client) error` wires and starts everything. Keep it short — the lint cap is 80 lines / 50 statements, so split the three construction steps into methods:
   - `resolveAuth(ctx) (string, error)` — copy `main.go`'s implementation verbatim (checks `AppID`/`InstallationID`/PEM, calls `githubapp.MintIAT`, returns the installation token; returns `errors.Errorf(ctx, "moot-sweep auth: GitHub App credentials not configured — set APP_ID, INSTALLATION_ID, and PEM_KEY_FILE (or PEM_KEY)")` when incomplete).
   - `createSweep(ctx, tracker, token) (prpkg.MootSweep, func(), error)`:
     ```go
     syncProducer, err := libkafka.NewSyncProducerWithName(ctx, a.KafkaBrokers, "moot-sweep")
     if err != nil {
         return nil, nil, errors.Wrap(ctx, err, "create sync producer")
     }
     cleanup := func() {
         if err := syncProducer.Close(); err != nil {
             glog.Warningf("close sync producer failed: %v", err)
         }
     }
     sender := cdb.NewCommandObjectSender(syncProducer, a.TopicPrefix, log.DefaultSamplerFactory)
     sweep := prpkg.NewMootSweep(
         tracker, github.NewGHClient(token), sender, libtime.NewCurrentDateTime(),
     )
     return sweep, cleanup, nil
     ```
   - `createConsumer(ctx, tracker) (libkafka.Consumer, error)`:
     ```go
     saramaClient, err := libkafka.CreateSaramaClient(ctx, a.KafkaBrokers)
     if err != nil {
         return nil, errors.Wrap(ctx, err, "create sarama client")
     }
     handler := libkafka.MessageHandlerFunc(
         func(ctx context.Context, msg *sarama.ConsumerMessage) error {
             var task agentlib.Task
             if err := json.Unmarshal(msg.Value, &task); err != nil {
                 return errors.Wrapf(ctx, err, "unmarshal task event")
             }
             tracker.Observe(ctx, task)
             return nil
         },
     )
     return libkafka.NewSimpleConsumer(
         saramaClient,
         agentlib.TaskV1SchemaID.EventTopic(a.TopicPrefix),
         libkafka.OffsetOldest,
         handler,
         log.DefaultSamplerFactory,
     ), nil
     ```
     The decode is `json.Unmarshal(msg.Value, &agentlib.Task{})` because the event topic's value is the JSON of the `base.Event` map produced by `base.ParseEvent`, whose keys line up with the `agentlib.Task` JSON tags — this is the same decode path prompt 1's tracker test locks. `OffsetOldest` is deliberate: on every restart the in-memory view is rebuilt from the topic, which is the recovery behaviour the spec's failure table requires. Do not add a consumer-group offset store.
   - `runTickLoop(ctx, sweep) error`:
     ```go
     ticker := time.NewTicker(prpkg.MootSweepInterval)
     defer ticker.Stop()
     for {
         select {
         case <-ctx.Done():
             return nil
         case <-ticker.C:
             closed, err := sweep.SweepPass(ctx)
             if err != nil {
                 return errors.Wrap(ctx, err, "sweep pass")
             }
             glog.V(2).Infof(
                 "sweep pass complete closed=%d task_ids=%v", len(closed), closed,
             )
         }
     }
     ```
     The log line must contain the literal `sweep pass` and must name the closed task ids — the post-deploy acceptance check greps for exactly that.
   - `createHTTPServer() run.Func` — a stdlib `http.NewServeMux` with `GET /healthz` and `GET /readiness` returning `200`, `/metrics` served by `promhttp.Handler()`, wrapped in `libhttp.NewServer(a.Listen, mux)`. The Deployment's probes need these; a long-running pod without them can never read `READY 1/1`.

   `Run` then:
   ```go
   token, err := a.resolveAuth(ctx)
   if err != nil {
       return err
   }
   tracker := prpkg.NewMootTracker()
   consumer, err := a.createConsumer(ctx, tracker)
   if err != nil {
       return err
   }
   sweep, cleanup, err := a.createSweep(ctx, tracker, token)
   if err != nil {
       return err
   }
   defer cleanup()
   return service.Run(
       ctx,
       consumer.Consume,
       func(ctx context.Context) error { return a.runTickLoop(ctx, sweep) },
       a.createHTTPServer(),
   )
   ```
   Do not spawn raw goroutines; `service.Run` cancels the others when any one returns.

6. Create `cmd/moot-sweep/main_test.go` (`package main_test`) mirroring `cmd/run-task/main_test.go`: one `It("Compiles")` that `gexec.Build("github.com/bborbe/github-pr-review-agent/cmd/moot-sweep", "-mod=mod")` and the standard `TestSuite(t *testing.T)` with `time.Local = time.UTC`, `format.TruncatedDiff = false`, `RegisterFailHandler(Fail)`, `suiteConfig.Timeout = 60 * time.Second`, `RunSpecs(t, "Moot-Sweep Suite", ...)`. This is what makes `go test ./...` prove the new entrypoint compiles.

7. Add the changelog entry. Create `## Unreleased` immediately below the preamble (after `...adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).`) and above `## v0.14.6` — never between the `# Changelog` title and the preamble. The bullet must contain the word `moot` (the spec's acceptance probe is `grep -ci 'moot' CHANGELOG.md` ≥ 1) and name the observable change:
   ```markdown
   ## Unreleased

   - feat: close moot PR-review tasks without spawning a reviewer pod. A new `moot-sweep` Deployment consumes the task-event topic, applies the shipped `prStateCheck` routing on a fixed five-minute tick, and publishes closures through the same CQRS command path the reviewer uses — aborted + superseded when the commit is no longer the pull request's head, completed for a merged or closed-unmerged pull request. Each closure carries `closed_by: sweep` and `spawn_state` (`none` / `job_assigned`), so a task closed before any pod existed stays distinguishable from one closed after a job was assigned; the in-pod path writes neither field, a GitHub error closes nothing, and a task whose commit is still the head is never touched.
   ```

8. `go.mod` note: this prompt's imports promote four currently-indirect modules to direct — `github.com/bborbe/http`, `github.com/bborbe/log`, `github.com/bborbe/run` and `github.com/IBM/sarama`. Do not hand-edit `go.mod`; `make precommit`'s `ensure` target runs `go mod tidy` and rewrites the `// indirect` markers. All four are already in `go.sum`.

9. Do not modify `main.go`, `cmd/run-task/`, `cmd/cli/`, `cmd/mint-iat/`, `pkg/factory/`, or any existing test.
</requirements>

<constraints>
- The tick interval is fixed at `prpkg.MootSweepInterval` (five minutes). Do NOT add a flag, environment variable, config field, or constructor parameter for it.
- `cmd/moot-sweep` is a thin entrypoint: it contains no routing decision, no moot check, and no frontmatter stamping. All of that lives in `package pkg` (prompts 1 and 2). Do not re-derive `prStateCheck` logic here.
- Do NOT add a task status, do not touch the existing routing or its call sites, do not export or duplicate `prStateCheck`/`closeMoot`.
- The service must never create, delete or modify a Kubernetes Job.
- No credentials beyond the reviewer's GitHub App installation token. No new Secret, no PAT, no allowlist.
- Do NOT render the chart, edit per-stage values, bump the chart pin, or run `helm` — those are operator steps and out of scope for this prompt. **Building the image is also operator-side**, but *editing* the Dockerfile/Makefile.docker so the image can exist is requirement 1b and is in scope here.
- The sweep writes through the existing command path (`cdb.CommandObjectSender` + `agentlib.TaskV1SchemaID`); no new write mechanism.
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must pass (fmt, generate, test, lint, vet, vuln, license).

Then confirm the spec's container-executable evidence (run from the repo root):
- `grep -rn 'func prStateCheck(' pkg/ cmd/ | wc -l` returns `1` — exactly one routing definition exists repo-wide.
- `grep -nE '^[^/]*prStateCheck\(ctx' pkg/moot_sweep.go` returns ≥ 1 — the sweep calls the shared function from code.
- `! grep -q 'closed_by' pkg/pr_state_check.go` exits 0 — the in-pod path stays unstamped.
- `grep -ci 'moot' CHANGELOG.md` returns ≥ 1 — the changelog names the change (unscoped: this file has no `## Unreleased` on master and `sweep` already appears in a released entry, so only `moot` is discriminating).
- `grep -n '^## Unreleased' CHANGELOG.md` shows the new section between the preamble and `## v0.14.6` (the `^` anchor matters: the literal string also appears in prose at `CHANGELOG.md:91`).
- `go test -mod=mod ./cmd/moot-sweep/... -count=1` exits 0 (the compile smoke test).
- `grep -n 'sweep pass complete' cmd/moot-sweep/main.go` returns ≥ 1.
- `grep -rn 'MootSweepInterval\|time.NewTicker' cmd/moot-sweep/main.go` shows the ticker is fed only from the package constant; no `flag`, `env:"...INTERVAL"`, or `default:"5m"` exists anywhere in `cmd/moot-sweep/` or `pkg/moot_sweep.go`.
</verification>

<!-- OPEN QUESTIONS FOR THE AUDITOR
1. RESOLVED BY REQUIREMENT 1b. The image build gap this item raised is now in scope: the prompt parameterises the build (or adds `cmd/moot-sweep/Dockerfile`) so a `moot-sweep` image can exist at all. Building it remains operator-side.
2. PROBES. `/healthz`, `/readiness` and `/metrics` on `:9090` are not named in the spec, but the post-deploy AC requires the Deployment to read `READY 1/1`, and the org's k8s-binary conventions require these endpoints for any Deployment. The operator's chart template must point its probes at them.
3. LOG VERBOSITY. All sweep lines are `glog.V(2)`, matching the rest of the repo. The post-deploy evidence greps the pod log for `sweep pass`, so the chart template must pass `-v=2` (the reviewer's `ENTRYPOINT` does this for `/main`; a separate image needs its own equivalent — requirement 1b should carry `-v=2` in its `ENTRYPOINT`).
-->
