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

package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// resetAll clears every vec these tests touch. The metrics are
// package-global (registered once in init), so tests share state and
// must not leak series into each other.
func resetAll(t *testing.T) {
	t.Helper()
	KubernetesUpgradePhaseInfo.Reset()
	KubernetesUpgradePhaseStartTimestampSeconds.Reset()
	NodeGroupNodesByPhase.Reset()
	t.Cleanup(func() {
		KubernetesUpgradePhaseInfo.Reset()
		KubernetesUpgradePhaseStartTimestampSeconds.Reset()
		NodeGroupNodesByPhase.Reset()
	})
}

func TestSetPhase_ClearsPriorPhaseForSameObject(t *testing.T) {
	resetAll(t)

	SetPhase("default", "upg-1", "Discovering")
	if got := testutil.ToFloat64(KubernetesUpgradePhaseInfo.WithLabelValues("default", "upg-1", "Discovering")); got != 1 {
		t.Fatalf("expected Discovering series = 1, got %v", got)
	}

	SetPhase("default", "upg-1", "Prechecks")

	// The old phase must be gone entirely, not just set to 0 - a lingering
	// {phase="Discovering"} series would make "count by (phase)" dashboards
	// double-count and break Failed/Paused alert queries.
	if n := testutil.CollectAndCount(KubernetesUpgradePhaseInfo); n != 1 {
		t.Errorf("expected exactly 1 phase series for the object, got %d", n)
	}
	if got := testutil.ToFloat64(KubernetesUpgradePhaseInfo.WithLabelValues("default", "upg-1", "Prechecks")); got != 1 {
		t.Errorf("expected Prechecks series = 1, got %v", got)
	}
}

func TestSetPhase_IndependentPerObject(t *testing.T) {
	resetAll(t)

	SetPhase("default", "upg-a", "WorkersUpgrade")
	SetPhase("default", "upg-b", "ControlPlaneUpgrade")

	if n := testutil.CollectAndCount(KubernetesUpgradePhaseInfo); n != 2 {
		t.Fatalf("expected 2 series (one per object), got %d", n)
	}

	// Transitioning one object must not disturb the other.
	SetPhase("default", "upg-a", "Postchecks")
	if got := testutil.ToFloat64(KubernetesUpgradePhaseInfo.WithLabelValues("default", "upg-b", "ControlPlaneUpgrade")); got != 1 {
		t.Errorf("upg-b's series should be untouched, got %v", got)
	}
}

func TestSetPhase_RecordsAStartTimestamp(t *testing.T) {
	resetAll(t)

	before := time.Now().Unix()
	SetPhase("default", "upg-1", "Discovering")
	after := time.Now().Unix()

	got := testutil.ToFloat64(KubernetesUpgradePhaseStartTimestampSeconds.WithLabelValues("default", "upg-1"))
	if int64(got) < before || int64(got) > after {
		t.Errorf("timestamp %v not within [%d, %d]", got, before, after)
	}
}

func TestDeleteKubernetesUpgrade_RemovesAllItsSeriesOnly(t *testing.T) {
	resetAll(t)

	SetPhase("default", "gone", "Failed")
	SetPhase("default", "stays", "WorkersUpgrade")

	DeleteKubernetesUpgrade("default", "gone")

	if n := testutil.CollectAndCount(KubernetesUpgradePhaseInfo); n != 1 {
		t.Errorf("expected only the surviving object's phase series, got %d", n)
	}
	if n := testutil.CollectAndCount(KubernetesUpgradePhaseStartTimestampSeconds); n != 1 {
		t.Errorf("expected only the surviving object's timestamp series, got %d", n)
	}
	if got := testutil.ToFloat64(KubernetesUpgradePhaseInfo.WithLabelValues("default", "stays", "WorkersUpgrade")); got != 1 {
		t.Errorf("the surviving object's series should be intact, got %v", got)
	}
}

