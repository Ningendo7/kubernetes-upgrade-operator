/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package observability defines this operator's domain-specific Prometheus
// metrics - distinct from controller-runtime's own built-in metrics
// (reconcile counts, workqueue depth, REST client calls), which already
// cover generic controller health and need no duplication here.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// providerLabel is the label name shared by every metric below that is
// broken out per node-group provider (kubeadm, aws-eks-managed-nodegroup,
// aws-autoscaling-group, linode-lke, generic).
const providerLabel = "provider"

// All metrics here register into controller-runtime's own metrics.Registry,
// so they are exposed on the same /metrics endpoint the operator already
// serves - no separate server, port, or scrape config needed.
//
// Every outcome/result label below is a small, fixed set of values (never
// raw error strings): Prometheus labels are meant to stay low-cardinality,
// and unbounded values from arbitrary error text would make these both a
// cardinality risk and impossible to write a stable alert against.
var (
	// KubernetesUpgradePhaseInfo reflects each live KubernetesUpgrade's
	// CURRENT phase - always 1, one series per (namespace, name, phase)
	// with exactly one phase alive at a time for a given object. Answers
	// two different real questions depending on how it is queried:
	// "count by (phase) (...)" gives a fleet-wide phase breakdown for a
	// dashboard; "kubernetesupgrade_phase_info{phase=~\"Failed|Paused\"}"
	// is a direct alert target, since both states always require a human
	// by this operator's own design (no automatic rollback). The PREVIOUS
	// phase's series is deleted the moment a transition happens (see
	// pkg/observability.SetPhase), and the whole object's series is deleted on
	// completion or deletion (see DeleteKubernetesUpgrade) - otherwise a
	// short-lived, frequently-recreated object like this one would leave
	// permanently-stale series behind for every past run.
	KubernetesUpgradePhaseInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kuo_kubernetesupgrade_phase_info",
			Help: "Always 1; identifies a live KubernetesUpgrade's current phase, by namespace, name, and phase.",
		},
		[]string{"namespace", "name", "phase"},
	)

	// KubernetesUpgradePhaseStartTimestampSeconds is when the CURRENT
	// phase began, as a Unix timestamp - the same idiom kube-state-metrics
	// uses for kube_pod_start_time. This exists so "has this been stuck in
	// its current phase too long" is a plain alert-rule expression
	// (time() - kuo_kubernetesupgrade_phase_start_timestamp_seconds >
	// threshold) rather than duration-tracking logic this operator would
	// otherwise have to own itself. Deliberately no threshold baked in
	// here: how long is "too long" depends on fleet size and which phase
	// (WorkersUpgrade across hundreds of nodes legitimately runs far
	// longer than ControlPlaneUpgrade across three) - that is environment
	// tuning for the alert rule, not this operator's business.
	KubernetesUpgradePhaseStartTimestampSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kuo_kubernetesupgrade_phase_start_timestamp_seconds",
			Help: "Unix timestamp when a live KubernetesUpgrade entered its current phase, by namespace and name.",
		},
		[]string{"namespace", "name"},
	)

	// NodeGroupNodesByPhase is a live count of nodes in each phase within
	// a NodeGroupUpgrade - answers "how many nodes are left to upgrade in
	// this group right now" for progress dashboards, and "did any nodes
	// end up Failed" for a fleet-wide rollup. Cardinality scales with the
	// number of live (kubernetesupgrade, nodegroup, phase) combinations,
	// not with individual node count or reconcile volume - a large fleet
	// has more nodes per group, not more series. Series for a group are
	// deleted together when its NodeGroupUpgrade is deleted (see
	// DeleteNodeGroupUpgrade).
	NodeGroupNodesByPhase = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kuo_nodegroup_nodes_by_phase",
			Help: "Current count of nodes in each phase for a NodeGroupUpgrade, by namespace, kubernetesupgrade, nodegroup, provider, and phase.",
		},
		[]string{"namespace", "kubernetesupgrade", "nodegroup", providerLabel, "phase"},
	)

	// AdapterOperationTotal counts every provider.Adapter interface call
	// (Precheck, BeginBatch, PollBatch, Verify), by provider, operation,
	// and outcome. This is the one place instrumentation lives for ALL
	// adapters at once (see pkg/provider's InstrumentedAdapter decorator) -
	// a new provider implementation gets this metric for free by
	// registering, with zero adapter-specific instrumentation code.
	// Answers "is this provider's upgrade path actually working" - alert
	// on a sustained rate of outcome="error", not on any single failure
	// (a single Precheck "not ready yet" is normal, expected, retried
	// automatically every 15s).
	AdapterOperationTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kuo_adapter_operation_total",
			Help: "Total provider.Adapter interface calls, by provider, operation (Precheck, BeginBatch, PollBatch, Verify), and outcome (ok, not_ready, error).",
		},
		[]string{providerLabel, "operation", "outcome"},
	)

	// AdapterOperationDuration times each provider.Adapter interface call,
	// by provider and operation. Recorded by the same InstrumentedAdapter
	// decorator as AdapterOperationTotal.
	AdapterOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "kuo_adapter_operation_duration_seconds",
			Help:    "Time spent in a single provider.Adapter interface call, by provider and operation.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{providerLabel, "operation"},
	)

	// ExecutorJobTotal counts every privileged, node-pinned executor Job
	// this operator creates (see SECURITY.md), by provider, kind (upgrade
	// or etcd_healthcheck), and outcome. This is arguably the single most
	// important operational signal in this codebase: these Jobs are the
	// highest-risk, most failure-prone operation this operator performs -
	// real-cluster testing found and fixed half a dozen distinct ways
	// they could fail (capability gaps, AppArmor, missing dependencies,
	// ownership mismatches) before this metric even existed. Answers "are
	// the actual host-mutating/host-reading operations succeeding, and
	// for which kind" - alert on a sustained rate of outcome="failed",
	// same reasoning as AdapterOperationTotal.
	ExecutorJobTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kuo_executor_job_total",
			Help: "Total privileged executor Jobs created, by provider, kind (upgrade, etcd_healthcheck), and outcome (succeeded, failed).",
		},
		[]string{providerLabel, "kind", "outcome"},
	)

	// EtcdQuorumHealthy reflects the CURRENT result of each independent
	// etcd quorum check this operator performs - 1 healthy, 0 not - by
	// method (apiserver_proxy or etcdctl; see docs/architecture.md for why
	// both run independently rather than one replacing the other).
	// Deliberately a plain current-state gauge, not a counter: a transient
	// single-poll failure (e.g. the apiserver briefly restarting mid
	// upgrade, which real testing hit repeatedly and is entirely normal)
	// must never alert on its own - the alert rule pairs this with a
	// sustained-duration `for:` clause, which a raw event count cannot
	// express.
	EtcdQuorumHealthy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kuo_etcd_quorum_healthy",
			Help: "Whether the most recent etcd quorum check reported healthy (1) or not (0), by method (apiserver_proxy, etcdctl).",
		},
		[]string{"method"},
	)

	// UpgradeLeaseAcquiredTimestampSeconds is when the cluster-wide
	// mutual-exclusion Lease was last acquired, as a Unix timestamp - the
	// same start-time idiom as KubernetesUpgradePhaseStartTimestampSeconds,
	// for the same reason: "has the lease been held far longer than any
	// real upgrade should take" becomes a plain alert-rule expression
	// instead of duration-tracking logic here. A single, unlabeled gauge:
	// there is only ever one such Lease cluster-wide by design.
	UpgradeLeaseAcquiredTimestampSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "kuo_upgrade_lease_acquired_timestamp_seconds",
			Help: "Unix timestamp when the cluster-wide upgrade mutual-exclusion Lease was last acquired. Absent when no upgrade currently holds it.",
		},
	)

	// DrainBlockedTotal counts pod evictions blocked by a
	// PodDisruptionBudget during a drain, by namespace, nodegroup, and
	// provider. Deliberately NOT wired to any alert on its own - a blocked
	// eviction is normal, expected, self-healing behavior (this operator
	// pauses and retries rather than force-evicting, unless drain.force is
	// set). This exists for dashboard visibility and for debugging an
	// ALREADY-fired "stuck too long" alert, not as an alert signal itself.
	DrainBlockedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kuo_drain_blocked_total",
			Help: "Total pod evictions blocked by a PodDisruptionBudget during a drain, by namespace, nodegroup, and provider.",
		},
		[]string{"namespace", "nodegroup", providerLabel},
	)
)

