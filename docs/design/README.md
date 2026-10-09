# Technical design documents

This directory holds the **technical design documents** for eino and eino-ext.
They are written in a technical style, in **English and French**, and use
**mermaid** diagrams for schemas and flows.

## Documents

| Document | English | Français | Covers |
|---|---|---|---|
| eino framework & adk | [`01-eino-framework-and-adk.en.md`](./01-eino-framework-and-adk.en.md) | [`01-eino-framework-and-adk.fr.md`](./01-eino-framework-and-adk.fr.md) | The eino framework (`v0.9.12`): component abstractions, `schema.Message`, identifying thinking / tool calls / final answer, streaming, `compose` orchestration, the `adk` subpackage (Agent, Runner, ChatModelAgent, middleware, callbacks), and the eino callback system. |
| eino-ext shared libraries | [`02-eino-ext-shared-libraries.en.md`](./02-eino-ext-shared-libraries.en.md) | [`02-eino-ext-shared-libraries.fr.md`](./02-eino-ext-shared-libraries.fr.md) | The eino-ext building blocks: safety / approval / mutation machinery, conversation memory (history), context compaction, cost saving & observability, the long-term memory agent, the callbacks, `libs/` helpers, and the MCP servers. |

## Related plans

- MCP servers per tool family (implementation plan):
  [`.opencode/plans/mcp-servers-per-tool-family.md`](../../.opencode/plans/mcp-servers-per-tool-family.md)

## Conventions

- **Bilingual:** every design doc exists in English (`*.en.md`) and French
  (`*.fr.md`). The English version is the reference; keep both in sync.
- **Diagrams:** use mermaid (` ```mermaid ` blocks) for architectures, flows,
  sequences, and class diagrams.
- **Code:** identifiers, type names, and code blocks stay in English in both
  versions; only prose and diagram labels are translated.
- **Placement:** design docs live here, in `docs/design/`. Component-level usage
  docs stay in each package's `README.md`.
- **Implementation depth:** each doc ends with an *implementation deep-dives*
  section (EN: `## Implementation deep-dives`, FR: `## Plongées dans
  l'implémentation`) that explains the internal mechanics behind the concepts —
  the code paths, algorithms, and data flow a developer needs to read, modify,
  and debug the code.

## Keeping docs in sync

When you add or change a component, middleware, callback, or lib covered by these
documents, update the matching design doc (both languages) in the same change.
