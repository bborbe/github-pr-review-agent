// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package citations_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/bborbe/github-pr-review-agent/pkg/citations"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func writeFile(content string) string {
	path := filepath.Join(GinkgoT().TempDir(), "file.json")
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
	return path
}

func ruleIndex() string {
	return writeFile(`[{"id":"go-error-wrapping"},{"id":"go-glog"}]`)
}

var _ = Describe("Validator", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("keeps a rule finding whose rule_id resolves", func() {
		validator, err := citations.NewValidator(ctx, ruleIndex())
		Expect(err).To(BeNil())

		result, err := validator.Validate(ctx, []byte(
			`{"findings":[{"kind":"rule","rule_id":"go-glog","file":"a.go","line":3}]}`,
		))

		Expect(err).To(BeNil())
		Expect(result.DroppedCount).To(Equal(0))
		Expect(string(result.Findings)).To(ContainSubstring("go-glog"))
	})

	It("drops a rule finding whose rule_id does not resolve, and counts it", func() {
		validator, err := citations.NewValidator(ctx, ruleIndex())
		Expect(err).To(BeNil())

		result, err := validator.Validate(ctx, []byte(
			`{"findings":[{"kind":"rule","rule_id":"drifted-rule","file":"a.go"}]}`,
		))

		Expect(err).To(BeNil())
		Expect(result.DroppedCount).To(Equal(1))
		Expect(result.Dropped[0].Kind).To(Equal(citations.KindRule))
		Expect(result.Dropped[0].ID).To(Equal("drifted-rule"))
		Expect(string(result.Findings)).NotTo(ContainSubstring("drifted-rule"))
	})

	It("treats a finding with no kind as a rule finding", func() {
		validator, err := citations.NewValidator(ctx, ruleIndex())
		Expect(err).To(BeNil())

		result, err := validator.Validate(ctx, []byte(
			`{"findings":[{"rule_id":"go-error-wrapping"}]}`,
		))

		Expect(err).To(BeNil())
		Expect(result.DroppedCount).To(Equal(0))
	})

	It("keeps a toolchain finding without any id", func() {
		validator, err := citations.NewValidator(ctx, ruleIndex())
		Expect(err).To(BeNil())

		result, err := validator.Validate(ctx, []byte(
			`{"findings":[{"kind":"toolchain","package":"github.com/a/b","advisory":"GO-2024-1"}]}`,
		))

		Expect(err).To(BeNil())
		Expect(result.DroppedCount).To(Equal(0))
		Expect(string(result.Findings)).To(ContainSubstring("GO-2024-1"))
	})

	It("drops an invariant finding when no security model is supplied", func() {
		validator, err := citations.NewValidator(ctx, ruleIndex())
		Expect(err).To(BeNil())

		result, err := validator.Validate(ctx, []byte(
			`{"findings":[{"kind":"invariant","invariant_id":"inv-1"}]}`,
		))

		Expect(err).To(BeNil())
		Expect(result.DroppedCount).To(Equal(1))
		Expect(result.Dropped[0].Kind).To(Equal(citations.KindInvariant))
	})

	It("keeps an invariant finding when the model carries its id", func() {
		model := writeFile(`{"invariants":[{"id":"inv-1"}]}`)
		validator, err := citations.NewValidatorWithModel(ctx, ruleIndex(), model)
		Expect(err).To(BeNil())

		result, err := validator.Validate(ctx, []byte(
			`{"findings":[{"kind":"invariant","invariant_id":"inv-1"}]}`,
		))

		Expect(err).To(BeNil())
		Expect(result.DroppedCount).To(Equal(0))
	})

	It("drops a finding of an unknown kind", func() {
		validator, err := citations.NewValidator(ctx, ruleIndex())
		Expect(err).To(BeNil())

		result, err := validator.Validate(ctx, []byte(
			`{"findings":[{"kind":"rumour","rule_id":"go-glog"}]}`,
		))

		Expect(err).To(BeNil())
		Expect(result.DroppedCount).To(Equal(1))
	})

	It("preserves the grouped findings_by_owner shape and names the owner", func() {
		validator, err := citations.NewValidator(ctx, ruleIndex())
		Expect(err).To(BeNil())

		result, err := validator.Validate(ctx, []byte(
			`{"stats":{"findings_count":2},"findings_by_owner":{`+
				`"go-quality-assistant":[{"rule_id":"go-glog"}],`+
				`"go-error-assistant":[{"rule_id":"nope"}]}}`,
		))

		Expect(err).To(BeNil())
		Expect(result.DroppedCount).To(Equal(1))
		Expect(result.Dropped[0].Owner).To(Equal("go-error-assistant"))

		var decoded map[string]interface{}
		Expect(json.Unmarshal(result.Findings, &decoded)).To(Succeed())
		Expect(decoded).To(HaveKey("findings_by_owner"))
		Expect(decoded).To(HaveKey("stats"))
	})

	It("fails when the rule index is missing", func() {
		_, err := citations.NewValidator(ctx, filepath.Join(GinkgoT().TempDir(), "absent.json"))
		Expect(err).NotTo(BeNil())
	})

	It("fails when the findings document is not JSON", func() {
		validator, err := citations.NewValidator(ctx, ruleIndex())
		Expect(err).To(BeNil())

		_, err = validator.Validate(ctx, []byte("not json"))
		Expect(err).NotTo(BeNil())
	})
})
