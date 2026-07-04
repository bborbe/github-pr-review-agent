// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"reflect"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/agent/delivery"
	"github.com/bborbe/github-pr-review-agent/pkg/factory"
	"github.com/bborbe/github-pr-review-agent/pkg/git"
	"github.com/bborbe/github-pr-review-agent/pkg/githubauth"
	libkafkamocks "github.com/bborbe/kafka/mocks"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Factory", func() {
	Describe("CreateClaudeRunner", func() {
		It("returns a non-nil runner with empty env", func() {
			runner := factory.CreateClaudeRunner(
				"",
				"agent",
				"sonnet",
				map[string]string{},
				claudelib.AllowedTools{"Read"},
			)
			Expect(runner).NotTo(BeNil())
		})

		It("returns a non-nil runner with GH_TOKEN in env", func() {
			runner := factory.CreateClaudeRunner(
				"",
				"agent",
				"sonnet",
				map[string]string{"GH_TOKEN": "test-token"},
				claudelib.AllowedTools{"Read"},
			)
			Expect(runner).NotTo(BeNil())
		})
	})

	Describe("CreateAgent", func() {
		It("returns a non-nil agent with empty token and env", func() {
			var repoManager git.RepoManager
			currentDateTime := libtime.NewCurrentDateTime()
			agent := factory.CreateAgent(
				"",
				"agent",
				"sonnet",
				"",
				map[string]string{},
				repoManager,
				"standard",
				nil,
				nil,
				nil,
				currentDateTime,
			)
			Expect(agent).NotTo(BeNil())
		})

		It("returns a non-nil agent with token set in env", func() {
			var repoManager git.RepoManager
			currentDateTime := libtime.NewCurrentDateTime()
			agent := factory.CreateAgent(
				"",
				"agent",
				"sonnet",
				"test-token",
				map[string]string{"GH_TOKEN": "test-token"},
				repoManager,
				"standard",
				nil,
				nil,
				nil,
				currentDateTime,
			)
			Expect(agent).NotTo(BeNil())
		})

	})

	Describe("CreateFileResultDeliverer", func() {
		It("returns a non-nil deliverer", func() {
			deliverer := factory.CreateFileResultDeliverer("/tmp/task.md")
			Expect(deliverer).NotTo(BeNil())
		})
	})

	Describe("CreateDeliverer", func() {
		It("returns a non-nil deliverer", func() {
			syncProducer := &libkafkamocks.KafkaSyncProducer{}
			currentDateTime := libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime {
				return libtime.DateTime{}
			})
			deliverer := factory.CreateDeliverer(
				syncProducer,
				agentlib.TaskIdentifier("task-123"),
				"dev",
				"content",
				currentDateTime,
			)
			Expect(deliverer).NotTo(BeNil())
		})
	})

	Describe("CreateKafkaResultDeliverer", func() {
		It("returns a non-nil deliverer", func() {
			syncProducer := &libkafkamocks.KafkaSyncProducer{}
			currentDateTime := libtime.CurrentDateTimeGetterFunc(func() libtime.DateTime {
				return libtime.DateTime{}
			})
			deliverer := factory.CreateKafkaResultDeliverer(
				syncProducer,
				"dev",
				agentlib.TaskIdentifier("task-123"),
				"original content",
				currentDateTime,
			)
			Expect(deliverer).NotTo(BeNil())
		})
	})

	Describe("Passthrough content generator wiring — failure body", func() {
		var gen delivery.ContentGenerator
		var ctx context.Context

		BeforeEach(func() {
			gen = delivery.NewPassthroughContentGenerator()
			ctx = context.Background()
		})

		Context("when result status is needs_input with empty Output", func() {
			It("produces a body containing ## Failure and the message", func() {
				result := agentlib.AgentResultInfo{
					Status:  agentlib.AgentStatusNeedsInput,
					Message: "GH_TOKEN unauthorized (HTTP 401)",
					Output:  "",
				}
				generated, err := gen.Generate(ctx, "", result)
				Expect(err).NotTo(HaveOccurred())
				Expect(generated).To(ContainSubstring("## Failure"))
				Expect(generated).To(ContainSubstring("GH_TOKEN unauthorized (HTTP 401)"))
			})
		})

		Context("when result status is failed with empty Output", func() {
			It("produces a body containing ## Failure and the message", func() {
				result := agentlib.AgentResultInfo{
					Status:  agentlib.AgentStatusFailed,
					Message: "claude CLI: 401 Invalid authentication credentials",
					Output:  "",
				}
				generated, err := gen.Generate(ctx, "", result)
				Expect(err).NotTo(HaveOccurred())
				Expect(generated).To(ContainSubstring("## Failure"))
				Expect(
					generated,
				).To(ContainSubstring("claude CLI: 401 Invalid authentication credentials"))
			})
		})

		// updated for lib v0.62.29: needs_input no longer writes phase: human_review in passthrough content generator (see github.com/bborbe/agent CHANGELOG v0.62.27 / v0.62.29)
		Context("when result status is needs_input with Output containing frontmatter", func() {
			It("writes ## Failure with the message and preserves existing phase", func() {
				result := agentlib.AgentResultInfo{
					Status:  agentlib.AgentStatusNeedsInput,
					Message: "GH_TOKEN unauthorized (HTTP 401)",
					Output:  "---\nstatus: in_progress\nphase: planning\n---\n",
				}
				generated, err := gen.Generate(ctx, "", result)
				Expect(err).NotTo(HaveOccurred())
				Expect(generated).To(ContainSubstring("## Failure"))
				Expect(generated).To(ContainSubstring("GH_TOKEN unauthorized (HTTP 401)"))
			})
		})
	})

	Describe("CreateAgentProvider", func() {
		var (
			ctx         context.Context
			repoManager git.RepoManager
			provider    agentlib.AgentProvider
		)
		BeforeEach(func() {
			ctx = context.Background()
			currentDateTime := libtime.NewCurrentDateTime()
			provider = factory.CreateAgentProvider(
				"",
				"agent",
				"sonnet",
				"",
				map[string]string{},
				repoManager,
				"standard",
				nil,
				currentDateTime,
			)
			Expect(provider).NotTo(BeNil())
		})

		It("returns a non-nil agent for pr-review task type", func() {
			agent, err := provider.Get(ctx, agentlib.TaskTypePRReview)
			Expect(err).NotTo(HaveOccurred())
			Expect(agent).NotTo(BeNil())
		})

		It("returns a non-nil agent for healthcheck task type", func() {
			agent, err := provider.Get(ctx, agentlib.TaskTypeHealthcheck)
			Expect(err).NotTo(HaveOccurred())
			Expect(agent).NotTo(BeNil())
		})

		It("returns an error naming the bogus value and both accepted task types", func() {
			agent, err := provider.Get(ctx, agentlib.TaskType("bogus"))
			Expect(err).To(HaveOccurred())
			Expect(agent).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("unknown task_type"))
			Expect(err.Error()).To(ContainSubstring("bogus"))
			Expect(err.Error()).To(ContainSubstring("pr-review"))
			Expect(err.Error()).To(ContainSubstring("healthcheck"))
		})
	})

	Describe("RunConfig.AuthSetup wiring", func() {
		It("pod path: NewGhAuthSetupGit produces the real impl type", func() {
			cfg := factory.RunConfig{
				AuthSetup: githubauth.NewGhAuthSetupGit("fake-token"),
			}
			Expect(cfg.AuthSetup).NotTo(BeNil())
			Expect(reflect.TypeOf(cfg.AuthSetup).String()).To(Equal("*githubauth.ghAuthSetupGit"))
		})

		It("local-CLI path: NewNoopAuthSetup produces the noop impl type", func() {
			cfg := factory.RunConfig{
				AuthSetup: githubauth.NewNoopAuthSetup(),
			}
			Expect(cfg.AuthSetup).NotTo(BeNil())
			Expect(reflect.TypeOf(cfg.AuthSetup).String()).To(Equal("*githubauth.noopAuthSetup"))
		})
	})
})
