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
	"strings"
	"testing"
)

func TestBuildUpgradeJob_NodeNameSetDirectlyBypassesCordon(t *testing.T) {
	job := buildUpgradeJob("worker-1", "v1.30.0", false)
	if job.Spec.Template.Spec.NodeName != "worker-1" {
		t.Fatalf("expected pod to be pinned via spec.nodeName, got %q", job.Spec.Template.Spec.NodeName)
	}
}

func TestBuildUpgradeJob_Deterministic(t *testing.T) {
	a := buildUpgradeJob("worker-1", "v1.30.0", false)
	b := buildUpgradeJob("worker-1", "v1.30.0", false)
	if a.Name != b.Name {
		t.Fatalf("expected the same (node, version) to produce the same Job name, got %q and %q", a.Name, b.Name)
	}

	c := buildUpgradeJob("worker-2", "v1.30.0", false)
	if a.Name == c.Name {
		t.Fatalf("expected different nodes to produce different Job names")
	}
}

func TestBuildUpgradeJob_PrivilegedHostAccess(t *testing.T) {
	job := buildUpgradeJob("worker-1", "v1.30.0", false)
	container := job.Spec.Template.Spec.Containers[0]

	if !job.Spec.Template.Spec.HostPID {
		t.Errorf("expected HostPID true (required for nsenter --target 1)")
	}
	if container.SecurityContext == nil || container.SecurityContext.Privileged == nil || !*container.SecurityContext.Privileged {
		t.Errorf("expected container to be privileged")
	}
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].MountPath != "/host" {
		t.Errorf("expected a single /host volume mount, got %+v", container.VolumeMounts)
	}
	if job.Spec.Template.Spec.ServiceAccountName != executorServiceAccount {
		t.Errorf("expected dedicated executor ServiceAccount, got %q", job.Spec.Template.Spec.ServiceAccountName)
	}
}

func TestBuildUpgradeJob_ApplyVsNodeCommandSelection(t *testing.T) {
	applyJob := buildUpgradeJob("cp-1", "v1.30.0", true)
	nodeJob := buildUpgradeJob("cp-2", "v1.30.0", false)

	applyScript := strings.Join(applyJob.Spec.Template.Spec.Containers[0].Args, " ")
	nodeScript := strings.Join(nodeJob.Spec.Template.Spec.Containers[0].Args, " ")

	if !strings.Contains(applyScript, "kubeadm upgrade apply") {
		t.Errorf("expected the first control-plane node's Job to run 'kubeadm upgrade apply', got: %s", applyScript)
	}
	if strings.Contains(nodeScript, "kubeadm upgrade apply") || !strings.Contains(nodeScript, "kubeadm upgrade node") {
		t.Errorf("expected subsequent nodes' Jobs to run 'kubeadm upgrade node', got: %s", nodeScript)
	}
}
