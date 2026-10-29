// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command moot-sweep is the long-running pod-less closer for PR-review tasks.
//
// It consumes the agent task-event topic into an in-memory tracker, then on a
// fixed five-minute tick applies the shipped PR-state routing and publishes a
// closure for every task whose review is moot. It mints the same GitHub App
// installation token the reviewer pod mints and writes through the same CQRS
// command path, so no new credential or write mechanism is introduced.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	sarama "github.com/IBM/sarama"
	agentlib "github.com/bborbe/agent"
	base "github.com/bborbe/cqrs/base"
	cdb "github.com/bborbe/cqrs/cdb"
	"github.com/bborbe/errors"
	prpkg "github.com/bborbe/github-pr-review-agent/pkg"
	github "github.com/bborbe/github-pr-review-agent/pkg/github"
	libhttp "github.com/bborbe/http"
	libkafka "github.com/bborbe/kafka"
	log "github.com/bborbe/log"
	run "github.com/bborbe/run"
	libsentry "github.com/bborbe/sentry"
	service "github.com/bborbe/service"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
	promhttp "github.com/prometheus/client_golang/prometheus/promhttp"

	githubapp "github.com/bborbe/maintainer/githubapp"
)

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

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
	AppID          int64  `required:"false" arg:"app-id"          env:"APP_ID"          usage:"GitHub App ID (numeric); required for App auth"`
	InstallationID int64  `required:"false" arg:"installation-id" env:"INSTALLATION_ID" usage:"GitHub App Installation ID (numeric)"`
	PEMKeyFile     string `required:"false" arg:"pem-key-file"    env:"PEM_KEY_FILE"    usage:"Path to the GitHub App private key (PEM file mounted from k8s Secret)"`
	PEMKey         string `required:"false" arg:"pem-key"         env:"PEM_KEY"         usage:"GitHub App private key (PEM) as env var content; mutually exclusive with PEM_KEY_FILE" display:"length"`

	// VaultAPIBaseURL points at the vault-api instance that serves the task
	// records the tracker holds. When set, every closure decision reads the
	// LIVE record instead of the replayed event snapshot.
	//
	// Empty is allowed so a missing value cannot crashloop the Deployment, but
	// it is NOT a supported mode: without it the sweep decides from a snapshot
	// that can be months older than the file, which is what closed five merged
	// records as `aborted` on 2026-10-29. Startup warns when it is empty.
	VaultAPIBaseURL string `required:"false" arg:"vault-api-base-url" env:"VAULT_API_BASE_URL" usage:"Base URL of the vault-api serving task records; empty disables the live read (stale-snapshot mode)"`
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	// Fail fast on bad credentials: a Deployment that starts with a token it
	// cannot mint would otherwise run for its whole life closing nothing.
	if _, err := a.resolveAuth(ctx); err != nil {
		return err
	}
	tracker := prpkg.NewMootTracker()
	consumer, err := a.createConsumer(ctx, tracker)
	if err != nil {
		return err
	}
	sender, cleanup, err := a.createSender(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	return service.Run(
		ctx,
		consumer.Consume,
		func(ctx context.Context) error { return a.runTickLoop(ctx, tracker, sender) },
		a.createHTTPServer(),
	)
}

// resolveAuth mints a GitHub App installation token and returns it. The token
// is a runtime value (not a config input), so it is returned to the caller
// rather than stored on the argument-parsed application struct.
func (a *application) resolveAuth(ctx context.Context) (string, error) {
	hasPEMFile := a.PEMKeyFile != ""
	hasPEMContent := a.PEMKey != ""
	useGitHubApp := a.AppID != 0 && a.InstallationID != 0 && (hasPEMFile || hasPEMContent)
	if !useGitHubApp {
		return "", errors.Errorf(
			ctx,
			"moot-sweep auth: GitHub App credentials not configured — set APP_ID, INSTALLATION_ID, and PEM_KEY_FILE (or PEM_KEY)",
		)
	}
	var iat string
	var err error
	if hasPEMFile {
		iat, err = githubapp.MintIAT(ctx, githubapp.Config{
			AppID:          a.AppID,
			InstallationID: a.InstallationID,
			PEMPath:        a.PEMKeyFile,
		})
	} else {
		iat, err = githubapp.MintIAT(ctx, githubapp.Config{
			AppID:          a.AppID,
			InstallationID: a.InstallationID,
			PEM:            []byte(a.PEMKey),
		})
	}
	if err != nil {
		return "", errors.Wrap(ctx, err, "mint github app iat")
	}
	glog.V(2).Infof(
		"moot-sweep auth mode=github-app app_id=%d installation_id=%d",
		a.AppID, a.InstallationID,
	)
	return iat, nil
}

// createSender returns the long-lived CQRS command sender. The GitHub client is
// deliberately NOT built here: it is rebuilt per pass from a freshly minted
// installation token (see runTickLoop), because this process outlives a token.
// The returned cleanup closes the sync producer and is safe to call once.
func (a *application) createSender(
	ctx context.Context,
) (cdb.CommandObjectSender, func(), error) {
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
	return sender, cleanup, nil
}

// taskReader returns the live-record reader, or nil when no vault-api URL is
// configured. nil is a real mode (the sweep falls back to the replayed event),
// so it warns loudly rather than failing: a crashloop is worse than a loud
// degradation, and the deployment sets the value.
func (a *application) taskReader() prpkg.MootTaskReader {
	if a.VaultAPIBaseURL == "" {
		glog.Warningf(
			"moot-sweep: VAULT_API_BASE_URL is empty — deciding from the replayed event snapshot. " +
				"Closures made in this mode can act on stale state (stale status, stale current_job).",
		)
		return nil
	}
	return newVaultTaskReader(a.VaultAPIBaseURL)
}

