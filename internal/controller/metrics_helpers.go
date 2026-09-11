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

package controller

import (
	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	obs "github.com/Ningendo7/kubernetes-upgrade-operator/pkg/observability"
)

// setKUPhase assigns ku.Status.Phase and, only on an actual change,
// records the phase-transition metrics. Safe to call redundantly: a
// no-op assignment records nothing, so the phase-start timestamp only
// moves on a real transition.
func setKUPhase(ku *upgradev1alpha1.KubernetesUpgrade, phase upgradev1alpha1.KubernetesUpgradePhase) {
	if ku.Status.Phase == phase {
		return
	}
	ku.Status.Phase = phase
	obs.SetPhase(ku.Namespace, ku.Name, string(phase))
}

// nodeGroupPhaseCounts derives a complete phase->count map from a
// NodeGroupUpgrade's per-node progress - "complete" meaning every phase
// present in progress is represented, so a phase that drops to zero is
// explicitly set to zero rather than left stale (see
// obs.SetNodeGroupNodesByPhase).
func nodeGroupPhaseCounts(progress []upgradev1alpha1.NodeProgress) map[string]int32 {
	counts := map[string]int32{}
	for _, np := range progress {
		phase := np.Phase
		if phase == "" {
			phase = "Pending"
		}
		counts[phase] = 0
	}
	for _, np := range progress {
		phase := np.Phase
		if phase == "" {
			phase = "Pending"
		}
		counts[phase]++
	}
	return counts
}

// recordNodeGroupPhaseCounts publishes the current per-phase node counts
// for a NodeGroupUpgrade. Called every reconcile so the gauge tracks
// reality; the series are cleaned up when the group is deleted (see the
// KubernetesUpgrade finalizer path).
func recordNodeGroupPhaseCounts(ng *upgradev1alpha1.NodeGroupUpgrade) {
	obs.SetNodeGroupNodesByPhase(
		ng.Namespace,
		ng.Labels[parentLabelKey],
		ng.Labels[groupLabelKey],
		string(ng.Spec.Provider),
		nodeGroupPhaseCounts(ng.Status.NodeProgress),
	)
}
