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
)

// SetExecutorImage overrides the default executor image.
func SetExecutorImage(image string) {
	ExecutorImage = image
}

// SetExecutorNamespace overrides the namespace executor Jobs run in.
func SetExecutorNamespace(ns string) {
	ExecutorNamespace = ns
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
func buildUpgradeJob(nodeName, targetVersion string, useApply bool) *batchv1.Job {
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

	return &batchv1.Job{
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
							},
							SecurityContext: &corev1.SecurityContext{
								// Deliberately not "privileged: true" -
								// narrowed to exactly what nsenter needs to
								// re-enter the host's namespaces.
								Capabilities: &corev1.Capabilities{
									Add:  []corev1.Capability{"SYS_ADMIN", "SYS_CHROOT"},
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
}

// sanitizeLabelValue guards against a version string exceeding the
// 63-character Kubernetes label-value limit.
func sanitizeLabelValue(v string) string {
	if len(v) > 63 {
		return v[:63]
	}
	return v
}
