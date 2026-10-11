# <app> feature map

Behavior-level inventory of <app>. Agents use it to decide what to drive and what counts as proof.
Humans use it as the regression checklist. One file per user-facing area, from `_area.md`.

## Baseline preconditions

- `doctor` passes: fresh build at HEAD, instance owned by this checkout.
- Fixtures: `<seed command>`; test account `<user>` signed in unless the area is sign-in.
- Feature flags at defaults unless a file says otherwise.
- Stable handles first (ARIA role + name, `data-*`, routes); class selectors and coordinates last.
- Wait on end states, not sleeps.

## Proof and skip reporting

- The area file defines the coverage set: every entry point, mode, gated variant, and the
  success / cancel / error / empty / persistence paths the change can affect.
- Capture the action and the final observable state; verify one side effect beyond pixels.
- Unreachable path: name it, name the blocker, cover the closest real path.

## Sweep order

Walk top to bottom for a broad regression, then `journeys.md`.

## <group>

- [<area>](<area>.md): <one line: what a user does here>.
- [<area>](<area>.md): <one line>.

## Journeys

- [journeys](journeys.md): flows that cross areas (multi-tab isolation, offline then reconnect, shortcut sweep).

## Entry contract

Every area file uses the same four H2s:
`Sub-features`, `How to get to it (user POV)`, `Driving it with <harness>`, `Gotchas`.
