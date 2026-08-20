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
	"testing"

	corev1 "k8s.io/api/core/v1"
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

func TestBuildUpgradeJob_HardenedHostAccess(t *testing.T) {
	job := buildUpgradeJob("worker-1", "v1.30.0", false)
	pod := job.Spec.Template.Spec
	container := pod.Containers[0]

	if !pod.HostPID {
		t.Errorf("expected HostPID true (required for nsenter --target 1)")
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Errorf("expected AutomountServiceAccountToken false")
	}
	if pod.ActiveDeadlineSeconds == nil {
		t.Errorf("expected ActiveDeadlineSeconds to be set")
	}
	if pod.ServiceAccountName != executorServiceAccount {
		t.Errorf("expected dedicated executor ServiceAccount, got %q", pod.ServiceAccountName)
	}

	sc := container.SecurityContext
	if sc == nil {
		t.Fatalf("expected a SecurityContext")
	}
	if sc.Privileged != nil && *sc.Privileged {
		t.Errorf("expected privileged to not be set - capabilities should be narrowed instead")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Errorf("expected AllowPrivilegeEscalation false")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Errorf("expected ReadOnlyRootFilesystem true")
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("expected capabilities to drop ALL, got %+v", sc.Capabilities)
	}
	if sc.Capabilities == nil || !containsCapability(sc.Capabilities.Add, "SYS_ADMIN") {
		t.Errorf("expected SYS_ADMIN capability to be added, got %+v", sc.Capabilities)
	}

	if len(container.VolumeMounts) != 0 {
		t.Errorf("expected no volume mounts (nsenter needs no host mount), got %+v", container.VolumeMounts)
	}
	if len(pod.Volumes) != 0 {
		t.Errorf("expected no volumes at all, got %+v", pod.Volumes)
	}
}

func containsCapability(caps []corev1.Capability, want corev1.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

func TestBuildUpgradeJob_ApplyVsNodeCommandSelection(t *testing.T) {
	applyJob := buildUpgradeJob("cp-1", "v1.30.0", true)
	nodeJob := buildUpgradeJob("cp-2", "v1.30.0", false)

	if mode := envValue(applyJob.Spec.Template.Spec.Containers[0], "UPGRADE_MODE"); mode != "apply" {
		t.Errorf("expected THE first control-plane node's Job  to set UPGRADE_MODE=apply, got: %s", mode)
	}
	if mode := envValue(nodeJob.Spec.Template.Spec.Containers[0], "UPGRADE_MODE"); mode != "node" {
		t.Errorf("expected subsequent nodes' Jobs to set UPGRADE_MODE=node, got: %s", mode)
	}
}

func envValue(container corev1.Container, name string) string {
	for _, e := range container.Env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}
