# Agent notes for yggdrasil

Yggdrasil is an Envoy xDS control plane: it watches Ingresses on several
Kubernetes clusters and pushes listeners and clusters to the Envoys on the
ingress nodes. One bad build affects every vhost at once, and Envoy rejects a
whole CDS or LDS update if a single resource in it is invalid. Treat any change
to generated config or to dependencies (`go-control-plane`, gRPC, client-go,
Go itself) as something to prove, not to assume.

## Repo facts that are easy to get wrong

- The integration branch is `staging`. `master` is the old upstream mirror but
  is still GitHub's default branch, so PRs target it unless told otherwise.
- `.gitignore` has the bare pattern `envoy`, which also matches `pkg/envoy/`.
  New files there need `git add -f`.
- `config/`, `envoy/`, `ca/`, `bin/` and `testing/` are git-ignored local test
  material. `config/` holds cluster tokens and TLS private keys: never print
  them, copy them into tracked paths, or commit them.
- Root-level `config_dump*` and `test-*.yaml` are the owner's scratch files.

## Proving a change is safe: local A/B run

The question to answer is always the same: **does the candidate make Envoy
behave differently from the baseline in any way the change did not intend?**
Unit tests cannot answer it, because they do not exercise real Ingress data or
a real Envoy.

The method that works is to run the baseline and the candidate side by side
against the same live cluster, give each its own local Envoy, and compare what
the two Envoys end up with. How you wire that up is your call; adapt it to the
change under test. What matters:

- **Same inputs.** Same config file, same flags, same Go toolchain for both
  builds, both running at the same moment. Ingresses change constantly, so
  runs taken at different times differ for reasons unrelated to the code.
- **Compare at Envoy, not at yggdrasil.** Envoy's admin `config_dump` is the
  config Envoy actually accepted, in a stable format, with private keys
  redacted. Yggdrasil's own `/configdump` (flag `--config-dump`) changes shape
  between library versions and contains private keys in clear when
  `syncSecrets` is on; keep it out of the repo and out of your output.
- **Normalise before diffing.** Yggdrasil merges Ingresses in random order,
  so list order differs between two runs of the same binary.
- **Check acceptance, not only content.** Envoy's `update_rejected` counters
  for CDS and LDS must stay at zero, and resource counts must match.
- **Exercise the behaviour, not just the config.** Send real requests through
  both Envoys and, for a TLS or timeout change, test that property directly.
  A config diff shows intent; a handshake or a request shows effect.

A run passes when every difference between the two sides is one the change
set out to make, and you can name each of them.

### What is available

- `config/` and `envoy/` contain a ready-made yggdrasil config and Envoy
  bootstrap for dev and for prod. Read them to see which clusters and ingress
  classes each one covers; they do not cover every instance that runs in
  production. If you need a variant, edit a copy outside the repo.
- The shell history and the Outline page "Yggdrasil - Multi-cluster with
  Kubernetes" (section "Contributing") show how the owner runs yggdrasil and
  Envoy locally, and how to recreate the configs if they are missing.

### Constraints

- Start with dev. Run against prod only when the user asks.
- A local Envoy pointed at prod sends active health checks to the real prod
  ingress controllers for every host that has a health-check annotation. That
  is real traffic: keep prod runs short and avoid running several Envoys
  against prod at once.
- Nothing in this procedure should write to a cluster or to the repo. Keep
  binaries, config copies and dumps in a scratch directory and remove them,
  and every process you started, when done.
- Dev has almost no `yggdrasil.uswitch.com/*` annotations; prod has thousands.
  A clean dev run says little about annotation handling. State what your run
  did and did not cover.

### Traps met so far

- Startup blocks forever, with no error, if an informer cannot list (expired
  token, missing RBAC, missing CRD).
- Requests whose Host header carries a non-standard port match no vhost and
  return 404 on both sides, which looks like a pass.
- macOS's built-in `openssl` is LibreSSL and lacks some `s_client` options;
  a container with OpenSSL 3 is more reliable for TLS tests.
- The local Envoy image may not be the exact version the ingress nodes run.
  Check, and report the gap.

When you hit a new trap, add it here.
