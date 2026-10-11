---
name: reverse-engineer
description: Reverse engineer shipped artifacts with the REA CLI - native binaries, macOS apps, Electron/ASAR and JavaScript bundles, .NET assemblies, APKs, firmware, web pages. Use when a claim depends on a binary or package rather than available source. Skip for ordinary source-repo analysis.
---

# Reverse engineer with REA

[REA](https://github.com/morluto/rea) via its CLI. No MCP server: every call is a separate
process, so save Evidence JSON to files and feed it to later calls.

## Run it

`rea` below means `npx -y rea-agents`, or `rea` when `command -v rea` finds one.

```sh
rea --llms                     # full command table; read this once per session
rea <command> --help           # arguments and options
rea <command> --schema         # JSON input schema for commands taking <inputJson>
rea doctor --json              # which engines (Ghidra, Hopper, IDA, JADX...) are ready
```

Always pass `--json` and redirect large results to a file under `/tmp/rea/<target>/`. Read them
with `jq`, or cap stdout with `--token-limit 8000`.

## Route the target

| Target | First command | Needs |
|---|---|---|
| ASAR / extracted Electron or JS tree | `analyze-javascript-application <path>` | nothing |
| ZIP / APK / IPA / DMG / MSIX inventory | `inspect-artifact <path>` | nothing |
| macOS Mach-O / `.app` metadata | `inspect-macho`, `inspect-signature`, `inspect-plist` | nothing |
| .NET PE/CLI assembly | `inspect-managed-artifact`, `inspect-managed-members` | nothing |
| Android APK code | `inspect-android-package`, then `search-android-classes`, `inspect-android-method` | JADX + JDK |
| Native code (functions, pseudocode) | `analyze`, `search`, `function`, `decompile`, `xrefs`, `trace` | Ghidra, Hopper or IDA |
| Firmware | `inspect-firmware-regions`, `extract-firmware` | Binwalk/Unblob, Linux |
| Open browser page | `list-browser-targets`, `inspect-web-page` | Chrome with CDP |
| Running process | `capture-process` | runs the target |

Native deep analysis picks the single available provider; pick one with `--provider ghidra`.
Reuse work across calls with `--snapshot /tmp/rea/<target>/snapshot.json`.

JavaScript Evidence can reach hundreds of MB. Save it, then read slices:

```sh
rea analyze-javascript-application /abs/app.asar --json > ev.json
jq 'keys' ev.json                                  # shape first, then narrow paths
jq -c '{source:{kind:"inline",evidence:.},view:{kind:"summary"}}' ev.json > view.json
rea inspect-analysis-view view.json               # rea >= 6 only; check --llms
```

Swap the view for `{"kind":"page","collection":"modules","offset":0,"limit":32}` to page modules,
then `trace-application-feature` once you have a seed.

## Rules

- **Ask which app** when Q says "my app" without a path. Never pick an example target.
- **Static first.** Run commands that execute the target (`capture-*`, `observe-*`) only when
  Q asked for runtime behavior.
- **A missing engine is a blocker.** Report what `doctor` says and keep going with the
  engine-free commands. Do not install Ghidra, IDA or a JDK unasked.
- **Separate observed, inferred, unknown.** Cite `evidence_id`s. Never claim static analysis saw
  execution. Keep bounded searches bounded ("no match in the first 500 strings").
- For a broad question, write the sub-questions first, answer each from evidence, and finish by
  marking each one answered, partial or open.
