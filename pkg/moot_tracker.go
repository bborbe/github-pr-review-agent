// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"sort"
	"sync"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"
)

// mootSweepAssignee is the task assignee the reviewer claims. Only tasks
// assigned to this agent are eligible for the pod-less moot closer.
const mootSweepAssignee = agentlib.TaskAssignee("pr-reviewer-agent")

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

// isTrackableReviewTask reports whether task is a review task that is still
// in a phase a closer may act on: assigned to the reviewer, status
// in_progress, phase planning / execution / ai_review.
func isTrackableReviewTask(task agentlib.Task) bool {
	if task.Frontmatter.Assignee() != mootSweepAssignee {
		return false
	}
	if task.Frontmatter.Status() != domain.TaskStatusInProgress {
		return false
	}
	phase := task.Frontmatter.Phase()
	if phase == nil {
		return false
	}
	switch *phase {
	case domain.TaskPhasePlanning, domain.TaskPhaseExecution, domain.TaskPhaseAIReview:
		return true
	default:
		return false
	}
}

// Observe admits task when it is still closeable, evicts the entry when it is
// not, and otherwise replaces any previously stored payload for the same task
// id.
func (t *mootTracker) Observe(ctx context.Context, task agentlib.Task) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !isTrackableReviewTask(task) {
		phase := ""
		if p := task.Frontmatter.Phase(); p != nil {
			phase = string(*p)
		}
		glog.V(2).Infof(
			"moot-tracker: evict task=%s assignee=%s status=%s phase=%s",
			task.TaskIdentifier,
			task.Frontmatter.Assignee(),
			task.Frontmatter.Status(),
			phase,
		)
		delete(t.tasks, task.TaskIdentifier)
		return
	}
	t.tasks[task.TaskIdentifier] = task
}

// Remove drops the entry for id. A no-op when the id is unknown.
func (t *mootTracker) Remove(id agentlib.TaskIdentifier) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.tasks, id)
}

// List returns a snapshot of the tracked tasks, ordered by task id so a sweep
// pass is deterministic.
func (t *mootTracker) List() []agentlib.Task {
	t.mu.Lock()
	defer t.mu.Unlock()

	tasks := make([]agentlib.Task, 0, len(t.tasks))
	for _, task := range t.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].TaskIdentifier < tasks[j].TaskIdentifier
	})
	return tasks
}
