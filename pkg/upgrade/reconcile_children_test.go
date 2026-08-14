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
	"reflect"
	"sort"
	"testing"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

func strategyPtr(s upgradev1alpha1.NodeGroupStrategy) *upgradev1alpha1.NodeGroupStrategy { return &s }

func TestResolveStrategy(t *testing.T) {
	tests := []struct {
		name     string
		group    DiscoveredGroup
		override *upgradev1alpha1.NodeGroupOverride
		want     upgradev1alpha1.NodeGroupStrategy
	}{
		{
			name:  "control-plane is always InPlace, no override",
			group: DiscoveredGroup{Role: upgradev1alpha1.RoleControlPlane, Provider: upgradev1alpha1.ProviderKubeadm},
			want:  upgradev1alpha1.StrategyInPlace,
		},
		{
			name:     "control-plane ignores an override attempting Replace",
			group:    DiscoveredGroup{Role: upgradev1alpha1.RoleControlPlane, Provider: upgradev1alpha1.ProviderKubeadm},
			override: &upgradev1alpha1.NodeGroupOverride{Strategy: strategyPtr(upgradev1alpha1.StrategyReplace)},
			want:     upgradev1alpha1.StrategyInPlace,
		},
		{
			name:  "kubeadm worker defaults to InPlace",
			group: DiscoveredGroup{Role: upgradev1alpha1.RoleWorker, Provider: upgradev1alpha1.ProviderKubeadm},
			want:  upgradev1alpha1.StrategyInPlace,
		},
		{
			name:     "kubeadm worker can be overridden to Replace",
			group:    DiscoveredGroup{Role: upgradev1alpha1.RoleWorker, Provider: upgradev1alpha1.ProviderKubeadm},
			override: &upgradev1alpha1.NodeGroupOverride{Strategy: strategyPtr(upgradev1alpha1.StrategyReplace)},
			want:     upgradev1alpha1.StrategyReplace,
		},
		{
			name:  "EKS managed node group defaults to Replace",
			group: DiscoveredGroup{Role: upgradev1alpha1.RoleWorker, Provider: upgradev1alpha1.ProviderAWSEKSManagedNodeGroup},
			want:  upgradev1alpha1.StrategyReplace,
		},
		{
			name:     "EKS managed node group rejects an InPlace override",
			group:    DiscoveredGroup{Role: upgradev1alpha1.RoleWorker, Provider: upgradev1alpha1.ProviderAWSEKSManagedNodeGroup},
			override: &upgradev1alpha1.NodeGroupOverride{Strategy: strategyPtr(upgradev1alpha1.StrategyInPlace)},
			want:     upgradev1alpha1.StrategyReplace,
		},
		{
			name:  "Linode LKE defaults to Replace",
			group: DiscoveredGroup{Role: upgradev1alpha1.RoleWorker, Provider: upgradev1alpha1.ProviderLinodeLKE},
			want:  upgradev1alpha1.StrategyReplace,
		},
		{
			name:  "self-managed ASG defaults to Replace but allows InPlace override",
			group: DiscoveredGroup{Role: upgradev1alpha1.RoleWorker, Provider: upgradev1alpha1.ProviderAWSAutoScalingGroup},
			want:  upgradev1alpha1.StrategyReplace,
		},
		{
			name:     "self-managed ASG override to InPlace is honored",
			group:    DiscoveredGroup{Role: upgradev1alpha1.RoleWorker, Provider: upgradev1alpha1.ProviderAWSAutoScalingGroup},
			override: &upgradev1alpha1.NodeGroupOverride{Strategy: strategyPtr(upgradev1alpha1.StrategyInPlace)},
			want:     upgradev1alpha1.StrategyInPlace,
		},
		{
			name:  "generic defaults to InPlace",
			group: DiscoveredGroup{Role: upgradev1alpha1.RoleWorker, Provider: upgradev1alpha1.ProviderGeneric},
			want:  upgradev1alpha1.StrategyInPlace,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveStrategy(tt.group, tt.override)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPruneCandidates(t *testing.T) {
	desired := []DiscoveredGroup{
		{Name: "control-plane"},
		{Name: "ng-1"},
	}
	existing := []ExistingChild{
		{Name: "control-plane", Phase: upgradev1alpha1.NGUpgrading},
		{Name: "ng-1", Phase: upgradev1alpha1.NGComplete},
		{Name: "vanished-complete", Phase: upgradev1alpha1.NGComplete},
		{Name: "vanished-never-started", Phase: ""},
		{Name: "vanished-still-draining", Phase: upgradev1alpha1.NGDraining},
		{Name: "vanished-failed", Phase: upgradev1alpha1.NGFailed},
	}

	got := PruneCandidates(desired, existing)
	sort.Strings(got)
	want := []string{"vanished-complete", "vanished-never-started"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}