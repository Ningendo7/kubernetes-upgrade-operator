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
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
)

func newDrainingTestReconciler(objs ...client.Object) *NodeGroupUpgradeReconciler {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &NodeGroupUpgradeReconciler{Client: c}
}

func ngWithStartedNode(nodeName string, startedAt time.Time, force bool, timeoutSeconds *int32) *upgradev1alpha1.NodeGroupUpgrade {
	started := metav1.NewTime(startedAt)
	return &upgradev1alpha1.NodeGroupUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ng-1", Namespace: "default"},
		Spec: upgradev1alpha1.NodeGroupUpgradeSpec{
			Drain: upgradev1alpha1.DrainPolicy{
				Force:          force,
				TimeoutSeconds: timeoutSeconds,
			},
		},
		Status: upgradev1alpha1.NodeGroupUpgradeStatus{
			NodeProgress: []upgradev1alpha1.NodeProgress{
				{Name: nodeName, StartedAt: &started},
			},
		},
	}
}

func TestHandleStuckDrain_NotYetTimedOut(t *testing.T) {
	r := newDrainingTestReconciler()
	ng := ngWithStartedNode("node-1", time.Now(), false, nil) // just started, default 10m timeout

	result := k8sutil.DrainResult{Pods: []k8sutil.PodStatus{{Namespace: "default", Name: "app-1", State: k8sutil.PodDrainBlocked}}}
	if err := r.handleStuckDrain(context.Background(), ng, "node-1", result); err != nil {
		t.Fatalf("handleStuckDrain: %v", err)
	}

	for _, c := range ng.Status.Conditions {
		if c.Type == "DrainStuck" {
			t.Errorf("expected no DrainStuck condition before the timeout elapses, got %+v", c)
		}
	}
}

func TestHandleStuckDrain_TimedOutWithoutForce_SetsCondition(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-1"}}
	r := newDrainingTestReconciler(pod)
	timeout := int32(1) // 1 second - "started 5s ago" below is comfortably timed out
	ng := ngWithStartedNode("node-1", time.Now().Add(-5*time.Second), false, &timeout)

	result := k8sutil.DrainResult{Pods: []k8sutil.PodStatus{{Namespace: "default", Name: "app-1", State: k8sutil.PodDrainBlocked}}}
	if err := r.handleStuckDrain(context.Background(), ng, "node-1", result); err != nil {
		t.Fatalf("handleStuckDrain: %v", err)
	}

	found := false
	for _, c := range ng.Status.Conditions {
		if c.Type == "DrainStuck" {
			found = true
			if c.Status != metav1.ConditionTrue {
				t.Errorf("expected DrainStuck=True, got %v", c.Status)
			}
		}
	}
	if !found {
		t.Errorf("expected a DrainStuck condition to be set")
	}

	// Force was false: the pod must NOT have been deleted.
	var got corev1.Pod
	if err := r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "app-1"}, &got); err != nil {
		t.Fatalf("expected pod to still exist (force=false), got err: %v", err)
	}
}

func TestHandleStuckDrain_TimedOutWithForce_DeletesBlockedPods(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-1"}}
	r := newDrainingTestReconciler(pod)
	timeout := int32(1)
	ng := ngWithStartedNode("node-1", time.Now().Add(-5*time.Second), true, &timeout)

	result := k8sutil.DrainResult{Pods: []k8sutil.PodStatus{{Namespace: "default", Name: "app-1", State: k8sutil.PodDrainBlocked}}}
	if err := r.handleStuckDrain(context.Background(), ng, "node-1", result); err != nil {
		t.Fatalf("handleStuckDrain: %v", err)
	}

	err := r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "app-1"}, &corev1.Pod{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected the blocked pod to be force-deleted, got err: %v", err)
	}
}

func TestHandleStuckDrain_AlreadyDeletedPodIsNotAnError(t *testing.T) {
	r := newDrainingTestReconciler() // no pod created at all
	timeout := int32(1)
	ng := ngWithStartedNode("node-1", time.Now().Add(-5*time.Second), true, &timeout)

	result := k8sutil.DrainResult{Pods: []k8sutil.PodStatus{{Namespace: "default", Name: "already-gone", State: k8sutil.PodDrainBlocked}}}
	if err := r.handleStuckDrain(context.Background(), ng, "node-1", result); err != nil {
		t.Fatalf("expected a NotFound deletion attempt to be tolerated, got: %v", err)
	}
}

func TestHandleStuckDrain_NoNodeProgressEntryIsNoOp(t *testing.T) {
	r := newDrainingTestReconciler()
	ng := &upgradev1alpha1.NodeGroupUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ng-1", Namespace: "default"},
	}
	if err := r.handleStuckDrain(context.Background(), ng, "missing-node", k8sutil.DrainResult{}); err != nil {
		t.Fatalf("expected no error when there's no matching NodeProgress entry: %v", err)
	}
}
