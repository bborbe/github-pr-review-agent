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

// SweepTask evaluates one tracked task through the shipped routing and
// publishes the closure when the routing decided the review is moot.
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

// buildMootMarkdown parses the task body and attaches a COPY of the event's
// frontmatter, so mutating the returned markdown never corrupts the record the
// tracker still holds.
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

// mootSpawnState reports whether a job had already been assigned when the
// task was closed: "job_assigned" when the executor recorded a current job,
// "none" otherwise.
func mootSpawnState(task agentlib.Task) string {
	if task.Frontmatter.CurrentJob() != "" {
		return "job_assigned"
	}
	return "none"
}
