# kubernetes-upgrade-operator

A production-focused Kubernetes operator for orchestrating safe, resumable cluster upgrades — across on-prem/bare-metal kubeadm clusters, AWS (EKS-managed node groups and self-managed Auto Scaling Groups), Linode LKE, and any other infrastructure via a generic fallback. One CR, `spec.targetVersion`, is usually all a user needs to provide: the operator discovers the rest from live cluster state.

## Goals

- **Safety first.** Control-plane nodes are upgraded one at a time with a health gate before proceeding; worker drains are PDB-aware and pause (rather than force-evict) when blocked; a stuck or failed upgrade halts and waits for a human, it never auto-rolls-back.
- **Small, declarative CRs.** You shouldn't have to hand-enumerate every node pool. The operator inspects `Node` objects (`providerID`, well-known labels) to classify nodes into logical groups, infer their provider, and pick a sensible default upgrade strategy — with explicit overrides available when the defaults are wrong for your environment.
- **One operator, two upgrade mechanisms.** `InPlace` (run `kubeadm upgrade` / restart kubelet directly on the host — for bare-metal nodes that can't just be replaced) and `Replace` (cordon/drain/delete, let cloud infrastructure recreate — for managed node pools and immutable instances), dispatched per node group.
- **Idempotent and resumable.** Progress lives in `status`, not in memory — a controller restart or a requeued reconcile picks up exactly where it left off.
- **No hidden multi-minor jumps.** Kubernetes only supports upgrading one minor version at a time. A request to go from, say, `v1.27` to `v1.30` is automatically decomposed into three sequential single-minor hops, applied to the whole fleet one hop at a time (never some nodes on `v1.30` while others sit on `v1.27`).

## Non-goals

- Fully automated rollback of a failed or partial upgrade.
- Replacing your cluster's broader lifecycle tooling (backups, cert rotation, etc.).
- "Best-effort" upgrades that race ahead without health checks — this operator is deliberately conservative.

## Architecture

Two CRDs, two controllers, one provider-adapter interface: a top-level `KubernetesUpgrade` state machine discovers and drives per-group `NodeGroupUpgrade` children, each dispatched to a `pkg/provider.Adapter` implementation based on its discovered provider.

See **[docs/architecture.md](docs/architecture.md)** for the full state machines, the discovery mechanism that keeps the CR small, and the safety mechanics (control-plane sequencing, the etcd health check, drain policy, mutual exclusion). See **[SECURITY.md](SECURITY.md)** for the reasoning behind the executor Job — this operator's single highest-risk operation — including alternatives considered and why they weren't chosen.

## Provider support

| Provider | Strategy default | Status |
|---|---|---|
| Kubeadm (on-prem / bare-metal) | `InPlace` | Implemented |
| Generic (unrecognized infrastructure) | `InPlace` | Implemented |
| AWS EKS-managed node group | `Replace` (not overridable) | Interface-conformant, not yet implemented |
| AWS self-managed Auto Scaling Group | `Replace` (overridable to `InPlace`) | Interface-conformant, not yet implemented |
| Linode LKE node pool | `Replace` (not overridable) | Interface-conformant, not yet implemented |

## Development

Standard kubebuilder v4 workflow:

```sh
make manifests generate   # regenerate CRD YAML + deepcopy after editing api/v1alpha1
make test                 # fmt, vet, manifests, generate, then unit + envtest suites
make lint                 # golangci-lint, matches CI
make run                  # run the manager against your current kubeconfig
```

## Status

This project is under active development. Current progress:

- [x] `KubernetesUpgrade` and `NodeGroupUpgrade` API types
- [x] `pkg/k8sutil` — cordon/uncordon, PDB-aware drain (explicit per-pod state), node readiness/version checks, control-plane health (Node-Ready proxy + real per-node etcd `/healthz` check + a real `etcdctl` quorum check via a dedicated privileged Job, validated against a real 3-node control-plane cluster)
- [x] `pkg/upgrade` — node discovery/classification (fail-closed on ambiguity), multi-minor step-plan computation, batching, strategy resolution
- [x] `pkg/provider` — adapter interface and registry
- [x] `pkg/provider/kubeadm` and `pkg/provider/generic` real implementations
- [x] `pkg/provider/{awseks,awsasg,linodelke}` stub implementations
- [x] `KubernetesUpgrade` and `NodeGroupUpgrade` controllers (full state machines, wired into `cmd/main.go`)
- [x] Validating webhook (no-downgrade, hop-count sanity ceiling)
- [x] envtest integration coverage for both controllers and the webhook, including a full multi-hop upgrade cycle
- [x] Kubeadm executor image source (`images/kubeadm-executor/`) — hardened per [SECURITY.md](SECURITY.md)
- [ ] A pinned, version-checksummed table for the kubeadm binary fetch (currently trusts the checksum served alongside the binary itself — see [SECURITY.md](SECURITY.md))
- [x] The executor image built and published (`docker.io/ningendo7/k8s-upgrade-operator-executor`) — currently a mutable `:test` tag, not yet pinned by digest (see below)
- [ ] `ExecutorImage`/the manager image pinned to a digest rather than a mutable tag (currently `:test`, acceptable for the active testing phase this project is still in, not for a real deployment)
- [x] End-to-end testing against a real multi-control-plane kubeadm cluster on Linode VMs (InPlace worker upgrades, control-plane upgrades with real etcd quorum via both the apiserver-proxy and real-`etcdctl` checks, multi-minor-hop upgrades, patch-only upgrades) — see [docs/architecture.md](docs/architecture.md) for bugs this surfaced and fixed
- [ ] `spec.safety.allowDowngrade` does not correctly handle a fleet where node groups sit at mixed versions: the per-group step logic (`upgrade.NextGroupTarget`) never moves a group backward, which closes a real regression risk during upgrades but also makes an authorized downgrade a silent no-op for any group already at or below the requested version. Safe (nothing moves the wrong direction), not yet correct. Narrow, opt-in-only edge case, not exercised by any test.
- [ ] A node that joins a group mid-upgrade (while its `NodeGroupUpgrade` is already past `Pending`) is not picked up until the *next* `KubernetesUpgrade` starts fresh — `NodeProgress` is only (re)seeded from `spec.Nodes` in `reconcilePending`. Not dangerous (the node is simply deferred a cycle, never mishandled), but not immediate either.
- [ ] Observability: custom Prometheus metrics beyond controller-runtime's generic reconcile metrics; Grafana dashboard
- [ ] `make lint` clean run (deferred to CI — see `.github/workflows/lint.yml`)
