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

func newFakeClientWithNode(node *corev1.Node) client.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
}

func TestCordonUncordon(t *testing.T) {
	ctx := context.Background()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	c := newFakeClientWithNode(node)

	if err := Cordon(ctx, c, "node-1"); err != nil {
		t.Fatalf("Cordon: %v", err)
	}
	var got corev1.Node
	if err := c.Get(ctx, client.ObjectKey{Name: "node-1"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Spec.Unschedulable {
		t.Fatalf("expected node to be unschedulable after Cordon")
	}

	// Cordoning an already-cordoned node should be a no-op, not an error.
	if err := Cordon(ctx, c, "node-1"); err != nil {
		t.Fatalf("Cordon (idempotent call): %v", err)
	}

	if err := Uncordon(ctx, c, "node-1"); err != nil {
		t.Fatalf("Uncordon: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: "node-1"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.Unschedulable {
		t.Fatalf("expected node to be schedulable after Uncordon")
	}
}

func TestCordonMissingNode(t *testing.T) {
	c := newFakeClientWithNode(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "other-node"}})
	if err := Cordon(context.Background(), c, "does-not-exist"); err == nil {
		t.Fatalf("expected error cordoning a nonexistent node")
	}
}
