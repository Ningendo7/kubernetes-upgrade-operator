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

package k8sutil

import "testing"

func TestIsSingleMinorHop(t *testing.T) {
	tests := []struct {
		name    string
		current string
		target  string
		want    bool
		wantErr bool
	}{
		{name: "single minor hop", current: "v1.29.4", target: "v1.30.0", want: true},
		{name: "same version", current: "v1.29.4", target: "v1.29.9", want: false},
		{name: "two minor hop", current: "v1.28.0", target: "v1.30.0", want: false},
		{name: "downgrade", current: "v1.30.0", target: "v1.29.0", want: false},
		{name: "major version change errors", current: "v1.29.0", target: "v2.0.0", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsSingleMinorHop(tt.current, tt.target)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("IsSingleMinorHop(%q, %q) = %v, want %v", tt.current, tt.target, got, tt.want)
			}
		})
	}
}

func TestCheckVersionSkew(t *testing.T) {
	tests := []struct {
		name             string
		apiserverVersion string
		kubeletVersion   string
		wantErr          bool
	}{
		{name: "same version", apiserverVersion: "v1.30.0", kubeletVersion: "v1.30.0", wantErr: false},
		{name: "kubelet one minor behind", apiserverVersion: "v1.30.0", kubeletVersion: "v1.29.0", wantErr: false},
		{name: "kubelet three minors behind (boundary)", apiserverVersion: "v1.30.0", kubeletVersion: "v1.27.0", wantErr: false},
		{name: "kubelet four minors behind", apiserverVersion: "v1.30.0", kubeletVersion: "v1.26.0", wantErr: true},
		{name: "kubelet newer than apiserver", apiserverVersion: "v1.29.0", kubeletVersion: "v1.30.0", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckVersionSkew(tt.apiserverVersion, tt.kubeletVersion)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
