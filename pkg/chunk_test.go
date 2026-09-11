// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	pkg "github.com/bborbe/github-pr-review-agent/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var chunkSectionRegexp = regexp.MustCompile(`(?m)^### Chunk `)

// fileList builds a changed-file inventory from a path → additions map.
func fileList(additions map[string]int) []pkg.ChangedFile {
	paths := make([]string, 0, len(additions))
	for p := range additions {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	files := make([]pkg.ChangedFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, pkg.ChangedFile{Path: p, Additions: additions[p]})
	}
	return files
}

// chunkPaths renders a chunk's files as a slice for equality assertions.
func chunkPaths(c pkg.ReviewChunk) []string {
	return c.Files
}

var _ = Describe("PartitionReviewChunks", func() {
	cfg := pkg.DefaultReviewChunkConfig()

	DescribeTable("partition composition",
		func(files []pkg.ChangedFile, c pkg.ReviewChunkConfig, want [][]string, wantAdds []int) {
			chunks := pkg.PartitionReviewChunks(files, c)
			Expect(chunks).To(HaveLen(len(want)))
			for i, chunk := range chunks {
				Expect(chunkPaths(chunk)).To(Equal(want[i]))
				Expect(chunk.Additions).To(Equal(wantAdds[i]))
			}
		},
		Entry("empty input yields one empty chunk",
			[]pkg.ChangedFile{}, cfg, [][]string{{}}, []int{0}),
		Entry("total at the engage threshold yields one chunk",
			fileList(map[string]int{"a.go": 500}), cfg,
			[][]string{{"a.go"}}, []int{500}),
		Entry("total above the engage threshold yields at least two chunks",
			fileList(map[string]int{"a.go": 301, "b.go": 300}), cfg,
			[][]string{{"a.go"}, {"b.go"}}, []int{301, 300}),
		Entry("path-sorted order regardless of input order",
			[]pkg.ChangedFile{
				{Path: "c.go", Additions: 100},
				{Path: "a.go", Additions: 100},
				{Path: "b.go", Additions: 100},
			}, cfg,
			[][]string{{"a.go", "b.go", "c.go"}}, []int{300}),
		Entry("chunk closes before the additions bound is exceeded",
			fileList(map[string]int{"a.go": 200, "b.go": 200, "c.go": 200}), cfg,
			[][]string{{"a.go"}, {"b.go"}, {"c.go"}}, []int{200, 200, 200}),
		Entry("chunk closes before the file bound is exceeded",
			fileList(map[string]int{"a.go": 100, "b.go": 100, "c.go": 100}),
			pkg.ReviewChunkConfig{EngageAdditions: 100, MaxAdditions: 1000, MaxFiles: 2},
			[][]string{{"a.go", "b.go"}, {"c.go"}}, []int{200, 100}),
		Entry("a file and its _test.go sibling stay in one chunk past the additions bound",
			fileList(map[string]int{"a.go": 200, "a_test.go": 150, "b.go": 100, "c.go": 100}), cfg,
			[][]string{{"a.go", "a_test.go"}, {"b.go", "c.go"}}, []int{350, 200}),
		Entry("an orphan _test.go file is its own unit",
			fileList(map[string]int{"a_test.go": 100, "b.go": 100, "c.go": 400}), cfg,
			[][]string{{"a_test.go", "b.go"}, {"c.go"}}, []int{200, 400}),
		Entry("a unit larger than the additions bound forms its own chunk",
			fileList(map[string]int{"a.go": 700, "b.go": 10, "c.go": 10}), cfg,
			[][]string{{"a.go"}, {"b.go", "c.go"}}, []int{700, 20}),
		Entry("30 files x 20 lines (600) partition into two chunks of 15/300",
			fileList(map[string]int{
				"f01.go": 20, "f02.go": 20, "f03.go": 20, "f04.go": 20, "f05.go": 20,
				"f06.go": 20, "f07.go": 20, "f08.go": 20, "f09.go": 20, "f10.go": 20,
				"f11.go": 20, "f12.go": 20, "f13.go": 20, "f14.go": 20, "f15.go": 20,
				"f16.go": 20, "f17.go": 20, "f18.go": 20, "f19.go": 20, "f20.go": 20,
				"f21.go": 20, "f22.go": 20, "f23.go": 20, "f24.go": 20, "f25.go": 20,
				"f26.go": 20, "f27.go": 20, "f28.go": 20, "f29.go": 20, "f30.go": 20,
			}), cfg,
			[][]string{
				{
					"f01.go", "f02.go", "f03.go", "f04.go", "f05.go",
					"f06.go", "f07.go", "f08.go", "f09.go", "f10.go",
					"f11.go", "f12.go", "f13.go", "f14.go", "f15.go",
				},
				{
					"f16.go", "f17.go", "f18.go", "f19.go", "f20.go",
					"f21.go", "f22.go", "f23.go", "f24.go", "f25.go",
					"f26.go", "f27.go", "f28.go", "f29.go", "f30.go",
				},
			}, []int{300, 300}),
	)

	It("covers every input path exactly once", func() {
		files := fileList(map[string]int{
			"a.go": 200, "a_test.go": 150, "b.go": 100, "c.go": 100, "d.txt": 50,
		})
		chunks := pkg.PartitionReviewChunks(files, cfg)
		seen := map[string]int{}
		for _, chunk := range chunks {
			for _, p := range chunk.Files {
				seen[p]++
			}
		}
		Expect(seen).To(HaveLen(len(files)))
		for _, f := range files {
			Expect(seen[f.Path]).To(Equal(1))
		}
	})
})

