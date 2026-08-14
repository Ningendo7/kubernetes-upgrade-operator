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

package upgrade

import (
	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

// ResolveStrategy determines the upgrade strategy for a discovered group,
// applying provider-appropriate defaults and any user override, with one
// hard safety rule that cannot be overridden: control-plane nodes are
// always upgraded InPlace. Replacing a control-plane node risks etcd
// membership/quorum in ways this operator does not manage.
func ResolveStrategy(group DiscoveredGroup, override *upgradev1alpha1.NodeGroupOverride) upgradev1alpha1.NodeGroupStrategy {
	if group.Role == upgradev1alpha1.RoleControlPlane {
		return upgradev1alpha1.StrategyInPlace
	}

	def := defaultStrategyForProvider(group.Provider)

	if override != nil && override.Strategy != nil {
		requested := *override.Strategy
		if isStrategyAllowedForProvider(group.Provider, requested) {
			return requested
		}
	}

	return def
}

func defaultStrategyForProvider(p upgradev1alpha1.ProviderType) upgradev1alpha1.NodeGroupStrategy {
	switch p {
	case upgradev1alpha1.ProviderAWSEKSManagedNodeGroup, upgradev1alpha1.ProviderLinodeLKE, upgradev1alpha1.ProviderAWSAutoScalingGroup:
		return upgradev1alpha1.StrategyReplace
	default: // Kubeadm, Generic
		return upgradev1alpha1.StrategyInPlace
	}
}

// isStrategyAllowedForProvider rejects overrides that don't make sense for
// a given provider: EKS-managed node groups and LKE pools are always
// replaced by their respective cloud control planes, there is no in-place
// path for them.
func isStrategyAllowedForProvider(p upgradev1alpha1.ProviderType, s upgradev1alpha1.NodeGroupStrategy) bool {
	switch p {
	case upgradev1alpha1.ProviderAWSEKSManagedNodeGroup, upgradev1alpha1.ProviderLinodeLKE:
		return s == upgradev1alpha1.StrategyReplace
	default:
		return true
	}
}

// ExistingChild is the minimal view of an existing NodeGroupUpgrade needed
// to decide whether it's safe to prune.
type ExistingChild struct {
	Name      string
	Phase	 upgradev1alpha1.NodeGroupUpgradePhase
}

// PruneCandidates returns the names of existing NodeGroupUpgrade children
// that no longer correspond to any discovered group and are safe to
// delete: never started, Pending, or already Complete. Children actively
// Draining/Upgrading/Verifying, or that ended Failed/Paused, are never
// auto-pruned even if their group's nodes have all disappeared - they must
// finish, or be resolved by a human, first.
func PruneCandidates(desired []DiscoveredGroup, existing []ExistingChild) []string {
	desiredNames := make(map[string]bool, len(desired))
	for _, d := range desired {
		desiredNames[d.Name] = true
	}

	var prune []string
	for _, e := range existing {
		if desiredNames[e.Name] {
			continue
		}
		if isSafeToPrune(e.Phase) {
			prune = append(prune, e.Name)
		}
	}
	return prune
}

func isSafeToPrune(phase upgradev1alpha1.NodeGroupUpgradePhase) bool {
	switch phase {
		case "", upgradev1alpha1.NGPending, upgradev1alpha1.NGComplete:
			return true
		default:
			return false
	}
}
