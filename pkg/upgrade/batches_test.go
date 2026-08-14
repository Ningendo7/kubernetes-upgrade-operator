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

	"k8s.io/apimachinery/pkg/util/intstr"
)

func int32ptr(v int32) *int32                          { return &v }
func ptrIntOrString(v intstr.IntOrString) *intstr.IntOrString { return &v }

func TestResolveConcurrency(t *testing.T) {
	tests := []struct {
		name           string
		total          int
		batchSize      *int32
		maxUnavailable *intstr.IntOrString
		want           int
		wantErr        bool
	}{
		{name: "no total", total: 0, want: 0},
		{name: "default to 1 when nothing set", total: 10, want: 1},
		{name: "batchSize wins and is used directly", total: 10, batchSize: int32ptr(3), want: 3},
		{name: "batchSize clamped to total", total: 2, batchSize: int32ptr(5), want: 2},
		{name: "maxUnavailable as absolute int", total: 10, maxUnavailable: ptrIntOrString(intstr.FromInt(2)), want: 2},
		{name: "maxUnavailable as percent", total: 10, maxUnavailable: ptrIntOrString(intstr.FromString("25%")), want: 2},
		{name: "maxUnavailable resolving to 0 is floored to 1", total: 10, maxUnavailable: ptrIntOrString(intstr.FromString("5%")), want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveConcurrency(tt.total, tt.batchSize, tt.maxUnavailable)
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
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNextBatch(t *testing.T) {
	nodes := []string{"n1", "n2", "n3", "n4", "n5"}

	t.Run("first pass picks up to batchSize new nodes", func(t *testing.T) {
		got, err := NextBatch(nodes, map[string]bool{}, map[string]bool{}, int32ptr(2), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"n1", "n2"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("done and in-progress nodes are excluded", func(t *testing.T) {
		done := map[string]bool{"n1": true}
		inProgress := map[string]bool{"n2": true}
		got, err := NextBatch(nodes, done, inProgress, int32ptr(2), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"n3"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("no available slots when in-progress already at limit", func(t *testing.T) {
		inProgress := map[string]bool{"n1": true, "n2": true}
		got, err := NextBatch(nodes, map[string]bool{}, inProgress, int32ptr(2), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})

	t.Run("all done returns nothing", func(t *testing.T) {
		done := map[string]bool{"n1": true, "n2": true, "n3": true, "n4": true, "n5": true}
		got, err := NextBatch(nodes, done, map[string]bool{}, int32ptr(2), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want none", got)
		}
	})
}