# workload-identity-sidecar

Sidecar container that exchanges a SPIFFE SVID for short-lived cloud credentials. The `ghcr.io/cujarrett/workload-identity-sidecar` image is built by CI in this repo and injected into [XApi](https://github.com/cujarrett/homelab/tree/main/platform/api) pods by the XApi Crossplane composition when the pod declares cloud resource bindings.

## Sidecar

`sidecar/Dockerfile` builds `ghcr.io/cujarrett/workload-identity-sidecar`. It installs `spire-agent` (ARM64 binary) and runs `sidecar/entrypoint.sh`.

The sidecar waits for the SPIFFE Workload API socket at `/var/run/secrets/spiffe.io/api.sock` (provided by the SPIFFE CSI driver), then runs up to two independent loops depending on which env vars are set:

- **AWS** (`AWS_BINDINGS` set): fetches a JWT-SVID per cycle, presents it to AWS STS via `AssumeRoleWithWebIdentity` (a plain unsigned HTTPS POST) per binding, and writes named profile sections to a temp file atomically renamed to `CREDS_FILE`. Refreshes every 50 minutes; on a failed exchange it keeps the previous credentials file and retries in 30 seconds.
- **Entra** (`ENTRA_FEDERATED_TOKEN_FILE` set): fetches a JWT-SVID scoped to `api://AzureADTokenExchange` and writes it straight to that file, refreshed every `ENTRA_REFRESH_INTERVAL` seconds (default 240). No exchange call here - the app's Azure SDK (`WorkloadIdentityCredential`) does the `client_assertion` swap itself, reading `AZURE_CLIENT_ID`/`AZURE_TENANT_ID`/`AZURE_FEDERATED_TOKEN_FILE`, all injected by the XApi composition.

An Api can enable either, both, or neither.

Renovate opens a PR automatically when SPIRE cuts a release, tracking `SPIRE_VERSION` in `sidecar/Dockerfile`. The PR carries a reminder to check it against the cluster's running version before merging.

## Rules

- Never run `git commit`, `git push`, or any git command that writes to or modifies repository history or remotes.

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
