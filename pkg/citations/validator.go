// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package citations validates the provenance of review findings, ported from the
// coding plugin's `scripts/validate-citations.sh`.
//
// The plugin runs that script through a `simple-bash-runner` agent invocation,
// which the review's fixed tool allowlist does not permit — the same
// invocation-form fragility that moved the mechanical funnel into pkg.FunnelRunner.
// Running it in Go keeps the contract identical: every finding resolves exactly
// one provenance, and a finding that does not is dropped and counted rather than
// silently kept.
//
// The drop count is what distinguishes "no findings" from "findings suppressed",
// which is the difference between an honest zero-findings line and a false clean
// pass.
package citations

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"sync"

	"github.com/bborbe/errors"
)

// Finding kinds. An absent `kind` is treated as KindRule, matching the script's
// legacy path.
const (
	KindRule      = "rule"
	KindInvariant = "invariant"
	KindToolchain = "toolchain"
)

// Drop is one finding removed for want of resolvable provenance.
type Drop struct {
	Owner string
	Kind  string
	ID    string
}

// Result carries the filtered findings document plus what was removed.
type Result struct {
	// Findings is the input document with the unprovenanced findings removed,
	// re-marshalled in the same shape.
	Findings []byte
	// DroppedCount is how many findings were removed.
	DroppedCount int
	// Dropped names each removed finding.
	Dropped []Drop
}

// Validator filters a findings document down to findings with resolvable
// provenance.
type Validator interface {
	Validate(ctx context.Context, findings []byte) (Result, error)
}

// NewValidator loads the rule index at indexPath. A missing or unparseable index
// is an error: the caller must not proceed as though validation had passed.
func NewValidator(ctx context.Context, indexPath string) (Validator, error) {
	ids, err := loadRuleIDs(ctx, indexPath)
	if err != nil {
		return nil, err
	}
	return &validator{ruleIDs: ids, invariantIDs: map[string]struct{}{}}, nil
}

// NewValidatorWithModel additionally loads a security model so invariant-kind
// findings can resolve. A model that cannot be read leaves the invariant set
// empty, which drops those findings — the script's fail-closed behaviour.
func NewValidatorWithModel(ctx context.Context, indexPath, modelPath string) (Validator, error) {
	ids, err := loadRuleIDs(ctx, indexPath)
	if err != nil {
		return nil, err
	}
	return &validator{ruleIDs: ids, invariantIDs: loadInvariantIDs(modelPath)}, nil
}

// NewLazyValidator defers reading the rule index until the first Validate call,
// so composition stays pure (no I/O at construction) and a missing index surfaces
// as an error from Validate rather than from wiring.
func NewLazyValidator(indexPath string) Validator {
	return &lazyValidator{indexPath: indexPath}
}

type lazyValidator struct {
	indexPath string
	once      sync.Once
	inner     Validator
	err       error
}

func (l *lazyValidator) Validate(ctx context.Context, findings []byte) (Result, error) {
	l.once.Do(func() {
		l.inner, l.err = NewValidator(ctx, l.indexPath)
	})
	if l.err != nil {
		return Result{}, l.err
	}
	return l.inner.Validate(ctx, findings)
}

type validator struct {
	ruleIDs      map[string]struct{}
	invariantIDs map[string]struct{}
}

// Validate keeps findings whose provenance resolves and returns the filtered
// document plus the drops.
//
// It filters ONLY the findings themselves — the owner-keyed `findings_by_owner`
// lists, a `findings` list, or a top-level list. Every other key (`stats`,
// `errors`, …) passes through untouched: the validator's job is findings, and
// rewriting the whole container would silently drop the funnel's own error
// report before the model ever sees it.
func (v *validator) Validate(ctx context.Context, findings []byte) (Result, error) {
	var root interface{}
	if err := json.Unmarshal(findings, &root); err != nil {
		return Result{}, errors.Wrapf(ctx, err, "unmarshal findings failed")
	}
	result := &Result{}
	filtered := v.filterDocument(root, result)
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return Result{}, errors.Wrapf(ctx, err, "marshal filtered findings failed")
	}
	result.Findings = encoded
	sort.Slice(result.Dropped, func(i, j int) bool {
		return result.Dropped[i].Owner < result.Dropped[j].Owner
	})
	return *result, nil
}

// filterDocument locates the findings and filters them in place, leaving every
// other part of the document as it was.
func (v *validator) filterDocument(root interface{}, result *Result) interface{} {
	switch typed := root.(type) {
	case []interface{}:
		return v.filterFindings(typed, "", result)
	case map[string]interface{}:
		if byOwner, ok := typed["findings_by_owner"].(map[string]interface{}); ok {
			filtered := make(map[string]interface{}, len(byOwner))
			for owner, value := range byOwner {
				items, ok := value.([]interface{})
				if !ok {
					filtered[owner] = value
					continue
				}
				filtered[owner] = v.filterFindings(items, owner, result)
			}
			typed["findings_by_owner"] = filtered
			return typed
		}
		if items, ok := typed["findings"].([]interface{}); ok {
			typed["findings"] = v.filterFindings(items, "", result)
		}
		return typed
	default:
		return root
	}
}

// filterFindings keeps only the entries whose provenance resolves. owner names
// the group a dropped finding came from.
func (v *validator) filterFindings(
	items []interface{},
	owner string,
	result *Result,
) []interface{} {
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		finding, ok := item.(map[string]interface{})
		if !ok {
			out = append(out, item)
			continue
		}
		if v.keep(finding, owner, result) {
			out = append(out, finding)
		}
	}
	return out
}

// keep reports whether a finding has resolvable provenance, recording a drop when
// it does not.
func (v *validator) keep(
	finding map[string]interface{},
	owner string,
	result *Result,
) bool {
	kind, _ := finding["kind"].(string)
	if kind == "" {
		kind = KindRule
	}
	var id string
	var resolved bool
	switch kind {
	case KindRule:
		id, _ = finding["rule_id"].(string)
		_, resolved = v.ruleIDs[id]
	case KindInvariant:
		id, _ = finding["invariant_id"].(string)
		_, resolved = v.invariantIDs[id]
	case KindToolchain:
		return true
	default:
		resolved = false
	}
	if resolved {
		return true
	}
	result.DroppedCount++
	result.Dropped = append(result.Dropped, Drop{Owner: owner, Kind: kind, ID: id})
	return false
}

// loadRuleIDs reads the rule index and returns its ids.
func loadRuleIDs(ctx context.Context, path string) (map[string]struct{}, error) {
	content, err := os.ReadFile(path) // #nosec G304 -- path is the plugin's own rules index
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "read rule index path=%s", path)
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(content, &entries); err != nil {
		return nil, errors.Wrapf(ctx, err, "parse rule index path=%s", path)
	}
	ids := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.ID != "" {
			ids[entry.ID] = struct{}{}
		}
	}
	return ids, nil
}

// loadInvariantIDs reads invariants[].id from the security model. Any failure
// yields an empty set, so invariant findings drop — never a silent pass.
func loadInvariantIDs(path string) map[string]struct{} {
	ids := map[string]struct{}{}
	if path == "" {
		return ids
	}
	content, err := os.ReadFile(path) // #nosec G304 -- path is the caller's own model file
	if err != nil {
		return ids
	}
	var model struct {
		Invariants []struct {
			ID string `json:"id"`
		} `json:"invariants"`
	}
	if err := json.Unmarshal(content, &model); err != nil {
		return ids
	}
	for _, inv := range model.Invariants {
		if inv.ID != "" {
			ids[inv.ID] = struct{}{}
		}
	}
	return ids
}