// SetPhase records that the KubernetesUpgrade identified by namespace/name
// is now in phase. Clears whatever phase (if any) was previously recorded
// for it first, so exactly one phase series stays live per object -
// callers never need to track or pass the previous phase themselves. Call
// this ONLY at the point ku.Status.Phase is actually being assigned a new
// value, never unconditionally on every reconcile - see the doc comment on
// KubernetesUpgradePhaseStartTimestampSeconds for why.
func SetPhase(namespace, name, phase string) {
	KubernetesUpgradePhaseInfo.DeletePartialMatch(prometheus.Labels{
		"namespace": namespace,
		"name":      name,
	})
	KubernetesUpgradePhaseInfo.WithLabelValues(namespace, name, phase).Set(1)
	KubernetesUpgradePhaseStartTimestampSeconds.WithLabelValues(namespace, name).Set(float64(time.Now().Unix()))
}

// DeleteKubernetesUpgrade removes every series for the KubernetesUpgrade
// identified by namespace/name. Call this ONLY from the actual deletion/
// finalizer path, never on reaching Complete/Failed/Paused - a Failed
// upgrade's alert-worthy gauge value must stay visible until a human
// resolves it, which in practice means deleting the object.
func DeleteKubernetesUpgrade(namespace, name string) {
	KubernetesUpgradePhaseInfo.DeletePartialMatch(prometheus.Labels{
		"namespace": namespace,
		"name":      name,
	})
	KubernetesUpgradePhaseStartTimestampSeconds.DeleteLabelValues(namespace, name)
}

