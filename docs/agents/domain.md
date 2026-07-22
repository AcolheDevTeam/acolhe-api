# Domain docs

This repository uses a single-context domain-documentation layout.

## Before exploring, read these

- Read `CONTEXT.md` at the repository root when it exists.
- Read relevant ADRs under `docs/adr/`.

If these files do not exist, proceed silently. Do not suggest creating them before they are needed. Domain-modeling skills create them when the project resolves terms or architectural decisions.

## File structure

```text
/
├── CONTEXT.md
├── docs/
│   └── adr/
└── src/
```

## Use the glossary's vocabulary

When output names a domain concept, use the term defined in `CONTEXT.md`. This applies to issue titles, proposals, hypotheses, test names, and code.

If the concept is missing, reconsider whether the term belongs to the project. If it exposes a real gap, record it for domain modeling.

## Flag ADR conflicts

Call out any proposal that contradicts an existing ADR. Name the ADR and explain why the decision may need to be reconsidered.
