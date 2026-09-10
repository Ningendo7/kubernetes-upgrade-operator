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
	"testing"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

func TestNeedsReset(t *testing.T) {
	tests := []struct {
		name string
		ng   *upgradev1alpha1.NodeGroupUpgrade
		want bool
	}{
		{
			name: "not complete - never needs reset",
			ng: &upgradev1alpha1.NodeGroupUpgrade{
				Status: upgradev1alpha1.NodeGroupUpgradeStatus{Phase: upgradev1alpha1.NGDraining},
			},
			want: false,
		},
		{
			name: "complete with no NodeProgress - nothing to reset",
			ng: &upgradev1alpha1.NodeGroupUpgrade{
				Status: upgradev1alpha1.NodeGroupUpgradeStatus{Phase: upgradev1alpha1.NGComplete},
			},
			want: false,
		},
		{
			name: "complete, same target version - genuinely done, no reset",
			ng: &upgradev1alpha1.NodeGroupUpgrade{
				Spec: upgradev1alpha1.NodeGroupUpgradeSpec{TargetVersion: "v1.30.0"},
				Status: upgradev1alpha1.NodeGroupUpgradeStatus{
					Phase:        upgradev1alpha1.NGComplete,
					NodeProgress: []upgradev1alpha1.NodeProgress{{Name: "n1", ToVersion: "v1.30.0"}},
				},
			},
			want: false,
		},
		{
			name: "complete, different target version - a new hop has begun",
			ng: &upgradev1alpha1.NodeGroupUpgrade{
				Spec: upgradev1alpha1.NodeGroupUpgradeSpec{TargetVersion: "v1.31.0"},
				Status: upgradev1alpha1.NodeGroupUpgradeStatus{
					Phase:        upgradev1alpha1.NGComplete,
					NodeProgress: []upgradev1alpha1.NodeProgress{{Name: "n1", ToVersion: "v1.30.0"}},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsReset(tt.ng); got != tt.want {
				t.Errorf("needsReset() = %v, want %v", got, tt.want)
			}
		})
	}
}