// vaultTaskReader reads a task record from vault-api's
// GET /api/v1/tasks/{id}, which returns {id,title,frontmatter,body}.
type vaultTaskReader struct {
	baseURL string
	client  *http.Client
}

func newVaultTaskReader(baseURL string) *vaultTaskReader {
	return &vaultTaskReader{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (r *vaultTaskReader) ReadTask(
	ctx context.Context,
	id agentlib.TaskIdentifier,
) (agentlib.TaskFrontmatter, string, error) {
	requestURL := r.baseURL + "/api/v1/tasks/" + url.PathEscape(string(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, "", errors.Wrap(ctx, err, "build vault-api request")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, "", errors.Wrap(ctx, err, "read task from vault-api")
	}
	defer func() { _ = resp.Body.Close() }()
	// 404 is "the record is gone", not an error: the sweep must leave it alone.
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", errors.Errorf(
			ctx, "vault-api GET /api/v1/tasks/%s returned %d", id, resp.StatusCode,
		)
	}
	var document struct {
		Frontmatter map[string]interface{} `json:"frontmatter"`
		Body        string                 `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&document); err != nil {
		return nil, "", errors.Wrap(ctx, err, "decode vault-api task document")
	}
	return agentlib.TaskFrontmatter(document.Frontmatter), document.Body, nil
}

// decodeTaskEvent decodes one task-event message into a Task, reporting whether
// the message is a task event at all.
//
// A message that cannot be decoded is SKIPPED, never returned as an error, and
// that is load-bearing rather than lenient. This consumer starts at
// OffsetOldest and rebuilds its entire view on every restart, so a single
// undecodable message is replayed on every start: returning an error here does
// not fail one message, it holds the pod in CrashLoopBackOff forever. That is
// not hypothetical — the first dev deploy died on
//
//	application failed: run failed: consume message failed:
//	consume message failed: unmarshal task event: unexpected end of JSON input
//
// and restarted into the same message three times in under three minutes.
//
// Two shapes are expected and both are skipped: an empty value is a Kafka
// TOMBSTONE left behind by topic compaction, and a value that is not JSON is
// another producer's event on the same topic. Neither carries task state, and
// neither may take the sweep down.
func decodeTaskEvent(value []byte) (agentlib.Task, bool) {
	var task agentlib.Task
	if len(value) == 0 {
		glog.V(2).Infof("moot-sweep: skip empty task event (tombstone)")
		return task, false
	}
	// The event topic value is the JSON of the base.Event map produced by
	// base.ParseEvent; its keys line up with the agentlib.Task JSON tags, so
	// decoding straight into Task is the same path the tracker test locks.
	if err := json.Unmarshal(value, &task); err != nil {
		glog.Warningf("moot-sweep: skip undecodable task event: %v", err)
		return task, false
	}
	return task, true
}

// createConsumer reads the task-event topic from the oldest offset into the
// tracker. OffsetOldest is deliberate: every restart rebuilds the in-memory
// view from the topic, which is the recovery behaviour the sweep relies on.
func (a *application) createConsumer(
	ctx context.Context,
	tracker prpkg.MootTracker,
) (libkafka.Consumer, error) {
	saramaClient, err := libkafka.CreateSaramaClient(ctx, a.KafkaBrokers)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "create sarama client")
	}
	handler := libkafka.MessageHandlerFunc(
		func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			task, ok := decodeTaskEvent(msg.Value)
			if !ok {
				return nil
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
}

// runTickLoop sweeps once per MootSweepInterval until the context is cancelled.
//
// The GitHub installation token is re-minted on every pass. This process is a
// long-lived Deployment, not the reviewer's per-run Job: a token minted once at
// startup expires after about an hour, and from then on every gh call fails —
// which the shipped routing correctly treats as a no-op, so the sweep would
// stop closing anything while readiness kept reporting 200.
func (a *application) runTickLoop(
	ctx context.Context,
	tracker prpkg.MootTracker,
	sender cdb.CommandObjectSender,
) error {
	ticker := time.NewTicker(prpkg.MootSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			token, err := a.resolveAuth(ctx)
			if err != nil {
				// Skip this pass, keep the pod: a transient mint failure must
				// not restart the Deployment, and a permanent one is loud here
				// instead of silent.
				glog.Errorf("moot-sweep: refresh github app iat failed: %v", err)
				continue
			}
			sweep := prpkg.NewMootSweep(
				tracker, a.taskReader(), github.NewGHClient(token), sender,
				libtime.NewCurrentDateTime(),
			)
			closed, err := sweep.SweepPass(ctx)
			if err != nil {
				if ctx.Err() != nil {
					// Cancellation mid-pass is a normal shutdown, not a failure.
					return nil
				}
				return errors.Wrap(ctx, err, "sweep pass")
			}
			glog.V(2).Infof(
				"sweep pass complete closed=%d task_ids=%v", len(closed), closed,
			)
		}
	}
}

// createHTTPServer serves the probes the Deployment needs and the metrics
// endpoint. Without them a long-running pod can never read READY 1/1.
func (a *application) createHTTPServer() run.Func {
	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", ok)
	mux.HandleFunc("GET /readiness", ok)
	mux.Handle("GET /metrics", promhttp.Handler())
	return libhttp.NewServer(a.Listen, mux)
}
