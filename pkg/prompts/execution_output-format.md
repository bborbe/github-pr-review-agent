Final response MUST be a single JSON object with this schema:

```json
{
  "verdict": "approve | request-changes",
  "summary": "1-2 sentence overall assessment",
  "comments": [
    {
      "file": "path/to/file.go",
      "line": 42,
      "severity": "critical | major | minor | nit",
      "message": "..."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "security: rate-limit on new endpoint",
      "disposition": "addressed",
      "detail": "rate limit added in handler.go:45"
    },
    {
      "concern": "correctness: context propagation",
      "disposition": "not-an-issue",
      "detail": "examined query.go:88 — already propagated"
    }
  ]
}
```

Field rules:
- `verdict`: required, one of the listed values
- `summary`: required, single short paragraph
- `comments`: required, may be empty list for `approve` with no nits
- Each comment requires `file`, `line`, `severity`, `message`
- `concerns_addressed`: required, one object per concern from `## Plan`. Each
  object has:
  - `concern` (required): the concern text from `## Plan`
  - `disposition` (required): exactly one of `addressed`, `not-an-issue`,
    `not-verified`
  - `detail` (optional): free-text explanation of the disposition
- The three dispositions are mutually exclusive:
  - `addressed` — a code change or comment resolves the concern
  - `not-an-issue` — you examined the concern and confirmed it is not an issue
  - `not-verified` — the time budget stopped you before you could examine it;
    it means ONLY that you never looked at the concern. A concern you examined
    is `not-an-issue`, never `not-verified`. If you examined the concern but
    the sandbox could not complete the verification (e.g. no toolchain to run a
    dependency-graph check) and CI/precommit is the verifier, that is STILL
    `not-an-issue`: write `detail` naming the verifier (e.g. "CI precommit
    runs go mod tidy/verify + build") that will exercise it.
- A concern listed as `not-verified` fail-closes an `approve` to
  `request-changes` ONLY when it is a MUST-tier / load-bearing blocker (the
  merge depends on verifying it — "must verify", "will never fire",
  "blocking") or a bare unexamined admission with no explanation. A
  `not-verified` concern that explains the gap as benign — e.g. the
  toolchain is unavailable in the sandbox but the diff is otherwise clean and
  CI/precommit is the gate — must NOT fail-close: disposition it
  `not-an-issue` with the verifier named in `detail`; the CI gate decides.

Output the JSON inside a fenced code block (```json ... ```). No prose before or after the fence. The fence renders the JSON readably in Obsidian and other markdown viewers; downstream consumers strip the fence before parsing.
