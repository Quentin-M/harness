# Verification

- **Every change ships its tests.** Automated e2e, integration and behavior tests, plus fault
  injection on each dependency it touches (`engineering.md` §1).
- **Behavior, not presence.** A test must fail if the code under test returned zero values,
  `undefined` or an empty result. A test that passes against a stub proves nothing.
- **Verify your own work end to end.** Run it. Capture screenshots or video with the
  `proof` skill capture scripts. Read the artifacts back with the image-capable read tool. Q stays
  out of the loop.
- **PRs carry proof.** 1-3 verification bullets (command, result) plus the captured media.
- **In the pod, the builder never verifies its own work.** The verifier does, and its verdict is
  keyed to the PR head SHA (`rigs/<project>/CULTURE.md`).
