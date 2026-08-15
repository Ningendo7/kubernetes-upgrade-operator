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

package kubeadm

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

func newAdapterTestClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func jobScript(t *testing.T, c client.Client, nodeName, targetVersion string) string {
	t.Helper()
	var job batchv1.Job
	key := client.ObjectKey{Namespace: ExecutorNamespace, Name: jobNameFor(nodeName, targetVersion)}
	if err := c.Get(context.Background(), key, &job); err != nil {
		t.Fatalf("expected job to exist: %v", err)
	}
	return strings.Join(job.Spec.Template.Spec.Containers[0].Args, " ")
}

func TestAdapter_BeginBatch_FirstControlPlaneNodeUsesApply(t *testing.T) {
	c := newAdapterTestClient()
	a := &Adapter{}
	group := &upgradev1alpha1.NodeGroupUpgrade{Spec: upgradev1alpha1.NodeGroupUpgradeSpec{Role: upgradev1alpha1.RoleControlPlane}}
	uc := provider.UpgradeContext{Client: c, Group: group, TargetVersion: "v1.30.0"}

	err := a.BeginBatch(context.Background(), uc, []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "cp-1"}}})
	if err != nil {
		t.Fatalf("BeginBatch: %v", err)
	}

	if script := jobScript(t, c, "cp-1", "v1.30.0"); !strings.Contains(script, "kubeadm upgrade apply") {
		t.Errorf("expected first control-plane node to use apply, got: %s", script)
	}
}

func TestAdapter_BeginBatch_SubsequentControlPlaneNodeUsesNode(t *testing.T) {
	c := newAdapterTestClient()
	a := &Adapter{}
	completedAt := metav1.Now()
	group := &upgradev1alpha1.NodeGroupUpgrade{
		Spec: upgradev1alpha1.NodeGroupUpgradeSpec{Role: upgradev1alpha1.RoleControlPlane},
		Status: upgradev1alpha1.NodeGroupUpgradeStatus{
			NodeProgress: []upgradev1alpha1.NodeProgress{
				{Name: "cp-1", ToVersion: "v1.30.0", CompletedAt: &completedAt},
			},
		},
	}
	uc := provider.UpgradeContext{Client: c, Group: group, TargetVersion: "v1.30.0"}

	err := a.BeginBatch(context.Background(), uc, []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "cp-2"}}})
	if err != nil {
		t.Fatalf("BeginBatch: %v", err)
	}

	script := jobScript(t, c, "cp-2", "v1.30.0")
	if strings.Contains(script, "kubeadm upgrade apply") || !strings.Contains(script, "kubeadm upgrade node") {
		t.Errorf("expected subsequent control-plane node to use 'upgrade node', got: %s", script)
	}
}

func TestAdapter_BeginBatch_WorkerNeverUsesApply(t *testing.T) {
	c := newAdapterTestClient()
	a := &Adapter{}
	group := &upgradev1alpha1.NodeGroupUpgrade{Spec: upgradev1alpha1.NodeGroupUpgradeSpec{Role: upgradev1alpha1.RoleWorker}}
	uc := provider.UpgradeContext{Client: c, Group: group, TargetVersion: "v1.30.0"}

	err := a.BeginBatch(context.Background(), uc, []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}})
	if err != nil {
		t.Fatalf("BeginBatch: %v", err)
	}

	if script := jobScript(t, c, "worker-1", "v1.30.0"); strings.Contains(script, "kubeadm upgrade apply") {
		t.Errorf("expected worker node to never use apply, got: %s", script)
	}
}

func TestAdapter_BeginBatch_IdempotentOnRetry(t *testing.T) {
	c := newAdapterTestClient()
	a := &Adapter{}
	group := &upgradev1alpha1.NodeGroupUpgrade{Spec: upgradev1alpha1.NodeGroupUpgradeSpec{Role: upgradev1alpha1.RoleWorker}}
	uc := provider.UpgradeContext{Client: c, Group: group, TargetVersion: "v1.30.0"}
	batch := []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}}

	if err := a.BeginBatch(context.Background(), uc, batch); err != nil {
		t.Fatalf("first BeginBatch: %v", err)
	}
	if err := a.BeginBatch(context.Background(), uc, batch); err != nil {
		t.Fatalf("second BeginBatch should be a no-op, got error: %v", err)
	}
}

func TestAdapter_PollBatch_MapsJobConditions(t *testing.T) {
	completeJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobNameFor("done-node", "v1.30.0"), Namespace: ExecutorNamespace},
		Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}},
	}
	failedJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobNameFor("failed-node", "v1.30.0"), Namespace: ExecutorNamespace},
		Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Message: "boom"}}},
	}
	runningJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobNameFor("running-node", "v1.30.0"), Namespace: ExecutorNamespace},
	}

	c := newAdapterTestClient(completeJob, failedJob, runningJob)
	a := &Adapter{}
	uc := provider.UpgradeContext{Client: c, TargetVersion: "v1.30.0"}

	batch := []corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "done-node"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "failed-node"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "running-node"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "missing-node"}},
	}

	results, err := a.PollBatch(context.Background(), uc, batch)
	if err != nil {
		t.Fatalf("PollBatch: %v", err)
	}

	want := map[string]provider.NodePhase{
		"done-node":    provider.NodePhaseUpgraded,
		"failed-node":  provider.NodePhaseFailed,
		"running-node": provider.NodePhaseInProgress,
		"missing-node": provider.NodePhaseFailed,
	}
	for _, r := range results {
		if r.Phase != want[r.NodeName] {
			t.Errorf("node %q: got phase %v, want %v", r.NodeName, r.Phase, want[r.NodeName])
		}
	}
}
