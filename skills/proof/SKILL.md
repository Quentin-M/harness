---
name: proof
description: Use when a change needs visible evidence (UI, API, CLI/TUI, perf) before a verdict, a PR, or "done". Captures screenshots, video, terminal recordings and diagrams with its capture scripts, reads every artifact back, and attaches 1-3 verification bullets plus media to the PR.
---

# Proof

A claim without an artifact is a guess. Capture it, read it back, attach it.

Scripts live in `scripts/` next to this file (call it `$V`). Every script has `--help`.
Write artifacts to `.pod/<slug>/proof/` or `/tmp/proof-<pr>/`, never into tracked source.

## 1. Pick the capture per surface

| Surface | Capture | Read back as |
|---|---|---|
| Web UI, one state | `$V/shot <url> out.png [--viewport 1280x800] [--full]` | the PNG |
| Web UI, a flow | `$V/record <url> out.webm --script steps.js` | `$V/frames out.webm dir/ --every 1s` |
| API | `curl -sS -D- ... \| tee out.txt` (status, headers, body) | the text file |
| CLI / TUI | `$V/term "<cmd>" out.mp4` or `$V/term flow.tape out.gif` | `$V/frames out.mp4 dir/` |
| Perf | `benchstat old.txt new.txt > bench.txt`, `EXPLAIN (ANALYZE, BUFFERS)` output | the text files |
| Architecture / data flow | `$V/diagram in.mmd\|in.d2\|in.dot out.png` | the PNG |

`steps.js` is a CommonJS module: `module.exports = async (page) => { await page.click('text=Save'); }`.
Drive the production user path (clicks, keys, real requests). Wait on visible end states, not sleeps.

## 2. Read every artifact back

Models read images, not video. For each artifact:

1. Open it with the image-capable read tool (PNG, GIF frame, text file).
2. For video, read the first, a middle and the last frame at minimum.
3. Write one line per artifact: what it shows, and whether that matches the claim.
4. If it does not show the claimed behavior, the claim fails. Do not attach it as proof.

A blank page, a spinner, a login wall or a stale build is not evidence.

## 3. Show trigger and end state

- Same capture holds the action and the stable result (before/after, or one recording).
- Verify side effects beyond pixels: DB row, file contents, network log, reload round trip.
- Error, empty and cancel paths count when the change touches them.

## 4. Attach to the PR

```sh
$V/pr-proof <pr> out.png flow.webm \
  -m "Save persists the title across reload (shot: after-reload.png)" \
  -m "Invalid title shows inline error, no request sent (flow.webm, frames 3-5)"
```

- 1-3 bullets. Each bullet = one observable fact plus the artifact that shows it.
- Images embed inline; video and text attach as links.
- Media lands on the orphan `proof-media` branch at `pr-<n>/<head-sha>/`, commit-pinned, so
  links survive force-pushes and render for anyone who can read the repo (private included).
- A new head SHA needs fresh proof. Old comments stay as history.

## Gotchas

| Lies | Truth check |
|---|---|
| "it opens" screenshot | the screenshot shows the changed behavior, not just the page |
| video looks fine in the summary | you read its frames yourself |
| `networkidle` passed | the element you assert on is visible in the PNG |
| vhs output typed before shell was ready | first frame shows a clean prompt |
| perf "faster" | benchstat shows the delta with p-value, same machine, same count |

## Tool setup

Playwright and mermaid-cli are version-pinned in `$V/lib.sh` and install on first use under
`$HARNESS_STATE/playwright`. `term` needs `brew install vhs`, `diagram` needs `brew install d2 graphviz`
for those formats; a missing tool exits 2 with its install command.
