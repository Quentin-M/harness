# Code Style

- Comments only for a non-obvious why.
- No backward-compat shims, no history notes, no text about what we don't do.
- Concise and visual: ASCII diagram over paragraph.
- Optimized from the first line: pick data structures and performance budgets in the design, never as a later pass.
- Names say what a thing is, in the domain's words. No `utils`, `helpers`, `manager`.
- Errors carry context and propagate; only the edge decides how to report them.
