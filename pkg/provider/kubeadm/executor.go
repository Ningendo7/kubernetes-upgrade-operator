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
	"fmt"
	"hash/fnv"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	// ExecutorImage is the container image carrying the kubeadm/kubelet
	// upgrade script. Configurable via SetExecutorImage (cmd/main.go wires
	// this from an env var/flag once the image exists).
	ExecutorImage = "ghcr.io/ningendo7/kubernetes-upgrade-operator-executor:latest"

	// ExecutorNamespace is where executor Jobs are created. This is a
	// dedicated namespace, separate from the manager's own - it runs at
	// the "privileged" Pod Security Standard (see config/executor/), while
	// the manager's namespace stays "restricted". Configurable via
	// SetExecutorNamespace.
	ExecutorNamespace = "kubernetes-upgrade-operator-executor"

	// ContainerdSocketGID, when set (non-nil), is added as a supplementary
	// group on the executor Job's pod. Some hosts run containerd with its
	// CRI socket owned by a non-root group (e.g. a template that starts
	// containerd as an unprivileged user rather than root:root) - kubeadm
	// itself needs to connect to that socket directly during "upgrade
	// apply" (to prepull images), and this process's capabilities are
	// deliberately narrowed to exactly SYS_ADMIN/SYS_CHROOT/SYS_PTRACE, not
	// CAP_DAC_OVERRIDE, so it cannot bypass the socket's normal permission
	// bits. Matching its group via SupplementalGroups grants exactly the
	// access needed through ordinary Unix permissions, rather than
	// widening capabilities cluster-node-wide. Unset by default: this is a
	// host-specific accommodation, not a general requirement.
	ContainerdSocketGID *int64

	// AllowUnpinnedChecksums, when true, lets an upgrade proceed for a
	// version that has no entry in pkg/checksums - the executor script
	// then falls back to verifying against a checksum fetched alongside
	// the binary from the same source, which is only an integrity check,
	// not an authenticity one (see SECURITY.md). Off by default: an
	// unpinned version is a hard failure, so adding support for a new
	// version is a deliberate, reviewed change to the pinned table.
	// Intended only for air-gapped setups pointing the *_BASE_URL vars at
	// a mirror they have made their own trust decision about.
	AllowUnpinnedChecksums = false
)

// SetExecutorImage overrides the default executor image.
func SetExecutorImage(image string) {
	ExecutorImage = image
}

// SetExecutorNamespace overrides the namespace executor Jobs run in.
func SetExecutorNamespace(ns string) {
	ExecutorNamespace = ns
}

// SetContainerdSocketGID configures the supplementary group added to the
// executor Job's pod so it can connect to a non-root-owned containerd
// socket. See the ContainerdSocketGID doc comment for why this exists.
func SetContainerdSocketGID(gid int64) {
	ContainerdSocketGID = &gid
}

// SetAllowUnpinnedChecksums toggles the fail-closed behaviour for a
// version missing from the pinned checksum table. See the
// AllowUnpinnedChecksums doc comment.
func SetAllowUnpinnedChecksums(allow bool) {
	AllowUnpinnedChecksums = allow
}

func boolEnv(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

const (
	// executorServiceAccount is a dedicated, least-privilege ServiceAccount
	// for executor Jobs. Deliberately NOT the manager's own ServiceAccount:
	// this Job is root-equivalent on the target node and must not inherit
	// the manager's broader Kubernetes API permissions.
	executorServiceAccount = "kubernetes-upgrade-operator-executor"

	nodeNameLabel  = "upgrade.k8s-upgrade-operator/node"
	targetVerLabel = "upgrade.k8s-upgrade-operator/target-version"
)

// jobNameFor deterministically names the executor Job for a given node and
// target version, so retrying or re-reconciling resolves to the same Job
// instead of creating a duplicate. Kubernetes Job names must fit the
// 63-character label-value limit (the Job controller stamps its pods with
// a job-name label), so this hashes rather than concatenating the raw node
// name, which could be much longer.
func jobNameFor(nodeName, targetVersion string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(nodeName + "@" + targetVersion))
	return fmt.Sprintf("kuo-upgrade-%x", h.Sum32())
}

