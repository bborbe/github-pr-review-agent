// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "testing"

// These cases exist because a single undecodable message on the task-event
// topic used to kill the process, and the consumer starts at OffsetOldest, so
// every restart replayed the same message and the pod sat in CrashLoopBackOff.
// Skipping is the contract, not leniency: see decodeTaskEvent's own comment.
func TestDecodeTaskEvent(t *testing.T) {
	const validID = "0f8e1c2a-1111-2222-3333-444455556666"

	tests := []struct {
		name   string
		value  []byte
		wantOK bool
		wantID string
	}{
		{
			name:   "nil value is a compaction tombstone and is skipped",
			value:  nil,
			wantOK: false,
		},
		{
			name:   "zero-length value is skipped",
			value:  []byte{},
			wantOK: false,
		},
		{
			name:   "truncated JSON is skipped rather than fatal",
			value:  []byte(`{"taskIdentifier":"` + validID + `"`),
			wantOK: false,
		},
		{
			name:   "non-JSON bytes are skipped",
			value:  []byte("not json at all"),
			wantOK: false,
		},
		{
			name:   "a valid task event is decoded",
			value:  []byte(`{"taskIdentifier":"` + validID + `"}`),
			wantOK: true,
			wantID: validID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task, ok := decodeTaskEvent(tt.value)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if string(task.TaskIdentifier) != tt.wantID {
				t.Fatalf("task id = %q, want %q", task.TaskIdentifier, tt.wantID)
			}
		})
	}
}
