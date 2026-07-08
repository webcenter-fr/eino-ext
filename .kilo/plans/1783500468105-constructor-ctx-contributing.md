# Plan: Add ctx.Context requirement to CONTRIBUTING.md

## Goal
Add an explicit rule to `CONTRIBUTING.md` that every `New...` constructor must accept `ctx context.Context` as its first parameter and thread it through to client creation.

## Scope
- Single file: `CONTRIBUTING.md`
- One new subsection `### Constructor Context` under `## Components` (after validation rules, around line 129)
- One new checklist item in `## Checklist before PR`

## Content to add

### New subsection (under `## Components`, after the validation requirement block)

```markdown
### Constructor Context

- Every `New...` constructor for a component or shared helper that creates
  remote clients (e.g. `NewClient`, `BuildClients`, `newBaseTool`,
  `osclient.New`) MUST accept `ctx context.Context` as its **first** parameter.
- Thread `ctx` through from the top-level constructor down to every
  underlying client-creation call. Even when the underlying library does
  not yet require a context, having it in the signature keeps the API
  future-proof and consistent across the codebase.
```

### New checklist item (append to the existing checklist)

```markdown
- [ ] Every `New...` constructor accepts `ctx context.Context` as its first
      parameter and threads it through to all client creation calls.
```

## Implementation
1. Edit `CONTRIBUTING.md`: insert the new subsection after the validation requirement block (after line 129, before the "component is considered complete" block).
2. Append a new checklist item to the `## Checklist before PR` section (before `Naming` line).

## Validation
- Read the file to confirm the new section renders correctly and flows naturally.

## Risk
None — documentation-only change.
