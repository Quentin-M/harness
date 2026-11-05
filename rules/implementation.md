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

## Subagents

A subagent sees none of the conversation, so each brief stands alone:

    GOAL      one sentence: the state when it is done
    CONTEXT   file:line pointers and facts found so far, never pasted file bodies
    SCOPE     what it may touch; read-only unless the task is a change
    RETURN    the shape of the answer: verdict first, findings with file:line, a word cap
    DONE      the check that proves it: a command, a test, a question answered

- Read-only discovery and summaries go to the scout (omp `scout`, Claude `Explore`). Code changes
  go to a general subagent. Judgment, the final review and anything Q reads stay with the lead.
- Ask for conclusions, not file dumps: every returned token is re-read on each later lead turn.
