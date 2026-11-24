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
func NewValidator(indexPath string) (Validator, error) {
	ids, err := loadRuleIDs(indexPath)
	if err != nil {
		return nil, err
	}
	return &validator{ruleIDs: ids, invariantIDs: map[string]struct{}{}}, nil
}

// NewValidatorWithModel additionally loads a security model so invariant-kind
// findings can resolve. A model that cannot be read leaves the invariant set
// empty, which drops those findings — the script's fail-closed behaviour.
func NewValidatorWithModel(indexPath, modelPath string) (Validator, error) {
	ids, err := loadRuleIDs(indexPath)
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
		l.inner, l.err = NewValidator(l.indexPath)
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

// Validate walks the document, keeps findings whose provenance resolves, and
// returns the filtered document plus the drops.
func (v *validator) Validate(ctx context.Context, findings []byte) (Result, error) {
	var root interface{}
	if err := json.Unmarshal(findings, &root); err != nil {
		return Result{}, errors.Wrapf(ctx, err, "unmarshal findings failed")
	}
	result := &Result{}
	filtered := v.filter(root, "", result)
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

// filter returns node with unprovenanced findings removed. owner carries the
// enclosing key so a drop can name the group it came from.
func (v *validator) filter(
	node interface{},
	owner string,
	result *Result,
) interface{} {
	switch typed := node.(type) {
	case []interface{}:
		out := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			switch entry := item.(type) {
			case map[string]interface{}:
				if isFinding(entry) {
					if v.keep(entry, owner, result) {
						out = append(out, entry)
					}
					continue
				}
				out = append(out, v.filter(entry, owner, result))
			case []interface{}:
				out = append(out, v.filter(entry, owner, result))
			default:
				out = append(out, item)
			}
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			out[key] = v.filter(value, key, result)
		}
		return out
	default:
		return node
	}
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

// isFinding distinguishes a finding from a container. A dict carrying any
// provenance key is a finding; a dict whose values are all scalars is a leaf and
// therefore a finding too (the script's leaf-dict branch, which exists so an
// unprovenanced leaf is counted rather than mistaken for a container).
func isFinding(item map[string]interface{}) bool {
	for _, key := range []string{"kind", "rule_id", "invariant_id"} {
		if _, ok := item[key]; ok {
			return true
		}
	}
	for _, value := range item {
		switch value.(type) {
		case map[string]interface{}, []interface{}:
			return false
		}
	}
	return true
}

func loadRuleIDs(path string) (map[string]struct{}, error) {
	content, err := os.ReadFile(path) // #nosec G304 -- path is the plugin's own rules index
	if err != nil {
		return nil, errors.Wrapf(context.Background(), err, "read rule index path=%s", path)
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(content, &entries); err != nil {
		return nil, errors.Wrapf(context.Background(), err, "parse rule index path=%s", path)
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
