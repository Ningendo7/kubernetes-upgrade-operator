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

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsNodeReady(t *testing.T) {
	tests := []struct {
		name       string
		conditions []corev1.NodeCondition
		want       bool
	}{
		{name: "ready true", conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}, want: true},
		{name: "ready false", conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}, want: false},
		{name: "no ready condition", conditions: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{Status: corev1.NodeStatus{Conditions: tt.conditions}}
			if got := IsNodeReady(node); got != tt.want {
				t.Errorf("IsNodeReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeIsAtVersion(t *testing.T) {
	node := &corev1.Node{Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.29.4"}}}

	got, err := NodeIsAtVersion(node, "v1.29.4")
	if err != nil || !got {
		t.Fatalf("expected node to be at v1.29.4, got=%v err=%v", got, err)
	}

	got, err = NodeIsAtVersion(node, "v1.30.0")
	if err != nil || got {
		t.Fatalf("expected node to not be at v1.30.0, got=%v err=%v", got, err)
	}
}

func TestGetNode(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}).
		Build()

	node, err := GetNode(context.Background(), c, "node-1")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.Name != "node-1" {
		t.Fatalf("got node %q, want node-1", node.Name)
	}

	if _, err := GetNode(context.Background(), c, "missing"); err == nil {
		t.Fatalf("expected error for missing node")
	}
}