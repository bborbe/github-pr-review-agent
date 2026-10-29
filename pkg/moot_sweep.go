// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"time"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/agent/delivery"
	base "github.com/bborbe/cqrs/base"
	"github.com/bborbe/cqrs/cdb"
	cqrsiam "github.com/bborbe/cqrs/iam"
	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
)

// MootSweepInterval is the fixed interval between sweep passes. Five minutes
// is fixed by the criterion the closer serves; it is deliberately not
// configurable.
const MootSweepInterval = 5 * time.Minute

//counterfeiter:generate -o ../mocks/moot-sweep.go --fake-name MootSweep . MootSweep

//counterfeiter:generate -o ../mocks/moot-task-reader.go --fake-name MootTaskReader . MootTaskReader

// MootTaskReader reads the CURRENT on-disk state of a task record.
//
// The tracker's copy comes from a replayed Kafka event and can be arbitrarily
// older than the file. Deciding from it produced two measured defects on the
// first live pass (2026-10-29): five records whose PR was MERGED were closed
// as `aborted` because the event still said `in_progress`, and four records
// were stamped `spawn_state: none` while carrying a real `current_job` the
// event predated. Every decision therefore reads the live record first.
//
// A nil frontmatter with a nil error means the record does not exist (any
// more) and the sweep must leave it alone.
type MootTaskReader interface {
	ReadTask(
		ctx context.Context,
		id agentlib.TaskIdentifier,
	) (agentlib.TaskFrontmatter, string, error)
}

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
	reader MootTaskReader,
	prState PRStateClient,
	sender cdb.CommandObjectSender,
	currentDateTime libtime.CurrentDateTimeGetter,
) MootSweep {
	return &mootSweep{
		tracker:         tracker,
		reader:          reader,
		prState:         prState,
		sender:          sender,
		currentDateTime: currentDateTime,
	}
}

type mootSweep struct {
	tracker         MootTracker
	reader          MootTaskReader
	prState         PRStateClient
	sender          cdb.CommandObjectSender
	currentDateTime libtime.CurrentDateTimeGetter
}

// SweepTask evaluates one tracked task through the shipped routing and
// publishes the closure when the routing decided the review is moot.
func (s *mootSweep) SweepTask(ctx context.Context, task agentlib.Task) (bool, error) {
	md, ok, err := s.liveMarkdown(ctx, task)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
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
		mootSpawnState(md.Frontmatter),
	)
	return true, nil
}

// liveMarkdown reads the record as it exists on disk right now and parses it.
// ok=false means "do not close": either the record is gone, or its live state
// could not be read. Closing on an unknown live state is exactly the mistake
// that wrote `aborted` onto merged records, so the failure is fail-closed.
func (s *mootSweep) liveMarkdown(
	ctx context.Context,
	task agentlib.Task,
) (*agentlib.Markdown, bool, error) {
	// No reader configured: fall back to the event snapshot. This is the
	// pre-live-read behaviour, kept only so an unset VAULT_API_BASE_URL cannot
	// crashloop the Deployment — it is NOT the correct mode and the stale-
	// snapshot defects return with it, so main logs a startup warning.
	if s.reader == nil {
		return buildMootMarkdown(ctx, task)
	}
	fm, body, err := s.reader.ReadTask(ctx, task.TaskIdentifier)
	if err != nil {
		glog.V(2).Infof(
			"moot-sweep: task=%s live read failed err=%v — not closing",
			task.TaskIdentifier, err,
		)
		return nil, false, nil
	}
	if fm == nil {
		glog.V(2).Infof(
			"moot-sweep: task=%s not present in the vault — not closing",
			task.TaskIdentifier,
		)
		return nil, false, nil
	}
	md, err := agentlib.ParseMarkdown(ctx, body)
	if err != nil {
		return nil, false, errors.Wrapf(ctx, err, "parse live task content")
	}
	md.Frontmatter = fm
	return md, true, nil
}

// SweepPass evaluates every tracked task and returns the ids it closed. A
// task is removed from the tracker only after its closure was published; a
// failure on one task leaves it tracked for the next pass.
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

// writeClosure stamps the closure provenance onto the routed markdown and
// publishes the closed record through the same command path the reviewer pod
// uses.
func (s *mootSweep) writeClosure(
	ctx context.Context,
	task agentlib.Task,
	md *agentlib.Markdown,
) error {
	md.Frontmatter["closed_by"] = "sweep"
	md.Frontmatter["spawn_state"] = mootSpawnState(md.Frontmatter)

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

// buildMootMarkdown parses the task body and attaches a COPY of the event's
// frontmatter, so mutating the returned markdown never corrupts the record the
// tracker still holds. Used only when no MootTaskReader is configured.
func buildMootMarkdown(
	ctx context.Context,
	task agentlib.Task,
) (*agentlib.Markdown, bool, error) {
	md, err := agentlib.ParseMarkdown(ctx, string(task.Content))
	if err != nil {
		return nil, false, errors.Wrapf(ctx, err, "parse task content")
	}
	fm := make(agentlib.TaskFrontmatter, len(task.Frontmatter))
	for k, v := range task.Frontmatter {
		fm[k] = v
	}
	md.Frontmatter = fm
	return md, true, nil
}

// mootSpawnState reports whether a job had already been assigned when the
// task was closed: "job_assigned" when the executor recorded a current job,
// "none" otherwise.
//
// It reads the LIVE frontmatter, never the tracker's event snapshot. The
// event predates the executor's `current_job` write, so the snapshot answered
// "none" on records that had a job — the exact evidence SC1 needs to be
// trustworthy.
func mootSpawnState(fm agentlib.TaskFrontmatter) string {
	if fm.CurrentJob() != "" {
		return "job_assigned"
	}
	return "none"
}
