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
    is `not-an-issue`, never `not-verified`.
- A concern listed as `not-verified` makes the review incomplete: an `approve`
  verdict carrying any `not-verified` concern is fail-closed to
  `request-changes`.

Output the JSON inside a fenced code block (```json ... ```). No prose before or after the fence. The fence renders the JSON readably in Obsidian and other markdown viewers; downstream consumers strip the fence before parsing.
