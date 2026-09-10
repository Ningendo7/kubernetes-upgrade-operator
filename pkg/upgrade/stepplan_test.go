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

// TestNextGroupTarget covers the bug found via real-cluster testing: a
// hop's ToVersion is computed from the apiserver's version, which is not
// necessarily a specific group's own actual current version (e.g. a group
// that started behind the rest of the cluster, or is resuming a
// partially-completed upgrade). NextGroupTarget must never hand back a
// target more than one minor ahead of groupCurrent, even when hopTarget
// itself is further ahead than that.
func TestNextGroupTarget(t *testing.T) {
	tests := []struct {
		name         string
		groupCurrent string
		hopTarget    string
		want         string
		wantErr      bool
	}{
		{
			name:         "group already at hop target",
			groupCurrent: "v1.29.15",
			hopTarget:    "v1.29.15",
			want:         "v1.29.15",
		},
		{
			name: "group already ahead of hop target: stays put, never regresses",
			// A group that already finished an earlier hop (or the whole
			// upgrade) on a prior pass can be strictly ahead of this
			// hop's target - returning hopTarget here would tell it to
			// downgrade, which is the real bug this covers.
			groupCurrent: "v1.30.0",
			hopTarget:    "v1.29.15",
			want:         "v1.30.0",
		},
		{
			name:         "group exactly one minor behind: goes straight to hop target",
			groupCurrent: "v1.29.15",
			hopTarget:    "v1.30.14",
			want:         "v1.30.14",
		},
		{
			name:         "group two minors behind: clamped to its own next minor, not hop target",
			groupCurrent: "v1.28.15",
			hopTarget:    "v1.30.14",
			want:         "v1.29.0",
		},
		{
			name:         "group three minors behind: still only one minor at a time",
			groupCurrent: "v1.27.10",
			hopTarget:    "v1.30.14",
			want:         "v1.28.0",
		},
		{
			name:         "malformed groupCurrent",
			groupCurrent: "not-a-version",
			hopTarget:    "v1.30.14",
			wantErr:      true,
		},
		{
			name:         "malformed hopTarget",
			groupCurrent: "v1.28.15",
			hopTarget:    "not-a-version",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextGroupTarget(tt.groupCurrent, tt.hopTarget)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