func TestSetNodeGroupNodesByPhase_CompleteMapZeroesDrainedPhases(t *testing.T) {
	resetAll(t)

	SetNodeGroupNodesByPhase("default", "upg-1", "workers", "kubeadm", map[string]int32{
		"Draining":  3,
		"Upgrading": 0,
		"Upgraded":  0,
	})
	if got := testutil.ToFloat64(NodeGroupNodesByPhase.WithLabelValues("default", "upg-1", "workers", "kubeadm", "Draining")); got != 3 {
		t.Fatalf("expected Draining = 3, got %v", got)
	}

	// A later pass where those 3 have moved on - the caller passes a
	// complete map, so Draining must go to 0, not stay stale at 3.
	SetNodeGroupNodesByPhase("default", "upg-1", "workers", "kubeadm", map[string]int32{
		"Draining":  0,
		"Upgrading": 0,
		"Upgraded":  3,
	})
	if got := testutil.ToFloat64(NodeGroupNodesByPhase.WithLabelValues("default", "upg-1", "workers", "kubeadm", "Draining")); got != 0 {
		t.Errorf("expected Draining = 0 after nodes moved on, got %v", got)
	}
	if got := testutil.ToFloat64(NodeGroupNodesByPhase.WithLabelValues("default", "upg-1", "workers", "kubeadm", "Upgraded")); got != 3 {
		t.Errorf("expected Upgraded = 3, got %v", got)
	}
}

func TestDeleteNodeGroupUpgrade_RemovesEveryPhaseSeriesForThatGroupOnly(t *testing.T) {
	resetAll(t)

	SetNodeGroupNodesByPhase("default", "upg-1", "workers", "kubeadm", map[string]int32{
		"Draining": 1, "Upgrading": 1, "Upgraded": 2,
	})
	SetNodeGroupNodesByPhase("default", "upg-1", "control-plane", "kubeadm", map[string]int32{
		"Upgraded": 3,
	})

	DeleteNodeGroupUpgrade("default", "upg-1", "workers")

	if n := testutil.CollectAndCount(NodeGroupNodesByPhase); n != 1 {
		t.Errorf("expected only the control-plane group's single series to remain, got %d", n)
	}
	if got := testutil.ToFloat64(NodeGroupNodesByPhase.WithLabelValues("default", "upg-1", "control-plane", "kubeadm", "Upgraded")); got != 3 {
		t.Errorf("the other group's series should be intact, got %v", got)
	}
}

// TestMetricLabelNames pins the label set of every metric - catches a
// stray string literal where a shared label constant was meant (e.g.
// "providerLabel" instead of providerLabel), which builds fine but
// silently ships the wrong label name to Prometheus.
func TestMetricLabelNames(t *testing.T) {
	cases := []struct {
		name  string
		c     prometheus.Collector
		setup func()
		want  []string
	}{
		{
			name:  "kuo_nodegroup_nodes_by_phase",
			c:     NodeGroupNodesByPhase,
			setup: func() { NodeGroupNodesByPhase.WithLabelValues("n", "k", "g", "kubeadm", "Upgraded").Set(1) },
			want:  []string{"namespace", "kubernetesupgrade", "nodegroup", "provider", "phase"},
		},
		{
			name:  "kuo_adapter_operation_total",
			c:     AdapterOperationTotal,
			setup: func() { AdapterOperationTotal.WithLabelValues("kubeadm", "Precheck", "ready").Inc() },
			want:  []string{"provider", "operation", "outcome"},
		},
		{
			name:  "kuo_executor_job_total",
			c:     ExecutorJobTotal,
			setup: func() { ExecutorJobTotal.WithLabelValues("kubeadm", "upgrade", "succeeded").Inc() },
			want:  []string{"provider", "kind", "outcome"},
		},
		{
			name:  "kuo_drain_blocked_total",
			c:     DrainBlockedTotal,
			setup: func() { DrainBlockedTotal.WithLabelValues("n", "g", "kubeadm").Inc() },
			want:  []string{"namespace", "nodegroup", "provider"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			got := labelNamesOf(t, tc.c, tc.name)
			if !equalStringSets(got, tc.want) {
				t.Errorf("label names = %v, want %v", got, tc.want)
			}
		})
	}
}

func labelNamesOf(t *testing.T, c prometheus.Collector, metricName string) []string {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != metricName || len(f.GetMetric()) == 0 {
			continue
		}
		var names []string
		for _, lp := range f.GetMetric()[0].GetLabel() {
			names = append(names, lp.GetName())
		}
		return names
	}
	t.Fatalf("metric %q not found in gathered families", metricName)
	return nil
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			return false
		}
	}
	return true
}
