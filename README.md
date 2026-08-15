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

Two CRDs, two controllers, one provider-adapter interface:

- **`KubernetesUpgrade`** (namespaced) — the resource a user creates. Holds `spec.targetVersion` plus optional escape hatches (`scope`, `defaults`, `groupOverrides`, `safety`). Drives a top-level state machine:

  ```
  Pending → Discovering → Prechecks → ControlPlaneUpgrade → WorkersUpgrade → Postchecks → Complete
                                                                                          ↘ Failed / Paused
  ```

  This loops once per single-minor-version hop in `status.stepPlan` when the target is more than one minor version ahead.

- **`NodeGroupUpgrade`** (namespaced, owned by a `KubernetesUpgrade`) — one per discovered node group; control-plane nodes always collapse into their own group regardless of provider. State machine:

  ```
  Pending → Draining → Upgrading → Verifying → Complete
                                              ↘ Failed / Paused
  ```

  batched by `maxUnavailable`/`batchSize`, never touching more nodes at once than the group's resolved concurrency limit.

- **`pkg/provider.Adapter`** — the interface implemented per provider (`kubeadm`, `generic`, `awseks`, `awsasg`, `linodelke`), dispatched by the `NodeGroupUpgrade` controller based on the group's classified `Provider`. The `InPlace` path (kubeadm and generic) executes host-level upgrades via a privileged per-node Job that `nsenter`s into the host's PID 1 namespaces — the controller has no SSH access to nodes, and a real `chroot` isn't enough to restart the host's `kubelet` via `systemctl`.

### Discovery: how the CR stays small

On every reconcile, `pkg/upgrade.DiscoverGroups` lists `Node`s and classifies each one:

1. An explicit `upgrade.k8s-upgrade-operator/provider-override` annotation wins outright, for cases the heuristics below get wrong.
2. Otherwise, `providerID` prefix + well-known labels decide it: `aws:///...` + `eks.amazonaws.com/nodegroup` → EKS-managed; bare `aws:///...` → self-managed ASG (flagged low-confidence — see below); `linode://...` + `lke.linode.com/pool-id` → LKE; empty `providerID` → Kubeadm (the on-prem/bare-metal default); anything else → Generic.
3. Nodes are grouped by `(role, provider, group-identity)` — control-plane nodes always form one `control-plane` group; workers group by their provider's pool identity, falling back to a single `workers` group.
4. Each group's `NodeGroupUpgrade` child is reconciled via Server-Side Apply with a dedicated field manager, so the controller's computed defaults stay in sync with cluster reality without clobbering fields a human has hand-edited on the child directly.

Some classifications are inherently ambiguous — e.g. a bare `aws:///` `providerID` with no EKS label could be a real self-managed ASG, or just a plain EC2 instance manually `kubeadm join`ed with the AWS cloud-provider integration enabled. Groups discovered this way are marked `heuristic: true` in `status.discoveredGroups`, so it's visible before the operator picks a (possibly wrong, possibly destructive) default strategy for them. Fix it with the override annotation.

### Safety mechanics

- Control-plane group is hard-pinned to `batchSize=1`, never user-configurable, and strategy is hard-pinned to `InPlace` regardless of any override (replacing a control-plane node risks etcd membership/quorum in ways this operator doesn't manage).
- Before moving to the next control-plane node, a proxy health check requires a majority of control-plane `Node`s to be `Ready` (real `etcdctl`-based quorum checking needs privileged access this operator doesn't have yet — see Roadmap).
- Draining is PDB-aware via the real Kubernetes eviction API; a drain blocked by a PodDisruptionBudget pauses and retries rather than force-evicting, unless `drain.force` is explicitly set.
- A `coordination.k8s.io` Lease prevents two `KubernetesUpgrade`s from running concurrently cluster-wide.
- Failures set `Phase=Failed`/`Paused` and stop — there is no automatic rollback of already-upgraded nodes.

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
- [x] `pkg/k8sutil` — cordon/uncordon, PDB-aware drain, node readiness/version checks, control-plane proxy health check
- [x] `pkg/upgrade` — node discovery/classification, multi-minor step-plan computation, batching, strategy resolution
- [x] `pkg/provider` — adapter interface and registry
- [ ] `pkg/provider/kubeadm` and `pkg/provider/generic` real implementations
- [ ] `pkg/provider/{awseks,awsasg,linodelke}` stub implementations
- [ ] `KubernetesUpgrade` and `NodeGroupUpgrade` controllers (state machines)
- [ ] Validating webhook
- [ ] End-to-end testing against a real kubeadm cluster
