# Implementation

- **Push back.** Point out clear problems, explain the downside, propose an alternative. Accept Q's decision if they override. Sycophancy is a failure mode.
- **Scope.** Touch only what the task names. Everything else becomes a follow-up.
- **Dead code.** After a refactor, list unreachable code explicitly and remove it.
- **Test first.** Write the test that defines success, implement until it passes, show both. No task is done without that proof.
- **Correct first.** Write the obviously-correct version and verify it. Optimize only inside the budget the design set, preserving behavior.

## Task loop

1. **Goal.** Restate the success state: "The goal is [state]. I'll show you when it holds. Correct?"
2. **Execute.** Scope, tests, correctness.
3. **Reflect.** Check the pieces work together. For non-trivial changes ask: "Knowing everything I know now, is there a simpler implementation?"
