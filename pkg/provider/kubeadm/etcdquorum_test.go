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
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestBuildEtcdctlHealthCheckJob_HardenedAndNarrower(t *testing.T) {
	job := buildEtcdctlHealthCheckJob("cp-1", "v3.5.24")
	pod := job.Spec.Template.Spec
	container := pod.Containers[0]

	if !pod.HostPID {
		t.Errorf("expected HostPID true (required for nsenter --target 1)")
	}
	if pod.NodeName != "cp-1" {
		t.Errorf("expected NodeName cp-1, got %q", pod.NodeName)
	}
	if container.Command[0] != "/usr/local/bin/etcd-healthcheck.sh" {
		t.Errorf("expected the etcd-healthcheck.sh entrypoint override, got %v", container.Command)
	}

	sc := container.SecurityContext
	if sc == nil {
		t.Fatalf("expected a SecurityContext")
	}
	// SYS_CHROOT is required too, confirmed empirically against a real
	// cluster: nsenter --mount fails to reassociate the mount namespace
	// without it, even with SYS_ADMIN present. Only the namespace set is
	// narrower than the upgrade job (mount/net/pid, no uts/ipc), not
	// the capability set.
	if !containsCapability(sc.Capabilities.Add, "SYS_ADMIN") || !containsCapability(sc.Capabilities.Add, "SYS_CHROOT") || !containsCapability(sc.Capabilities.Add, "SYS_PTRACE") {
		t.Errorf("expected SYS_ADMIN, SYS_CHROOT, and SYS_PTRACE, got %+v", sc.Capabilities.Add)
	}

	const wantAppArmorAnnotation = "container.apparmor.security.beta.kubernetes.io/etcdctl-check"
	if got := job.Spec.Template.ObjectMeta.Annotations[wantAppArmorAnnotation]; got != "unconfined" {
		t.Errorf("expected pod template annotation %q to be unconfined, got %q", wantAppArmorAnnotation, got)
	}

	var etcdVersionSet bool
	for _, env := range container.Env {
		if env.Name == "ETCD_VERSION" && env.Value == "v3.5.24" {
			etcdVersionSet = true
		}
	}
	if !etcdVersionSet {
		t.Errorf("expected ETCD_VERSION=v3.5.24 env var, got %+v", container.Env)
	}
}

func TestCheckEtcdQuorumViaEtcdctl_NoNodesTriviallyHealthy(t *testing.T) {
	c := newAdapterTestClient()
	done, status, err := CheckEtcdQuorumViaEtcdctl(context.Background(), c, nil, "v3.5.24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done || !status.Healthy {
		t.Errorf("expected done=true healthy=true for no nodes, got done=%v status=%+v", done, status)
	}
}

func TestCheckEtcdQuorumViaEtcdctl_CreatesJobsAndWaits(t *testing.T) {
	c := newAdapterTestClient()
	nodes := []corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "cp-1"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "cp-2"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "cp-3"}},
	}

	done, _, err := CheckEtcdQuorumViaEtcdctl(context.Background(), c, nodes, "v3.5.24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if done {
		t.Errorf("expected done=false on the first pass - jobs were just created, not resolved yet")
	}

	for _, n := range nodes {
		var job batchv1.Job
		key := client.ObjectKey{Namespace: ExecutorNamespace, Name: etcdctlCheckJobName(n.Name)}
		if err := c.Get(context.Background(), key, &job); err != nil {
			t.Errorf("expected a job to have been created for node %q: %v", n.Name, err)
		}
	}
}

func TestCheckEtcdQuorumViaEtcdctl_MajorityArithmetic(t *testing.T) {
	tests := []struct {
		name         string
		healthyCount int
		failedCount  int
		wantHealthy  bool
	}{
		{name: "3/3 healthy", healthyCount: 3, failedCount: 0, wantHealthy: true},
		{name: "2/3 healthy: majority holds", healthyCount: 2, failedCount: 1, wantHealthy: true},
		{name: "1/3 healthy: no quorum", healthyCount: 1, failedCount: 2, wantHealthy: false},
		{name: "0/3 healthy: no quorum", healthyCount: 0, failedCount: 3, wantHealthy: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objs []objectWithJobState
			for i := 0; i < tt.healthyCount; i++ {
				objs = append(objs, objectWithJobState{name: nodeName(i), complete: true})
			}
			for i := tt.healthyCount; i < tt.healthyCount+tt.failedCount; i++ {
				objs = append(objs, objectWithJobState{name: nodeName(i), complete: false})
			}

			c := newAdapterTestClient()
			var nodes []corev1.Node
			for _, o := range objs {
				nodes = append(nodes, corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: o.name}})
				job := &batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: etcdctlCheckJobName(o.name), Namespace: ExecutorNamespace},
				}
				if o.complete {
					job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
				} else {
					job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}
				}
				if err := c.Create(context.Background(), job); err != nil {
					t.Fatalf("seeding job: %v", err)
				}
			}

			done, status, err := CheckEtcdQuorumViaEtcdctl(context.Background(), c, nodes, "v3.5.24")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !done {
				t.Fatalf("expected done=true - all jobs already resolved, got status=%+v", status)
			}
			if status.Healthy != tt.wantHealthy {
				t.Errorf("got Healthy=%v, want %v (status=%+v)", status.Healthy, tt.wantHealthy, status)
			}
		})
	}
}

type objectWithJobState struct {
	name     string
	complete bool
}

func nodeName(i int) string {
	names := []string{"cp-1", "cp-2", "cp-3"}
	return names[i]
}