var _ = Describe("MergeChunkReviews", func() {
	approveChunk := "```json\n{\"verdict\":\"approve\",\"reason\":\"ok\"}\n```\n"
	changesChunk := "```json\n{\"verdict\":\"request-changes\",\"reason\":\"bad\"}\n```\n"

	It("merges all-approve chunks to approve with one verdict block", func() {
		body, result := pkg.MergeChunkReviews([]string{approveChunk, approveChunk})
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		Expect(pkg.ParseVerdict(body).Verdict).To(Equal(pkg.VerdictApprove))
		Expect(strings.Count(body, "```json")).To(Equal(1))
		Expect(chunkSectionRegexp.FindAllString(body, -1)).To(HaveLen(2))
	})

	It("merges any request-changes chunk to request-changes", func() {
		body, result := pkg.MergeChunkReviews([]string{approveChunk, changesChunk})
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(pkg.ParseVerdict(body).Verdict).To(Equal(pkg.VerdictRequestChanges))
	})

	It("fail-closes an unparseable chunk output to request-changes", func() {
		body, result := pkg.MergeChunkReviews([]string{approveChunk, "no verdict here"})
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(pkg.ParseVerdict(body).Verdict).To(Equal(pkg.VerdictRequestChanges))
	})

	It("applies the blocking gate to an approve chunk carrying a blocking comment", func() {
		blocking := "```json\n" +
			`{"verdict":"approve","reason":"ok","comments":[{"blocking":true}]}` +
			"\n```\n"
		body, result := pkg.MergeChunkReviews([]string{blocking})
		Expect(result.Verdict).To(Equal(pkg.VerdictRequestChanges))
		Expect(pkg.ParseVerdict(body).Verdict).To(Equal(pkg.VerdictRequestChanges))
	})

	It("carries the union of not-verified concerns into the merged verdict block", func() {
		withConcern := "```json\n" +
			`{"verdict":"approve","reason":"ok","concerns_addressed":` +
			`[{"concern":"c1","disposition":"not-verified"}]}` +
			"\n```\n"
		body, result := pkg.MergeChunkReviews([]string{approveChunk, withConcern})
		Expect(result.Verdict).To(Equal(pkg.VerdictApprove))
		Expect(pkg.HasUnverifiedConcerns(body)).To(BeTrue())
		Expect(pkg.ParseVerdict(body).Verdict).To(Equal(pkg.VerdictApprove))
	})

	It("labels each chunk section with its index and count", func() {
		body, _ := pkg.MergeChunkReviews([]string{approveChunk, approveChunk, approveChunk})
		Expect(body).To(ContainSubstring("### Chunk 1/3"))
		Expect(body).To(ContainSubstring("### Chunk 3/3"))
	})
})

