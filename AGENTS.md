# workload-identity-sidecar

A sidecar container that trades a pod's SPIFFE identity for short-lived cloud credentials. CI builds `ghcr.io/cujarrett/workload-identity-sidecar`. The homelab [Api](https://github.com/cujarrett/homelab/tree/main/platform/api) composition adds it to pods that need AWS, Entra or a secret.

## Sidecar

One Go binary, built from `sidecar/` by `sidecar/Dockerfile`. It talks to the SPIRE agent's Workload API socket with `go-spiffe`. The README has the full behaviour.

It waits for the socket at `/var/run/secrets/spiffe.io/api.sock`, then runs each loop whose env var is set:

- **AWS** (`AWS_BINDINGS`, `aws.go`): trades a JWT-SVID with STS per binding and writes `CREDS_FILE`.
- **Entra** (`ENTRA_FEDERATED_TOKEN_FILE`, `entra.go`): writes a fresh JWT-SVID for the app's Azure SDK to trade.
- **Token Secret** (`TOKEN_SECRET_NAME`, `tokensecret.go`): trades a JWT-SVID with Entra and patches the access token into a Secret.
- **Secrets** (`SECRET_BINDINGS`, `secrets.go`): fetches a ManagedSecret's value and writes one file per property.

The image stays on Alpine, because the Api composition's startup probe runs `sh` in it.

Go conventions:

- `just ci` runs lint, test and build. Run it before pushing.
- Standard library first. The AWS SDK and `go-spiffe` are the only dependencies.
- Each loop takes its SVID fetcher as a field, so tests run without SPIRE.

## Rules

- Never run `git commit`, `git push`, or any git command that writes to or modifies repository history or remotes.
- **Never add a `Co-Authored-By` trailer or a "Generated with Claude Code" line** to commit messages or PR descriptions, including in suggested commit messages. Commits are authored by the user alone.

### Pre-commit safety check

Before telling the user to commit, always run `/security-review`. It reviews the pending changes on the current branch for security issues. Once it confirms the changes are safe, offer the user a suggested commit message - do not run `git commit` yourself.

## Philosophy: Grug-Brained Development

> "Complexity very, very bad." - [grugbrain.dev](https://grugbrain.dev/)

- **Say no.** The best weapon against complexity is the word "no". No new feature, no new abstraction, until it earns its place.
- **Cheapest rung that works.** Before writing code go down the ladder and stop at the first rung that solves it - skip the feature, reuse code already here, standard library, native platform feature, a dependency already installed, one line, then build the minimum.
- **No abstraction until a pattern repeats three times.** Let cut points emerge naturally from the code; don't invent them up front.
- **80/20 solutions.** Ship 80% of the value with 20% of the code. Ugly but working beats elegant but over-engineered.
- **Chesterton's Fence.** Understand why code exists before removing it. If you don't see the use, go away and think.
- **Boring, obvious code wins.** Intermediate variables with good names beat clever one-liners. Easier to debug.
- **DRY is not a law.** A little copy-paste beats a complex abstraction built for two cases.
- **No FOLD** (Fear Of Looking Dumb). If something is too complex, say so. That's a signal to simplify, not a personal failing.
