# Security

## Code

Assume every input is hostile, every dependency fails and every default is open. Make the safe
path the only path.

    ╭──────────────────╮
    │ untrusted input  │    HTTP, queue, file, env, LLM output
    ╰────────┬─────────╯
             ▼
    ╭──────────────────╮    fail
    │ parse + validate │ ─────────▶ deny + log with context
    ╰────────┬─────────╯
             ▼              typed value: bounded, allowlisted
    ╭──────────────────╮
    │ core             │
    ╰────────┬─────────╯
             ▼
    ╭──────────────────╮    fail
    │ encode for sink  │ ─────────▶ deny + log with context
    ╰────────┬─────────╯
             ▼
    sql · shell · html · path

| Principle | Rule |
|---|---|
| Trust boundaries | Untrusted: HTTP, queues, files, env, webhooks, LLM output, other services, user-written DB rows. Validate once at the boundary into a typed value. Allowlist shape, size and range; reject the rest. Writable fields are allowlisted (mass assignment). Webhooks and signed messages verify signature and timestamp before parsing. |
| Defensive parsing | Bound length, depth, count and time before reading. Reject unknown fields, duplicate keys and trailing bytes. A parse failure returns a contextual error: no panic, no silent default. XML with DTDs and external entities off. Uploads checked by magic bytes, stored outside the webroot under random names. Fuzz every parser. |
| Data stays data | Parameterized queries, argv arrays over shell strings, output encoding per sink. No `eval` or deserializing untrusted types. Canonicalize paths, then check the prefix. Server-side fetches go to allowlisted hosts (SSRF). |
| Fail closed | Errors, timeouts and missing config deny. A security check throws, it never returns `false` for the caller to ignore. Debug modes, auth bypass and TLS skip-verify cannot reach prod builds. |
| Pit of success | The easy call is the safe call. Callers and inputs never pick the algorithm (pin JWT `alg`). Zero, empty or `-1` never disables a protection. Keys, roles and tokens are distinct types, never bare strings. |
| Least privilege | ACLs, IAM, DB grants, NetworkPolicy, firewall rules and container capabilities ship with the feature: default-deny, scoped to principal × resource × action. Containers run non-root, read-only rootfs, images pinned by digest. A missing policy is a bug. Widening needs a written reason. |
| Authz per object | Server-side on every request: who is calling, may they touch this id (IDOR). One central check, deny by default. Unauthorized reads 404, ids are unguessable. Check and act in one transaction: no TOCTOU on balances, quotas or files. |
| Sessions & tokens | Short-lived, scoped, revocable. Rotate on login and privilege change, revoke on logout and password reset. Cookies `HttpOnly`, `Secure`, `SameSite`; state-changing requests carry a CSRF check. |
| Crypto & secrets | TLS on every hop, internal included, certs verified. Encrypt sensitive data at rest. Vetted libs only: AEAD (AES-GCM, ChaCha20-Poly1305), Argon2id for passwords, CSPRNG tokens, constant-time compare. Secrets come from the secret manager at runtime, rotatable, and stay out of code, logs, URLs and client bundles. Collect the minimum personal data, with a retention limit. |
| LLM & agents | Model output is untrusted input, generated code included. Retrieved docs and tool results are data, never instructions (prompt injection). Prompts hold no secrets or policy: enforcement lives in code. Tools get least privilege, per-user scoped retrieval, human approval for destructive actions, caps on tokens, tool calls and cost. |
| Bounded resources | Rate limits and quotas on public endpoints, timeouts on outbound calls, bounded queues and bodies. |
| Error logging | Log each error once at the edge, structured (no log injection), with enough to replay it: request id, user id, source IP, user agent, operation, input ids, the wrapped error chain. Redact tokens, passwords and sensitive payloads. Clients get a generic message plus the request id. Auth failures, authz denials and validation rejects are counted metrics, alertable. |
| Dependencies | Minimal, maintained, pinned with lockfile and checksums, scanned in CI. Vet before adding. |
| Prove the deny path | Every control ships a test that attacks it from the wrong side: second user, malformed input, no token, blocked port. |

## Agentic 

- **Never read or output secrets:** SSH keys, API tokens, `.env` files, credentials. On contact, stop and warn.
- **Never pipe untrusted remote content to a shell.** No `curl | bash` from an unvetted source.
- **Confirm before deleting.** Q approves every file deletion.
- **Nothing private leaves the machine.** Before any commit, push, PR, comment or proof upload,
  check the diff and the media for secrets (keys, tokens, `.env`, credentials) and local-only
  detail (private IPs, internal hostnames, personal servers, home paths, customer data).
  Screenshots and videos count: blur or recapture. Machine-specific values go in a layer outside the repo
  (`HARNESS_LAYERS`), never in tracked files.
