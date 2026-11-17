---
status: approved
approved: 2026-11-17T17:56:06.820264654Z
kind: bug
---

## Summary

Sentry issue `NUKE-DEV-GX` is a real bug in `bborbe/github-pr-review-agent` at `pkg/git/repo_manager.go:152`, filed automatically by the sentry-fix-agent from the High/High real bug deep verdict — that verdict is the approval gate, so this spec is approved rather than drafted.

## Problem

repo_manager.go:94's taskIDRegexp accepts only lowercase [0-9a-f], so the uppercase UUID 9B4C7E12-6D3A-4F58-A1C9-3E7B5D2F8A64 read verbatim from task frontmatter fails the check at repo_manager.go:152-153 and aborts before any git work.

## Reproduction

The implicated source location from the deep verdict is `pkg/git/repo_manager.go:152` in `bborbe/github-pr-review-agent` (resolution rule: `candidate position 1 (bborbe/github-pr-review-agent)`), Sentry issue `NUKE-DEV-GX`.

## Recommended Fix

Canonicalize the task ID (strings.ToLower(strings.TrimSpace(...)) or parse via github.com/google/uuid) before validation and before filepath.Join into the worktree path, keep the regex as defense-in-depth, and add tests for uppercase, malformed, and empty task_identifier.
