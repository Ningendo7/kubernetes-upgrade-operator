# Security

## Reporting a vulnerability

This is a personal/learning project without a dedicated security contact. If you find a vulnerability, please open a GitHub issue (or, for something you'd rather not disclose publicly, reach out to the maintainer directly via GitHub) rather than a public pull request.

## The executor Job: this operator's highest-risk operation

Everything else in this operator is bounded by ordinary Kubernetes RBAC: list/watch/patch calls against the API server, scoped by a `ClusterRole`. One operation is fundamentally different in kind, not just degree: the `kubeadm`/`generic` provider adapters' `InPlace` strategy runs a privileged, node-pinned Job that uses `nsenter` to re-enter a real host's namespaces and mutate its OS state directly (install a new `kubeadm`/`kubelet`, restart `kubelet`). That's intentional container-escape-as-a-feature — there's no way to install a new kubelet binary on a bare-metal/on-prem node from inside a Kubernetes cluster without it. It deserves to be treated with more scrutiny than everything else in this codebase, and this document exists so that scrutiny is written down rather than scattered across design discussions.

### What this looks like: a node-pinned, short-lived, privileged Job

- **Node-pinned**: `spec.nodeName` is set directly on the pod, bypassing the scheduler. This is deliberate, not incidental — it's also what lets the Job still land on a node that's already been cordoned by the `Draining` phase (cordoning only blocks the *scheduler*).
- **Short-lived**: the Job exists only for the duration of one upgrade action. `TTLSecondsAfterFinished` cleans it up after completion; `ActiveDeadlineSeconds` bounds how long it's allowed to run *before* completion, so a hung attempt can't linger indefinitely.
- **Privileged**: narrowed to exactly the `SYS_ADMIN` (confirmed necessary for the `setns()` calls `nsenter` makes) and `SYS_CHROOT` (included defensively, though it may not be strictly load-bearing for a `setns`-based approach as opposed to a `chroot`-based one — worth re-verifying once this runs for real) capabilities, rather than blanket `privileged: true`. `AllowPrivilegeEscalation: false` and `ReadOnlyRootFilesystem: true` are also set.

### Blast-radius containment

- **A dedicated namespace** (`config/executor/`), separate from the manager's own. Only this namespace runs at the Kubernetes **`privileged` Pod Security Standard**; everything else this operator touches, including the manager's own namespace, runs at `restricted`. This is deliberate isolation, not an oversight — an earlier draft of this design ran the executor in the *manager's* namespace, which would have meant either weakening the whole deployment's PSA level or having the executor Jobs get rejected by admission control outright. Keeping the manager's own footprint minimal and auditable, while being explicit about exactly where the real risk concentrates, is the point.
- **A dedicated ServiceAccount** with **zero RBAC grants** — no `RoleBinding` exists for it anywhere, because the executor container never talks to the Kubernetes API at all; it only runs local host commands. `automountServiceAccountToken: false` is set on both the `ServiceAccount` and the pod template as two independent enforcement points, so there's no token to abuse even if the image were compromised.
- **No host filesystem mount.** An earlier draft mounted the host's entire root filesystem into the container via `hostPath` — but the script never referenced it. `nsenter --mount` switches the *process itself* into the host's real mount namespace; paths resolve against the real host filesystem directly once that happens, with no bind-mount needed at all. Removing it was a strict security improvement with zero functionality lost: a full read-write view of the host filesystem, sitting unused, is exactly the kind of thing that should not exist.
- **Minimal image content.** The container image itself only needs `nsenter` (from `util-linux`) and a shell — nothing else. Every other command the script runs (`curl`, `apt-get`/`dnf`, `kubeadm`, `systemctl`) executes *after* `nsenter` switches namespaces, meaning it resolves against the **host's** own binaries, not anything bundled in the image. Less software inside a container that runs with `SYS_ADMIN` is less attack surface, full stop.
- **The script logic is baked into the image** (`images/kubeadm-executor/`), not injected at Job-creation time by the operator's Go code. This matters for digest pinning to mean anything: if the actual behavior could be changed independently of the image (e.g. by editing a string constant in the controller and redeploying), pinning the image's digest wouldn't actually capture "what runs" — it'd just be pinning an inert shell interpreter.

### Supply-chain integrity of the fetched binaries

The script downloads `kubeadm` for the target version from the official Kubernetes release CDN (configurable via `KUBEADM_RELEASE_BASE_URL`, so air-gapped deployments can point it at an internal mirror instead) and verifies its SHA-256 checksum before installing it. This is the load-bearing integrity control for that step — **not network-level restriction**. A Kubernetes `NetworkPolicy` on the executor namespace cannot constrain this download: it happens after `nsenter --net` switches the process into the host's own network namespace, which structurally exits the pod network layer `NetworkPolicy` enforces at. Any egress restriction on that specific traffic would need to happen at the host/infrastructure level (node firewall rules, DNS/egress controls) — outside what this operator can enforce from inside the cluster. If this were ever extended to run a network-touching step *before* `nsenter`, that step would be the one place a `NetworkPolicy` could actually bind.

### Known follow-up: image pinning by digest

`ExecutorImage`'s default is currently a mutable tag (`:latest`), not a content-addressed digest (`@sha256:...`). Pinning by digest is the correct production practice — it means whoever controls the image tag can't silently swap in different content — but there's no real digest to pin to until the image is actually built and published somewhere. This is a deployment-configuration follow-up once that happens, not a code change.

### Alternatives considered, and why this one was kept

| Approach | Lowers the ceiling of what a compromise achieves? | Real tradeoff |
|---|---|---|
| **Privileged Job + `nsenter`** (this design) | No — `nsenter --mount` with `SYS_ADMIN` is full host control by definition; no capability-narrowing trick changes that ceiling | Short-lived and node-pinned, so the *window* of exposure is minimized even though the *ceiling* isn't |
| Persistent DaemonSet-based agent | No — needs the same capability for the same job | Worse on one axis: an always-running privileged process on every node has a larger, longer-lived attack surface than a Job that exists only for one action |
| SSH-based external orchestration | No — whoever holds the SSH key has root everywhere; same ceiling, different vector | Trades "compromised image / Job-creation RBAC" for "leaked SSH secret," plus real key-rotation burden, plus requires pre-provisioned SSH access this operator is explicitly designed not to assume |
| Never mutate in place — always re-image via out-of-band bare-metal provisioning (PXE/Tinkerbell/MAAS-style) | Yes — genuinely eliminates in-cluster privileged mutation | Not a hardening tweak on this design, a different and much larger system; it would also remove the reason `InPlace` exists at all, since the audience most likely to need it (on-prem kubeadm clusters) is also the audience least likely to already have that provisioning infrastructure |

The honest conclusion: this operation is, and will remain, the single highest-risk thing in this codebase, by design — the job itself (mutate a real host's OS with no pre-provisioned access) inherently requires it. The response to that isn't a different architecture, it's treating it with more rigor than everything else here, which is what this document is meant to hold accountable over time.
