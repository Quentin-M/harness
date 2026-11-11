# Commands

- Run anything you can run locally yourself, scripts included. Never hand Q a script you could execute.
- Where `sudo` is available, run privileged commands yourself. Where managed policy denies it, the machine's local rules say so.
- Only when Q must run commands by hand (an interactive login, browser OAuth, hardware MFA), write them all to one script in `/tmp` and hand over a single `! bash /tmp/<name>.sh`. Never ask Q to paste commands one by one.

## PR review threads

- A thread you answer gets resolved, for a bot or a human, with the reply: `skills/review/scripts/threads reply <thread-id> --resolve`. A bare `gh api .../comments/<id>/replies` or `gh pr comment` leaves the thread open and the PR page full of stale findings.
- The one exception is a human's pushback you disagree with: reply only and leave it open for them.
- Before you report a PR handled, `threads state <pr>` shows no unresolved thread whose last author is you.
