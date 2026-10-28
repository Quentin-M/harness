# Skills

One copy of every skill. Never install into `~/.claude`, `~/.agents`, `~/.codex` or `~/.omp`.

- **Q's skills and vendored third-party skills** live in `tools/harness/skills/<name>/`. Every
  harness links them from there. Bare `omp` reads them through `skills.customDirectories`.
- **Project-only skills** live in that repo's `.agents/skills/<name>/`. If a tool needs
  `.claude/skills`, make it a symlink to `.agents/skills`. Never copy.
- **Installers** (`npx skills add`, `omp skill install`, codex `skill-installer`) default to the
  home directories. Run them into a scratch dir, or fetch the upstream repo, then copy the skill
  folder into `tools/harness/skills/` and delete the scratch. Third-party skills record their
  upstream and commit (see `skills/PSTACK.md`).
- **Before adding a skill** check the name does not already exist in `tools/harness/skills/` or the
  project's `.agents/skills/`. A second copy under the same name is a bug, not a variant.
- **OpenRig** seeds `openrig-skills`, `rigs` and `refocusing` into `~/.claude/skills` and
  `~/.agents/skills` itself. That is an upstream limitation; leave them there.
