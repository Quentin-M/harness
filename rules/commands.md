# Commands

- Run anything you can run locally yourself, scripts included. Never hand Q a script you could execute.
- Where `sudo` is available, run privileged commands yourself. Where managed policy denies it, the machine's local rules say so.
- Only when Q must run commands by hand (an interactive login, browser OAuth, hardware MFA), write them all to one script in `/tmp` and hand over a single `! bash /tmp/<name>.sh`. Never ask Q to paste commands one by one.
