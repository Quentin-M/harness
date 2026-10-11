# Code Intelligence

Navigate code with LSP; grep only for text the LSP cannot index (string literals, config values,
comments).

| Need | LSP call |
|---|---|
| jump to source | `goToDefinition`, `goToImplementation` |
| all usages, before any rename | `findReferences` |
| find a definition by name | `workspaceSymbol` |
| list symbols in a file | `documentSymbol` |
| types and docs | `hover` |
| call hierarchy | `incomingCalls`, `outgoingCalls` |

## Trace every path entry to leaf

The leaf is where data is read, written, sent or received. Never stop at an unverified call boundary.

1. The entry point exists and its expected callers reach it.
2. Each layer calls the next with correct arguments and handles the return.
3. Referenced resources exist: tables, queues, files, configs, services.
4. Data shape holds end to end: names, types, units.
5. Error and absence paths are handled, never swallowed.
