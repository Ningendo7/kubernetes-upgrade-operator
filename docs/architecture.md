# Architecture

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

- **`pkg/provider.Adapter`** — the interface implemented per provider (`kubeadm`, `generic`, `awseks`, `awsasg`, `linodelke`), dispatched by the `NodeGroupUpgrade` controller based on the group's classified `Provider`. The `InPlace` path (kubeadm and generic) executes host-level upgrades via a privileged per-node Job that `nsenter`s into the host's PID 1 namespaces — the controller has no SSH access to nodes, and a real `chroot` isn't enough to restart the host's `kubelet` via `systemctl`. See [SECURITY.md](../SECURITY.md) for the full hardening rationale behind that Job.

## Discovery: how the CR stays small

On every reconcile, `pkg/upgrade.DiscoverGroups` lists `Node`s and classifies each one:

1. An explicit `upgrade.k8s-upgrade-operator/provider-override` annotation wins outright, for cases the heuristics below get wrong.
2. Otherwise, `providerID` prefix + well-known labels decide it: `aws:///...` + `eks.amazonaws.com/nodegroup` → EKS-managed; bare `aws:///...` → self-managed ASG (flagged low-confidence — see below); `linode://...` + `lke.linode.com/pool-id` → LKE; empty `providerID` → Kubeadm (the on-prem/bare-metal default); anything else → Generic.
3. Nodes are grouped by `(role, provider, group-identity)` — control-plane nodes always form one `control-plane` group; workers group by their provider's pool identity, falling back to a single `workers` group.
4. Each group's `NodeGroupUpgrade` child is reconciled via Server-Side Apply with a dedicated field manager, so the controller's computed defaults stay in sync with cluster reality without clobbering fields a human has hand-edited on the child directly.

Some classifications are inherently ambiguous — e.g. a bare `aws:///` `providerID` with no EKS label could be a real self-managed ASG, or just a plain EC2 instance manually `kubeadm join`ed with the AWS cloud-provider integration enabled. Groups discovered this way are marked `heuristic: true` in `status.discoveredGroups` **and fail closed**: the child `NodeGroupUpgrade` is created `paused: true` with an explanatory condition, and stays that way until a human either fixes the classification (the override annotation) or explicitly confirms intent via `spec.groupOverrides[].strategy` for that specific group. An unrelated override field (e.g. `batchSize`) does not count as confirmation — it doesn't mean anyone actually looked at the provider guess.

## Safety mechanics

- Control-plane group is hard-pinned to `batchSize=1`, never user-configurable, and strategy is hard-pinned to `InPlace` regardless of any override (replacing a control-plane node risks etcd membership/quorum in ways this operator doesn't manage).
- Before moving to the next control-plane node, three independent checks must all pass: a cheap proxy (a majority of control-plane `Node`s report `Ready`); each control-plane node's *own* apiserver instance queried directly by IP (bypassing any load balancer) at its `/healthz/etcd` endpoint, requiring a majority to report etcd healthy (the apiserver's own etcd client, using its `apiserver-etcd-client` cert); and a real `etcdctl` check against each control-plane node's *own* local etcd member independently (never a single node's `--cluster` cross-discovery, which would make the whole determination only as reliable as whichever one node answered it), using a completely different `healthcheck-client` cert and code path, run via a narrowly-scoped privileged Job (see [SECURITY.md](../SECURITY.md)) and requiring a majority to report healthy. The second and third checks are deliberately kept as independent, both-required signals rather than one replacing the other — different certs and client implementations talking to etcd can in principle disagree, and requiring both closes that gap at negligible extra cost. Validated end-to-end against a real 3-node control-plane cluster.
- Draining is PDB-aware via the real Kubernetes eviction API; every pod DrainNode considers gets an explicit state (`Evicting`/`Blocked`/`Skipped`) rather than a bare count, so there's nothing for a caller to infer. A drain blocked by a PodDisruptionBudget pauses and retries rather than force-evicting, unless `drain.force` is explicitly set.
- A `coordination.k8s.io` Lease, held and continuously renewed for the entire duration of an active upgrade (not just acquired once), prevents two `KubernetesUpgrade`s from running concurrently cluster-wide. A finalizer ensures the lease is released even if a `KubernetesUpgrade` is deleted mid-upgrade, rather than leaking it for up to the staleness window.
- A `Generic`-provider group defaults to `InPlace`; overriding it to `Replace` requires an explicit `groupOverrides[].acknowledgeReplaceRisk: true` and is surfaced via a `GenericReplaceRisk` status condition either way (rejected-and-why, or acknowledged-and-active) — `Replace` for an unrecognized provider means deleting the `Node` object with no operator-verified mechanism confirming anything recreates it.
- Failures set `Phase=Failed`/`Paused` and stop — there is no automatic rollback of already-upgraded nodes.
- Each discovered group's per-pass target is computed from that group's own actual current version (`upgrade.GroupCurrentVersion`), not the cluster-wide step plan's hop target — a group that started behind the rest of the fleet (a previous upgrade paused partway through, or a group scope excluded last time) gets its own safe one-minor waypoint instead of being asked to skip a version, and a group that already finished early is never asked to move backward to an earlier hop (`upgrade.NextGroupTarget`). One real, known limitation from this: `spec.safety.allowDowngrade` does not correctly handle a fleet at mixed versions — because a group is never moved backward, an authorized downgrade is currently a safe no-op (nothing regresses) rather than actually downgrading. Narrow, opt-in-only edge case; not yet exercised by any test.
