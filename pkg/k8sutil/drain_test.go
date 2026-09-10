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
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func nodeNameIndexer(obj client.Object) []string {
	pod := obj.(*corev1.Pod)
	return []string{pod.Spec.NodeName}
}

func newDrainTestClient(t *testing.T, funcs interceptor.Funcs, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := policyv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&corev1.Pod{}, NodeNameIndexField, nodeNameIndexer).
		WithObjects(objs...).
		WithInterceptorFuncs(funcs).
		Build()
}

func testPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
	}
}

func podState(t *testing.T, result DrainResult, name string) PodStatus {
	t.Helper()
	for _, p := range result.Pods {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no PodStatus found for pod %q in %+v", name, result.Pods)
	return PodStatus{}
}

func TestDrainNode_EvictsOrdinaryPod(t *testing.T) {
	ctx := context.Background()
	pod := testPod("app-1")
	c := newDrainTestClient(t, interceptor.Funcs{}, pod)

	result, err := DrainNode(ctx, c, "node-1", DrainOptions{IgnoreDaemonSets: true, DeleteEmptyDirData: true})
	if err != nil {
		t.Fatalf("DrainNode: %v", err)
	}
	if result.Remaining() != 1 {
		t.Fatalf("Remaining() = %d, want 1", result.Remaining())
	}
	if len(result.Blocked()) != 0 {
		t.Fatalf("Blocked() = %+v, want none", result.Blocked())
	}
	if state := podState(t, result, "app-1").State; state != PodDrainEvicting {
		t.Errorf("expected app-1 to be Evicting, got %v", state)
	}

	var got corev1.Pod
	err = c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "app-1"}, &got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected pod to be evicted (deleted), got err=%v", err)
	}
}

func TestDrainNode_SkipsDaemonSetPod(t *testing.T) {
	ctx := context.Background()
	pod := testPod("ds-pod")
	pod.OwnerReferences = []metav1.OwnerReference{{Kind: "DaemonSet", Name: "cni", APIVersion: "apps/v1", UID: "abc"}}
	c := newDrainTestClient(t, interceptor.Funcs{}, pod)

	result, err := DrainNode(ctx, c, "node-1", DrainOptions{IgnoreDaemonSets: true, DeleteEmptyDirData: true})
	if err != nil {
		t.Fatalf("DrainNode: %v", err)
	}
	if result.Remaining() != 0 {
		t.Fatalf("Remaining() = %d, want 0 (daemonset pod should be skipped)", result.Remaining())
	}
	if state := podState(t, result, "ds-pod").State; state != PodDrainSkipped {
		t.Errorf("expected ds-pod to be Skipped, got %v", state)
	}

	var got corev1.Pod
	if err := c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "ds-pod"}, &got); err != nil {
		t.Fatalf("expected daemonset pod to remain untouched: %v", err)
	}
}

func TestDrainNode_BlocksOnEmptyDirWhenNotAllowed(t *testing.T) {
	ctx := context.Background()
	pod := testPod("stateful-ish")
	pod.Spec.Volumes = []corev1.Volume{{Name: "scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	c := newDrainTestClient(t, interceptor.Funcs{}, pod)

	result, err := DrainNode(ctx, c, "node-1", DrainOptions{IgnoreDaemonSets: true, DeleteEmptyDirData: false})
	if err != nil {
		t.Fatalf("DrainNode: %v", err)
	}
	if result.Remaining() != 1 || len(result.Blocked()) != 1 {
		t.Fatalf("got Remaining()=%d Blocked()=%+v, want 1 remaining and 1 blocked", result.Remaining(), result.Blocked())
	}
	if reason := podState(t, result, "stateful-ish").Reason; reason == "" {
		t.Errorf("expected a non-empty reason explaining the block")
	}

	var got corev1.Pod
	if err := c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "stateful-ish"}, &got); err != nil {
		t.Fatalf("expected pod to remain (blocked, not evicted): %v", err)
	}
}

func TestDrainNode_BlockedByPDBIsNotAFatalError(t *testing.T) {
	ctx := context.Background()
	pod := testPod("guarded")
	funcs := interceptor.Funcs{
		SubResourceCreate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
			if subResourceName == "eviction" {
				return apierrors.NewTooManyRequests("cannot evict pod as it would violate the pod's disruption budget", 0)
			}
			return cl.SubResource(subResourceName).Create(ctx, obj, subResource, opts...)
		},
	}
	c := newDrainTestClient(t, funcs, pod)

	result, err := DrainNode(ctx, c, "node-1", DrainOptions{IgnoreDaemonSets: true, DeleteEmptyDirData: true})
	if err != nil {
		t.Fatalf("DrainNode should not return a hard error when eviction is PDB-blocked: %v", err)
	}
	if result.Remaining() != 1 || len(result.Blocked()) != 1 {
		t.Fatalf("got Remaining()=%d Blocked()=%+v, want 1 remaining and 1 blocked", result.Remaining(), result.Blocked())
	}

	var got corev1.Pod
	if err := c.Get(ctx, client.ObjectKey{Namespace: "default", Name: "guarded"}, &got); err != nil {
		t.Fatalf("expected pod to remain since eviction was blocked: %v", err)
	}
}

func TestDrainNode_AlreadyEvictingPodIsReportedNotReattempted(t *testing.T) {
	ctx := context.Background()
	pod := testPod("mid-eviction")
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	pod.Finalizers = []string{"kubernetes.io/pv-protection"} // any finalizer keeps the fake client from hard-deleting immediately
	c := newDrainTestClient(t, interceptor.Funcs{}, pod)

	result, err := DrainNode(ctx, c, "node-1", DrainOptions{IgnoreDaemonSets: true, DeleteEmptyDirData: true})
	if err != nil {
		t.Fatalf("DrainNode: %v", err)
	}
	if state := podState(t, result, "mid-eviction").State; state != PodDrainEvicting {
		t.Errorf("expected an already-terminating pod to be reported Evicting, got %v", state)
	}
	if result.Remaining() != 1 {
		t.Errorf("expected an in-progress eviction to still count toward Remaining, got %d", result.Remaining())
	}
}

func TestDrainNode_GenericEvictionErrorIsReportedAndReturned(t *testing.T) {
	ctx := context.Background()
	pod := testPod("weird-failure")
	funcs := interceptor.Funcs{
		SubResourceCreate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
			if subResourceName == "eviction" {
				return errors.New("connection reset by peer")
			}
			return cl.SubResource(subResourceName).Create(ctx, obj, subResource, opts...)
		},
	}
	c := newDrainTestClient(t, funcs, pod)

	result, err := DrainNode(ctx, c, "node-1", DrainOptions{IgnoreDaemonSets: true, DeleteEmptyDirData: true})
	if err == nil {
		t.Fatalf("expected a genuine (non-PDB, non-NotFound) eviction error to be returned")
	}
	if state := podState(t, result, "weird-failure").State; state != PodDrainBlocked {
		t.Errorf("expected the pod to still be reported as Blocked even on a hard error, got %v", state)
	}
}