// SetNodeGroupNodesByPhase records the current count of nodes in each
// phase for a NodeGroupUpgrade. counts must be a COMPLETE phase->count
// map (every phase this group's nodes can be in, including ones now at
// zero) - a partial map would leave a phase that used to have nodes but
// no longer does sitting at a stale nonzero value forever.
func SetNodeGroupNodesByPhase(namespace, kubernetesUpgrade, nodeGroup, provider string, counts map[string]int32) {
	for phase, count := range counts {
		NodeGroupNodesByPhase.WithLabelValues(namespace, kubernetesUpgrade, nodeGroup, provider, phase).Set(float64(count))
	}
}

// DeleteNodeGroupUpgrade removes every series for the NodeGroupUpgrade
// identified by namespace/kubernetesUpgrade/nodeGroup. Call this from the
// same place its lifecycle actually ends (deletion), mirroring
// DeleteKubernetesUpgrade.
func DeleteNodeGroupUpgrade(namespace, kubernetesUpgrade, nodeGroup string) {
	NodeGroupNodesByPhase.DeletePartialMatch(prometheus.Labels{
		"namespace":         namespace,
		"kubernetesupgrade": kubernetesUpgrade,
		"nodegroup":         nodeGroup,
	})
}

func init() {
	metrics.Registry.MustRegister(
		KubernetesUpgradePhaseInfo,
		KubernetesUpgradePhaseStartTimestampSeconds,
		NodeGroupNodesByPhase,
		AdapterOperationTotal,
		AdapterOperationDuration,
		ExecutorJobTotal,
		EtcdQuorumHealthy,
		UpgradeLeaseAcquiredTimestampSeconds,
		DrainBlockedTotal,
	)
}