// buildUpgradeJob constructs the privileged, node-pinned Job that performs
// an in-place kubeadm/kubelet upgrade on nodeName. useApply selects
// "kubeadm upgrade apply" (run exactly once, on the first control-plane
// node upgraded for a given hop) vs "kubeadm upgrade node" (every other
// control-plane node, and all workers).
// pinnedChecksums carries the SHA-256 values the executor script verifies
// its fetches against. Zero values mean "not pinned" - only valid when
// AllowUnpinnedChecksums is set, in which case the script falls back to a
// fetch-alongside checksum.
type pinnedChecksums struct {
	kubeadm    string
	kubeletDeb string
}

func buildUpgradeJob(nodeName, targetVersion string, useApply bool, sums pinnedChecksums) *batchv1.Job {
	backoffLimit := int32(2)
	ttl := int32(600)
	activeDeadline := int64(900)
	allowPrivilegeEscalation := false
	readOnlyRootFS := true
	automountToken := false

	upgradeMode := "node"
	if useApply {
		upgradeMode = "apply"
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobNameFor(nodeName, targetVersion),
			Namespace: ExecutorNamespace,
			Labels: map[string]string{
				nodeNameLabel:  nodeName,
				targetVerLabel: sanitizeLabelValue(targetVersion),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						nodeNameLabel: nodeName,
					},
					Annotations: map[string]string{
						// containerd's default AppArmor profile
						// (cri-containerd.apparmor.d) denies ptrace
						// regardless of Linux capabilities - AppArmor
						// mediation is enforced independently of
						// CAP_SYS_PTRACE. Without this, nsenter cannot
						// even open /proc/1/ns/* to re-enter the host's
						// namespaces. Scoped to this one container only,
						// not a cluster-wide default change.
						"container.apparmor.security.beta.kubernetes.io/kubeadm-upgrade": "unconfined",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           executorServiceAccount,
					AutomountServiceAccountToken: &automountToken,
					RestartPolicy:                corev1.RestartPolicyNever,
					// Setting NodeName directly (instead of a nodeSelector)
					// bypasses the scheduler entirely, so this Job can still
					// land on a node that's already been cordoned.
					NodeName:              nodeName,
					HostPID:               true,
					ActiveDeadlineSeconds: &activeDeadline,
					// Guards against a NoExecute taint (e.g. a health
					// condition) evicting this Job mid-run; NodeName
					// bypasses the scheduler but the kubelet's taint
					// eviction manager still acts on already-bound pods.
					Tolerations: []corev1.Toleration{
						{
							Operator: corev1.TolerationOpExists,
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "kubeadm-upgrade",
							Image: ExecutorImage,
							Env: []corev1.EnvVar{
								{
									Name:  "TARGET_VERSION",
									Value: targetVersion,
								},
								{
									Name:  "UPGRADE_MODE",
									Value: upgradeMode,
								},
								{
									Name:  "KUBEADM_SHA256",
									Value: sums.kubeadm,
								},
								{
									Name:  "KUBELET_DEB_SHA256",
									Value: sums.kubeletDeb,
								},
								{
									Name:  "ALLOW_UNPINNED_CHECKSUMS",
									Value: boolEnv(AllowUnpinnedChecksums),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								// Deliberately not "privileged: true" -
								// narrowed to exactly what nsenter needs to
								// re-enter the host's namespaces.
								Capabilities: &corev1.Capabilities{
									// SYS_PTRACE is required just to *open* another
									// process's /proc/<pid>/ns/* handles (ptrace_may_access);
									// SYS_ADMIN is separately required to actually setns()
									// into them once open. Both are needed for nsenter to
									// re-enter the host's namespaces.
									Add:  []corev1.Capability{"SYS_ADMIN", "SYS_CHROOT", "SYS_PTRACE"},
									Drop: []corev1.Capability{"ALL"},
								},
								AllowPrivilegeEscalation: &allowPrivilegeEscalation,
								ReadOnlyRootFilesystem:   &readOnlyRootFS,
							},
						},
					},
				},
			},
		},
	}

	if ContainerdSocketGID != nil {
		job.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			SupplementalGroups: []int64{*ContainerdSocketGID},
		}
	}

	return job
}

// sanitizeLabelValue guards against a version string exceeding the
// 63-character Kubernetes label-value limit.
func sanitizeLabelValue(v string) string {
	if len(v) > 63 {
		return v[:63]
	}
	return v
}
