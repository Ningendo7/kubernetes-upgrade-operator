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
	job := buildUpgradeJob("worker-1", "v1.30.0", false, pinnedChecksums{})
	if job.Spec.Template.Spec.NodeName != "worker-1" {
		t.Fatalf("expected pod to be pinned via spec.nodeName, got %q", job.Spec.Template.Spec.NodeName)
	}
}

func TestBuildUpgradeJob_Deterministic(t *testing.T) {
	a := buildUpgradeJob("worker-1", "v1.30.0", false, pinnedChecksums{})
	b := buildUpgradeJob("worker-1", "v1.30.0", false, pinnedChecksums{})
	if a.Name != b.Name {
		t.Fatalf("expected the same (node, version) to produce the same Job name, got %q and %q", a.Name, b.Name)
	}

	c := buildUpgradeJob("worker-2", "v1.30.0", false, pinnedChecksums{})
	if a.Name == c.Name {
		t.Fatalf("expected different nodes to produce different Job names")
	}
}

func TestBuildUpgradeJob_HardenedHostAccess(t *testing.T) {
	job := buildUpgradeJob("worker-1", "v1.30.0", false, pinnedChecksums{})
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
	if sc.Capabilities == nil || !containsCapability(sc.Capabilities.Add, "SYS_PTRACE") {
		t.Errorf("expected SYS_PTRACE capability to be added (required to open /proc/1/ns/* before setns), got %+v", sc.Capabilities)
	}

	const wantAppArmorAnnotation = "container.apparmor.security.beta.kubernetes.io/kubeadm-upgrade"
	if got := job.Spec.Template.ObjectMeta.Annotations[wantAppArmorAnnotation]; got != "unconfined" {
		t.Errorf("expected pod template annotation %q to be %q (containerd's default AppArmor profile denies ptrace regardless of capabilities), got %q", wantAppArmorAnnotation, "unconfined", got)
	}

	if len(container.VolumeMounts) != 0 {
		t.Errorf("expected no volume mounts (nsenter needs no host mount), got %+v", container.VolumeMounts)
	}
	if len(pod.Volumes) != 0 {
		t.Errorf("expected no volumes at all, got %+v", pod.Volumes)
	}
}

// TestBuildUpgradeJob_ContainerdSocketGID covers a real bug found via
// real-cluster testing: some hosts run containerd with its CRI socket
// owned by a non-root group, which this capability-limited (no
// CAP_DAC_OVERRIDE) process cannot connect to via UID 0 alone. When
// configured, the pod-level SupplementalGroups must carry that GID.
func TestBuildUpgradeJob_ContainerdSocketGID(t *testing.T) {
	t.Cleanup(func() { ContainerdSocketGID = nil })

	t.Run("unset by default", func(t *testing.T) {
		ContainerdSocketGID = nil
		job := buildUpgradeJob("worker-1", "v1.30.0", false, pinnedChecksums{})
		if job.Spec.Template.Spec.SecurityContext != nil {
			t.Errorf("expected no pod-level SecurityContext when unset, got %+v", job.Spec.Template.Spec.SecurityContext)
		}
	})

	t.Run("added as a supplemental group when configured", func(t *testing.T) {
		SetContainerdSocketGID(1000)
		job := buildUpgradeJob("worker-1", "v1.30.0", false, pinnedChecksums{})
		sc := job.Spec.Template.Spec.SecurityContext
		if sc == nil || len(sc.SupplementalGroups) != 1 || sc.SupplementalGroups[0] != 1000 {
			t.Errorf("expected SupplementalGroups [1000], got %+v", sc)
		}
	})
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
	applyJob := buildUpgradeJob("cp-1", "v1.30.0", true, pinnedChecksums{})
	nodeJob := buildUpgradeJob("cp-2", "v1.30.0", false, pinnedChecksums{})

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
