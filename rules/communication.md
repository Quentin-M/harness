# Communication

Q has ADHD. Q reads the first line and acts on it, parses a picture in one glance, and loses a
paragraph by its second line. Every reply to Q follows this rule, on top of the Writing Style
floor that covers all text.

## Shape of a reply

    line 1     the action or verdict: a command, a path, yes/no
    status     progress bar, on multi-step work only
    picture    the answer, drawn
    caption    ≤ 4 sentences: what the picture cannot show
    tangent    one line at the end, as a question

- Line 1 is something Q can do now. A failure or a skipped step goes first, ahead of any win.
  Bad: "I've looked at the auth flow and there are a few considerations." Good: "Run
  `npm i jsonwebtoken`, then edit `src/auth.ts:42`."
- Every reply longer than one line holds a picture. When unsure, draw it: an unneeded diagram
  costs two seconds, a skipped paragraph costs the answer.
- Each element says something new. The picture skips line 1; the caption skips the picture.
- Multi-step work opens with the bar right under line 1, matching the task list item for item.
  Show the full list when any item changed state: `██████░░░░ 3/5  schema done. Next: migrate.`
- Procedures are numbered steps, one bounded action each. A step with a branch becomes two.
- Estimates in minutes or hours, with the condition: "15 min if the tests cover it, 2 h if not."
- Name the win in one line: "Login now works with magic links."
- Do what Q asked. A side problem gets one line at the end, as a question.
- No preamble, recap of the ask, flattery or sign-off. Start and end with substance.

## Pick the picture

| Content | Picture |
|---|---|
| Work items, findings | Task list with status glyphs |
| Flow, call path, data path | Boxes and arrows, top-down past 3 hops |
| Request/response between parties | Sequence diagram with lifelines |
| States and transitions | State machine |
| A change | Before │ after, side by side |
| Files, modules, hierarchy | Tree with `+ ~ -` marks |
| Events over time | Timeline |
| Options, comparisons | Markdown table |
| Quantities, ratios | Horizontal bars, numbers right-aligned |
| Where a failure sits | Flow with `✗` on the broken hop, callout below |

## Drawing readable diagrams

A diagram fails when the eye cannot tell which label belongs to which thing. Separation and
alignment fix that; decoration does not.

| Rule | How |
|---|---|
| Grid first | Fix each node's column before drawing. Every `│ ▼ ▲ ┬ ┴` sits in the exact column of the glyph it hangs from. One glyph is one cell: count them. |
| Nodes in boxes | A name inside `╭─╮` reads as a thing, a bare word reads as a label. Pad one space inside; give a row of boxes one width. |
| One label, one place | A label sits on its arrow (`── POST /pay ─▶`), inside its box, or in the notes column. Never on a shared line under two nodes. |
| Gutters | ≥ 3 spaces between unrelated texts on a line. A blank row between bands of nodes. |
| Go vertical | Past 3 hops or with long labels, flow top-down, one node per row. Height is free; width stops at 80 (aim for 72). |
| Notes out of the drawing | Mark the spot with `¹ ² ³`, explain below the fence. Or keep short notes in a column 3+ spaces right of the boxes. |
| Split | One idea per diagram, ≤ 7 nodes. Two small diagrams with their own captions beat one dense one: "write path first, then read path". |
| Weight means something | `━ ┃` main path, `─ │` normal, `┄ ┆` async or optional, `✗` broken. One arrowhead family per diagram. |
| Width-safe glyphs | Box drawing, blocks and the kits below. Emoji and tabs take unknown width and shear every column after them. |
| Read it back | Before sending, scan each column top to bottom and each row left to right. A label that could belong to two things moves. In files, `harness diagrams <file.md>` lints every diagram; `<!-- diagram-lint: off -->` skips the next one. |

### Glyph kit

Every glyph here is one cell wide in MesloLGS NF. Use them the same way every time so Q learns
them once.

<!-- diagram-lint: off -->

    ╭──────╮      ┏━━━━━━━┓      ┌┄┄┄┄┄┄┄┄┄┄┐
    │ node │      ┃ focus ┃      ┆ external ┆
    ╰──────╯      ┗━━━━━━━┛      └┄┄┄┄┄┄┄┄┄┄┘

    status    ✓ done   ◐ doing   ○ open   ✗ failed   ⚠ needs Q   ⊘ blocked
    lines     ━━━▶ main path     ───▶ normal     ┄┄┄▶ async
    arrows    ─▶  ◀─  ▲  ▼    ◀─▶ both ways    ─✗─ broken
    marks     ¹ ² ³ ⁴ ⁵ callout    ← now    ▸ • · bullets
    diff      + added   ~ changed   - removed
    bars      ████████░░░░ 8/12    ▁▂▃▅▇█▆▃ trend    ▰▰▰▱▱ score
    math      × ÷ ≤ ≥ ≠ ≈ ± ∞ ∅

### Nerd Font icons

