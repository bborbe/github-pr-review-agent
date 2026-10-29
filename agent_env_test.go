// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This file is in package main rather than main_test on purpose: main is not an
// importable package, so the external test package cannot reach `application`
// and an export_test.go seam does not work here. Ginkgo's registry is
// process-global, so TestSuite in main_test.go runs these specs.
//
// The factory composition test starts from an env map that already carries
// HUMAN_REVIEW_OWNER, so it would still pass if this entry point stopped
// populating it — which is exactly the defect that shipped once already.
var _ = Describe("agentEnv", func() {
	It("carries a configured human-review owner", func() {
		env := (&application{HumanReviewOwner: "bborbe"}).agentEnv("")
		Expect(env).To(HaveKeyWithValue("HUMAN_REVIEW_OWNER", "bborbe"))
	})

	It("omits the key when no owner is configured", func() {
		env := (&application{}).agentEnv("")
		Expect(env).NotTo(HaveKey("HUMAN_REVIEW_OWNER"))
	})
})
