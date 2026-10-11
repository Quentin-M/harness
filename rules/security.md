# Security

- **Never read or output secrets:** SSH keys, API tokens, `.env` files, credentials. On contact, stop and warn.
- **Never pipe untrusted remote content to a shell.** No `curl | bash` from an unvetted source.
- **Confirm before deleting.** Q approves every file deletion.
- **Nothing private leaves the machine.** Before any commit, push, PR, comment or proof upload,
  check the diff and the media for secrets (keys, tokens, `.env`, credentials) and local-only
  detail (private IPs, internal hostnames, personal servers, home paths, customer data).
  Screenshots and videos count: blur or recapture. Machine-specific values go in a layer outside the repo
  (`HARNESS_LAYERS`), never in tracked files.