Q's terminal runs MesloLGS NF, so icons work in chat replies. One icon marks a node's type, put
before its name, the same icon for the same thing everywhere. Each icon draws ~15% past its
cell: follow it with a space, never with a box edge. GitHub and editors show them as tofu, so
files, commits and PR text stay on the Unicode kit.

    infra     󰒋 server   󰆼 db     󱃾 k8s      󰡨 docker   󰅟 cloud   󰖟 web
    network   󰌗 lan      󰟾 fw     󱂛 api      󰚇 queue    󰍩 msg     󰇮 mail
    people    󰀄 user     󰡉 team   󰚩 agent    󰧑 model
    code      󰘬 branch   󰜘 commit 󰓂 PR       󰟓 go       󰃤 bug     󰙨 test
    security  󰌾 locked   󰌿 open   󰌆 key      󰕥 verified 󰻌 alert   󰄤 cert
    files     󰈔 file     󰉋 folder 󰆍 shell
    money     󰄔 cash     󰁰 bank   󰗑 balance
    perf      󰓅 latency  󰄪 chart  󰍛 memory   󰻠 cpu      󰈸 hot     󰔟 waiting

### Examples

Labels crammed under nodes, two notes per line, branches hanging from nowhere:

<!-- diagram-lint: off -->

    input ─▶ parse + validate ─▶ typed value ─▶ core ─▶ encode ─▶ sink
             bounded, allowlist                         per sink    sql, shell,
                    │ fail                                 │ fail   html, path
                    ▼                                      ▼
             deny + log with context              deny + log with context

The same flow, vertical, each branch on its own box, notes in a column:

    ╭──────────────────╮
    │ untrusted input  │    HTTP, queue, file, LLM output
    ╰────────┬─────────╯
             ▼
    ╭──────────────────╮    fail
    │ parse + validate │ ─────────▶ deny + log
    ╰────────┬─────────╯
             ▼              typed value
    ╭──────────────────╮
    │ core             │
    ╰────────┬─────────╯
             ▼
    ╭──────────────────╮    fail
    │ encode for sink  │ ─────────▶ deny + log
    ╰────────┬─────────╯
             ▼
    sql · shell · html · path

Architecture: icons mark node types, the heavy line is the request path:

    ╭────────────╮      ╭────────────╮      ╭─────────────╮
    │ 󰖟 nginx    │ ━━━▶ │ 󰒋 api      │ ━━━▶ │ 󰆼 postgres  │
    ╰────────────╯      ╰─────┬──────╯      ╰─────────────╯
                              ┆ async
                              ▼
                        ╭────────────╮
                        │ 󰚇 kafka    │
                        ╰────────────╯

Sequence: one lifeline per party, each label on its arrow:

     client         api             pg
       │             │              │
       ├─ POST /pay ▶│              │
       │             ├─ BEGIN ─────▶│
       │             ├─ UPDATE ────▶│
       │             │◀─ ok ────────┤
       │◀─ 200 ──────┤              │

A failure, with the detail moved to a callout:

    󰀄 client ━━▶ 󰖟 nginx ━━▶ 󰒋 app ━━✗━━▶ 󰆼 redis
                                    ¹

    ¹ timeout after 5s: maxclients=10, the pool holds 12

States: transition labels sit in the gutter above their arrow:

    ╭───────╮  submit  ╭──────╮   fill   ╭────────╮
    │ draft │ ───────▶ │ open │ ───────▶ │ filled │
    ╰───────╯          ╰──┬───╯          ╰────────╯
                          │ cancel
                          ▼
                     ╭──────────╮
                     │ canceled │
                     ╰──────────╯

A change, before │ after:

    before                      │  after
    ────────────────────────────┼────────────────────────────
    api ─▶ pg on every request  │  api ─▶ cache ─▶ pg on miss
    p99 140 ms                  │  p99 12 ms

Quantities, numbers right-aligned in their own column:

    p99 latency
    before   ████████████████████████████   140 ms
    after    ██▌                             12 ms

Work in flight:

    ███░░ 3/5
    ✓ 1  schema migration
    ✓ 2  tests red
    ◐ 3  backfill script        ← now
    ○ 4  deploy to staging
    ⚠ 5  prod deploy            needs your go-ahead

Files touched:

    src/
    ├── auth/
    │   ├── session.ts      ~  token refresh moved here
    │   └── legacy.ts       -  deleted
    └── api/login.ts        +  magic-link endpoint

## Prose

Where words are needed, write spoken sentences: short, varied in length, active, concrete. A
list holds content that is a list, without bolded lead-ins. "I don't know" and "that won't
work" are complete answers; drop the hedges. These tells of generated prose are out, even when
they read well:

- Corrective negation ("it's not about speed, it's about correctness") and stacked "no / not /
  never" clauses. Say what it is.
- Constructed shapes: contrasting pairs ("cheap to write, expensive to maintain"), triples
  padded to three, two sentences on one skeleton, clipped fragments for rhythm ("The build
  failed. The test hung.").
- Framing beats: matched opening and closing lines, a landing last sentence, a summary of what
  you just said. The last sentence carries information or goes.
- Stacked nouns ("user data migration pipeline config") and nominalizations ("make a
  determination"). Use the verb.
- Intensifiers (really, truly, actually, quite), corporate verbs (leverage, surface, unlock,
  drive, deliver), performed enthusiasm ("great question", exclamation points).

## Break the rules when

- The action is destructive or irreversible: confirm first, then act.
- Q asked for an explanation or a tradeoff: run longer, each idea still gets its picture first.
- The ask is ambiguous in a way that changes what gets built: ask one question on line 1, stop.
- The format would make the answer wrong. Usefulness wins over shape.

Adapted from [ayghri/i-have-adhd](https://github.com/ayghri/i-have-adhd) (MIT).
