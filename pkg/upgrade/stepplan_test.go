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
	"testing"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

func TestComputeStepPlan(t *testing.T) {
	tests := []struct {
		name           string
		current        string
		target         string
		allowDowngrade bool
		want           []upgradev1alpha1.UpgradeStep
		wantErr        bool
	}{
		{
			name:    "already at target",
			current: "v1.29.4",
			target:  "v1.29.4",
			want:    nil,
		},
		{
			name:    "patch-only bump",
			current: "v1.29.4",
			target:  "v1.29.9",
			want: []upgradev1alpha1.UpgradeStep{
				{FromVersion: "v1.29.4", ToVersion: "v1.29.9"},
			},
		},
		{
			name:    "single minor hop",
			current: "v1.29.4",
			target:  "v1.30.0",
			want: []upgradev1alpha1.UpgradeStep{
				{FromVersion: "v1.29.4", ToVersion: "v1.30.0"},
			},
		},
		{
			name:    "multi-minor hop decomposes into single-minor steps",
			current: "v1.27.4",
			target:  "v1.30.2",
			want: []upgradev1alpha1.UpgradeStep{
				{FromVersion: "v1.27.4", ToVersion: "v1.28.0"},
				{FromVersion: "v1.28.0", ToVersion: "v1.29.0"},
				{FromVersion: "v1.29.0", ToVersion: "v1.30.2"},
			},
		},
		{
			name:    "downgrade rejected by default",
			current: "v1.30.0",
			target:  "v1.29.0",
			wantErr: true,
		},
		{
			name:           "downgrade allowed when explicitly enabled",
			current:        "v1.30.0",
			target:         "v1.29.0",
			allowDowngrade: true,
			want: []upgradev1alpha1.UpgradeStep{
				{FromVersion: "v1.30.0", ToVersion: "v1.29.0"},
			},
		},
		{
			name:    "major version change rejected",
			current: "v1.30.0",
			target:  "v2.0.0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ComputeStepPlan(tt.current, tt.target, tt.allowDowngrade)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (steps=%+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
