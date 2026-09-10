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
	"errors"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

func newUpgradingTestReconciler(t *testing.T, ng *upgradev1alpha1.NodeGroupUpgrade, recorder record.EventRecorder) *NodeGroupUpgradeReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := upgradev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&upgradev1alpha1.NodeGroupUpgrade{}).
		WithObjects(ng).
		Build()
	return &NodeGroupUpgradeReconciler{Client: c, Recorder: recorder}
}

func TestHandleAdapterError_NotImplemented_PausesAndEmitsEvent(t *testing.T) {
	ng := &upgradev1alpha1.NodeGroupUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ng-1", Namespace: "default"},
		Spec:       upgradev1alpha1.NodeGroupUpgradeSpec{Provider: upgradev1alpha1.ProviderAWSEKSManagedNodeGroup},
	}
	recorder := record.NewFakeRecorder(10)
	r := newUpgradingTestReconciler(t, ng, recorder)

	// Wrapped, not the bare sentinel - confirms errors.Is() sees through
	// wrapping the way a real adapter's error would be wrapped.
	wrapped := fmt.Errorf("calling BeginBatch: %w", provider.ErrNotImplemented)
	if _, err := r.handleAdapterError(context.Background(), ng, "BeginBatch", wrapped); err != nil {
		t.Fatalf("handleAdapterError: %v", err)
	}

	if ng.Status.Phase != upgradev1alpha1.NGPaused {
		t.Errorf("expected phase Paused, got %v", ng.Status.Phase)
	}

	select {
	case event := <-recorder.Events:
		if event == "" {
			t.Errorf("expected a non-empty event")
		}
	default:
		t.Errorf("expected an Event to be recorded for ErrNotImplemented")
	}
}

func TestHandleAdapterError_NotImplemented_NilRecorderDoesNotPanic(t *testing.T) {
	ng := &upgradev1alpha1.NodeGroupUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ng-1", Namespace: "default"},
	}
	r := newUpgradingTestReconciler(t, ng, nil)

	if _, err := r.handleAdapterError(context.Background(), ng, "Precheck", provider.ErrNotImplemented); err != nil {
		t.Fatalf("handleAdapterError: %v", err)
	}
	if ng.Status.Phase != upgradev1alpha1.NGPaused {
		t.Errorf("expected phase Paused, got %v", ng.Status.Phase)
	}
}

func TestHandleAdapterError_GenericError_Fails(t *testing.T) {
	ng := &upgradev1alpha1.NodeGroupUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ng-1", Namespace: "default"},
	}
	r := newUpgradingTestReconciler(t, ng, nil)

	if _, err := r.handleAdapterError(context.Background(), ng, "PollBatch", errors.New("boom")); err != nil {
		t.Fatalf("handleAdapterError: %v", err)
	}
	if ng.Status.Phase != upgradev1alpha1.NGFailed {
		t.Errorf("expected phase Failed, got %v", ng.Status.Phase)
	}
	if ng.Status.Message == "" {
		t.Errorf("expected a failure message to be recorded")
	}
}

// recordingAdapter behaves like fakeAdapter (BeginBatch no-op, PollBatch
// reports every node it is GIVEN as upgraded) but records which node
// names BeginBatch was actually called with, so a test can assert an
// already-satisfied node was never dispatched at all.
type recordingAdapter struct {
	providerType     upgradev1alpha1.ProviderType
	beginBatchCalled []string
}

func (f *recordingAdapter) Type() upgradev1alpha1.ProviderType { return f.providerType }
func (f *recordingAdapter) SupportsStrategy(s upgradev1alpha1.NodeGroupStrategy) bool {
	return true
}
func (f *recordingAdapter) Precheck(context.Context, provider.UpgradeContext) (bool, string, error) {
	return true, "", nil
}
func (f *recordingAdapter) BeginBatch(_ context.Context, _ provider.UpgradeContext, batch []corev1.Node) error {
	for _, n := range batch {
		f.beginBatchCalled = append(f.beginBatchCalled, n.Name)
	}
	return nil
}
func (f *recordingAdapter) PollBatch(_ context.Context, _ provider.UpgradeContext, batch []corev1.Node) ([]provider.NodeResult, error) {
	results := make([]provider.NodeResult, 0, len(batch))
	for _, n := range batch {
		results = append(results, provider.NodeResult{NodeName: n.Name, Phase: provider.NodePhaseUpgraded})
	}
	return results, nil
}
func (f *recordingAdapter) Verify(context.Context, provider.UpgradeContext) (bool, string, error) {
	return true, "", nil
}

// TestReconcileUpgrading_SkipsNodeAlreadyAtOrPastTarget covers a real bug
// found via real-cluster testing: NodeGroupUpgrade.Spec.TargetVersion is
// one value applied to every member node, but a group's target is
// computed from its OLDEST member (see upgrade.GroupCurrentVersion) - so
// a different, already-ahead node in the same group would otherwise get
// dispatched a Job trying to install an OLDER version over a newer one.
func TestReconcileUpgrading_SkipsNodeAlreadyAtOrPastTarget(t *testing.T) {
	const providerType = upgradev1alpha1.ProviderKubeadm

	alreadyDone := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "already-done"},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.31.9"}},
	}
	needsUpgrade := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "needs-upgrade"},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.30.14"}},
	}

	started := metav1.Now()
	ng := &upgradev1alpha1.NodeGroupUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ng-1", Namespace: "default"},
		Spec: upgradev1alpha1.NodeGroupUpgradeSpec{
			Provider:      providerType,
			TargetVersion: "v1.31.9",
		},
		Status: upgradev1alpha1.NodeGroupUpgradeStatus{
			Phase: upgradev1alpha1.NGUpgrading,
			NodeProgress: []upgradev1alpha1.NodeProgress{
				{Name: alreadyDone.Name, Phase: "Pending", StartedAt: &started},
				{Name: needsUpgrade.Name, Phase: "Pending", StartedAt: &started},
			},
		},
	}

	scheme := runtime.NewScheme()
	if err := upgradev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&upgradev1alpha1.NodeGroupUpgrade{}).
		WithObjects(ng, alreadyDone, needsUpgrade).
		Build()

	adapter := &recordingAdapter{providerType: providerType}
	registry := provider.NewRegistry()
	registry.Register(adapter)

	r := &NodeGroupUpgradeReconciler{Client: c, Adapters: registry}

	if _, err := r.reconcileUpgrading(context.Background(), ng); err != nil {
		t.Fatalf("reconcileUpgrading: %v", err)
	}

	if len(adapter.beginBatchCalled) != 1 || adapter.beginBatchCalled[0] != needsUpgrade.Name {
		t.Errorf("expected BeginBatch to be called with only %q, got %v", needsUpgrade.Name, adapter.beginBatchCalled)
	}

	idx := findNodeProgress(ng.Status.NodeProgress, alreadyDone.Name)
	if idx == -1 || ng.Status.NodeProgress[idx].Phase != "Upgraded" {
		t.Errorf("expected %q to be marked Upgraded without a Job, got %+v", alreadyDone.Name, ng.Status.NodeProgress)
	}
}
