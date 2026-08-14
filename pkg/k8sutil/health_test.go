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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func cpNode(name string, ready bool) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{ControlPlaneLabelKey: ""},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
}

func newHealthTestClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestCheckControlPlaneHealth(t *testing.T) {
	tests := []struct {
		name        string
		nodes       []client.Object
		wantHealthy bool
	}{
		{name: "no control-plane nodes (managed control plane)", nodes: nil, wantHealthy: true},
		{name: "single node, ready", nodes: []client.Object{cpNode("cp-1", true)}, wantHealthy: true},
		{name: "3 nodes, 2 ready - quorum met", nodes: []client.Object{cpNode("cp-1", true), cpNode("cp-2", true), cpNode("cp-3", false)}, wantHealthy: true},
		{name: "3 nodes, 1 ready - quorum lost", nodes: []client.Object{cpNode("cp-1", true), cpNode("cp-2", false), cpNode("cp-3", false)}, wantHealthy: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newHealthTestClient(tt.nodes...)
			health, err := CheckControlPlaneHealth(context.Background(), c)
			if err != nil {
				t.Fatalf("CheckControlPlaneHealth: %v", err)
			}
			if health.Healthy != tt.wantHealthy {
				t.Errorf("Healthy = %v, want %v (reason: %s)", health.Healthy, tt.wantHealthy, health.Reason)
			}
		})
	}
}