var _ = Describe("ValidateReviewChunkConfig", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("accepts the defaults", func() {
		Expect(pkg.ValidateReviewChunkConfig(ctx, pkg.DefaultReviewChunkConfig())).To(Succeed())
	})

	DescribeTable("rejects a non-positive threshold naming its env var",
		func(cfg pkg.ReviewChunkConfig, envVar string) {
			err := pkg.ValidateReviewChunkConfig(ctx, cfg)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(envVar))
		},
		Entry("engage additions is 0",
			pkg.ReviewChunkConfig{EngageAdditions: 0, MaxAdditions: 300, MaxFiles: 15},
			"REVIEW_CHUNK_ENGAGE_ADDITIONS"),
		Entry("max additions is 0",
			pkg.ReviewChunkConfig{EngageAdditions: 500, MaxAdditions: 0, MaxFiles: 15},
			"REVIEW_CHUNK_MAX_ADDITIONS"),
		Entry("max files is 0",
			pkg.ReviewChunkConfig{EngageAdditions: 500, MaxAdditions: 300, MaxFiles: 0},
			"REVIEW_CHUNK_MAX_FILES"),
	)
})

var _ = Describe("ChunkDeadline", func() {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	DescribeTable("per-chunk deadline",
		func(outerDeadline time.Time, remainingChunks int, want time.Time) {
			Expect(pkg.ChunkDeadline(now, outerDeadline, remainingChunks)).To(Equal(want))
		},
		Entry("equal share of a large remaining budget",
			now.Add(10*time.Minute), 5, now.Add(2*time.Minute)),
		Entry("60s floor when the equal share is below 60s",
			now.Add(90*time.Second), 5, now.Add(60*time.Second)),
		Entry("capped at the outer deadline when now+share would pass it",
			now.Add(30*time.Second), 5, now.Add(30*time.Second)),
		Entry("remainingChunks == 1 gives the whole remaining time",
			now.Add(10*time.Minute), 1, now.Add(10*time.Minute)),
		Entry("remainingChunks == 1 with less than 60s left is capped",
			now.Add(30*time.Second), 1, now.Add(30*time.Second)),
		Entry("remainingChunks <= 0 is defensive: the outer deadline",
			now.Add(10*time.Minute), 0, now.Add(10*time.Minute)),
	)
})

var _ = Describe("FilterFindingsByBasenames", func() {
	ctx := context.Background()
	// Three findings across two files plus one with an empty file: the empty-file
	// finding must never survive any chunk.
	const findings = `{"stats":{"yamls_run":66,"findings_count":3,"elapsed_ms":10},` +
		`"findings_by_owner":{"go-error-assistant":[` +
		`{"rule_id":"r1","file":"pkg/a.go","line":3},` +
		`{"rule_id":"r2","file":"pkg/b.go","line":7},` +
		`{"rule_id":"r3","file":"","line":1}]},"errors":[]}`

	type parsedReport struct {
		Stats struct {
			FindingsCount int `json:"findings_count"`
		} `json:"stats"`
		FindingsByOwner map[string][]struct {
			RuleID string `json:"rule_id"`
		} `json:"findings_by_owner"`
		Errors []json.RawMessage `json:"errors"`
	}

	DescribeTable("filters findings to the chunk's basenames",
		func(files []string, wantCount int, wantRules []string) {
			out, err := pkg.FilterFindingsByBasenames(ctx, findings, files)
			Expect(err).NotTo(HaveOccurred())

			var report parsedReport
			Expect(json.Unmarshal([]byte(out), &report)).To(Succeed())
			Expect(report.Stats.FindingsCount).To(Equal(wantCount))
			Expect(report.Errors).NotTo(BeNil())

			rules := make([]string, 0)
			for _, ownerFindings := range report.FindingsByOwner {
				for _, f := range ownerFindings {
					rules = append(rules, f.RuleID)
				}
			}
			sort.Strings(rules)
			Expect(rules).To(Equal(wantRules))
		},
		Entry("a finding whose basename is in the chunk survives",
			[]string{"pkg/a.go"}, 1, []string{"r1"}),
		Entry("a finding from another file drops",
			[]string{"pkg/b.go"}, 1, []string{"r2"}),
		Entry("findings from both chunk files survive",
			[]string{"pkg/a.go", "pkg/b.go"}, 2, []string{"r1", "r2"}),
		Entry("the match is by basename, not full path",
			[]string{"deep/dir/a.go"}, 1, []string{"r1"}),
		Entry("a chunk with no matching findings returns findings_count 0",
			[]string{"pkg/c.go"}, 0, []string{}),
		Entry("an empty chunk file list drops every finding",
			[]string{}, 0, []string{}),
	)

	It("returns a wrapped error for invalid JSON", func() {
		_, err := pkg.FilterFindingsByBasenames(ctx, "not json", []string{"pkg/a.go"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unmarshal chunk funnel findings"))
	})
})
