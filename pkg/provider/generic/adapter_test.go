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

package generic

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider/kubeadm"
)

func newGenericTestClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestAdapter_SupportsBothStrategies(t *testing.T) {
	a := &Adapter{inPlace: &kubeadm.Adapter{}}
	if !a.SupportsStrategy(upgradev1alpha1.StrategyInPlace) {
		t.Errorf("expected Generic to support InPlace")
	}
	if !a.SupportsStrategy(upgradev1alpha1.StrategyReplace) {
		t.Errorf("expected Generic to support Replace")
	}
}

func TestAdapter_Replace_DeletesNodeThenReportsUpgradedOnceGone(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}
	c := newGenericTestClient(node)
	a := &Adapter{inPlace: &kubeadm.Adapter{}}
	group := &upgradev1alpha1.NodeGroupUpgrade{Spec: upgradev1alpha1.NodeGroupUpgradeSpec{Strategy: upgradev1alpha1.StrategyReplace}}
	uc := provider.UpgradeContext{Client: c, Group: group, TargetVersion: "v1.30.0"}
	batch := []corev1.Node{*node}

	if err := a.BeginBatch(context.Background(), uc, batch); err != nil {
		t.Fatalf("BeginBatch: %v", err)
	}

	results, err := a.PollBatch(context.Background(), uc, batch)
	if err != nil {
		t.Fatalf("PollBatch: %v", err)
	}
	if len(results) != 1 || results[0].Phase != provider.NodePhaseUpgraded {
		t.Fatalf("got %+v, want a single Upgraded result", results)
	}
}

func TestAdapter_Replace_StillInProgressBeforeDeletion(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}
	c := newGenericTestClient(node)
	a := &Adapter{inPlace: &kubeadm.Adapter{}}
	group := &upgradev1alpha1.NodeGroupUpgrade{Spec: upgradev1alpha1.NodeGroupUpgradeSpec{Strategy: upgradev1alpha1.StrategyReplace}}
	uc := provider.UpgradeContext{Client: c, Group: group, TargetVersion: "v1.30.0"}
	batch := []corev1.Node{*node}

	// Poll without ever calling BeginBatch: node still exists.
	results, err := a.PollBatch(context.Background(), uc, batch)
	if err != nil {
		t.Fatalf("PollBatch: %v", err)
	}
	if len(results) != 1 || results[0].Phase != provider.NodePhaseInProgress {
		t.Fatalf("got %+v, want a single InProgress result", results)
	}
}

func TestAdapter_InPlace_DelegatesToKubeadmExecutor(t *testing.T) {
	c := newGenericTestClient()
	a := &Adapter{inPlace: &kubeadm.Adapter{}}
	group := &upgradev1alpha1.NodeGroupUpgrade{Spec: upgradev1alpha1.NodeGroupUpgradeSpec{Strategy: upgradev1alpha1.StrategyInPlace, Role: upgradev1alpha1.RoleWorker}}
	uc := provider.UpgradeContext{Client: c, Group: group, TargetVersion: "v1.30.0"}
	batch := []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}}

	if err := a.BeginBatch(context.Background(), uc, batch); err != nil {
		t.Fatalf("BeginBatch: %v", err)
	}

	var jobs batchv1.JobList
	if err := c.List(context.Background(), &jobs); err != nil {
		t.Fatalf("listing jobs: %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("expected InPlace BeginBatch to delegate to the kubeadm executor and create 1 Job, got %d", len(jobs.Items))
	}
}
