# <Area name>

<One sentence: what a user accomplishes here, from which surfaces, and how they confirm it worked.>

## Sub-features

What exists. One bullet per behavior, with a stable id the PR can cite.

- `<area>-<verb>`: <behavior>.
- `<area>-<verb>`: <behavior>.

## How to get to it (user POV)

Every entry point a user has. A proof that drives one of several is incomplete.

- <button / menu / route / keybinding / CLI command>.
- <second entry point>.

## Driving it with <harness>

Preconditions:

- <state the instance and data must be in>.

Steps (action, command, observable end state):

- **<Step>.** <User action>. Run `<harness command with real selector>`. <What appears or changes>.
- **<Persist>.** Reload or reopen. Run `<command>`. <Value survives>.
- **<Error / empty / cancel>.** <Action>. Run `<command>`. <Observable result>.
- **Proof.** Run `<proof skill>/scripts/shot <url> <artifact dir>/<area>.png` (or `record`).
  The artifact shows <exact thing>.

Fault rows that touch this area: <fault matrix ids>.
Perf: <benchmark / query / endpoint to measure, or "none">.

## Gotchas

What usually lies, and the check that catches it.

- <a toast or status alone is not proof: reopen from the list>.
- <async menu or stream: snapshot live rows before choosing>.
- <input normalized on save: assert the rendered value, not the draft>.
- <manual-only path (OS drag, native dialog, system-browser auth): mark as a gap>.
