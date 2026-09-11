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
	"fmt"
	"hash/fnv"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/checksums"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
	obs "github.com/Ningendo7/kubernetes-upgrade-operator/pkg/observability"
)

// etcdctlCheckJobName deterministically names the per-node etcdctl health
// check Job, the same way jobNameFor does for upgrade Jobs.
func etcdctlCheckJobName(nodeName string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte("etcdctl-health@" + nodeName))
	return fmt.Sprintf("kuo-etcdctl-%x", h.Sum32())
}

// buildEtcdctlHealthCheckJob constructs the privileged, node-pinned Job
// that runs a real etcdctl health check against nodeName's own local etcd
// member. Reuses the same executor image, namespace, and ServiceAccount
// as the upgrade Job (see executor.go). This check needs no CRI/apt
// access at all, only the hosts etcd client certs and network, but
// nsenter --mount itself needs SYS_CHROOT in addition to SYS_ADMIN - its
// mount-namespace reassociation invokes a chroot-adjacent syscall to keep
// the calling processs working directory sane, confirmed empirically
// (dropping it produces "reassociate to namespace ns/mnt failed:
// Operation not permitted" even with SYS_ADMIN present) - so this only
// narrows the namespace set (mount/net/pid, no uts/ipc), not capabilities.
func buildEtcdctlHealthCheckJob(nodeName, etcdVersion, etcdctlSHA string) *batchv1.Job {
	backoffLimit := int32(1)
	// Deliberately shorter than the upgrade job's TTL: this check runs
	// repeatedly (created fresh, deleted, and recreated on every retry
	// while unhealthy) rather than once per node per hop, so a stale
	// completed/failed Job should not linger anywhere near as long.
	ttl := int32(30)
	activeDeadline := int64(60)
	allowPrivilegeEscalation := false
	readOnlyRootFS := true
	automountToken := false

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      etcdctlCheckJobName(nodeName),
			Namespace: ExecutorNamespace,
			Labels: map[string]string{
				nodeNameLabel: nodeName,
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
						// Same reasoning as the upgrade Job: containerd's
						// default AppArmor profile denies ptrace
						// regardless of Linux capabilities, and nsenter
						// needs to open /proc/1/ns/* before it can
						// re-enter the hosts namespaces at all.
						"container.apparmor.security.beta.kubernetes.io/etcdctl-check": "unconfined",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           executorServiceAccount,
					AutomountServiceAccountToken: &automountToken,
					RestartPolicy:                corev1.RestartPolicyNever,
					NodeName:                     nodeName,
					HostPID:                      true,
					ActiveDeadlineSeconds:        &activeDeadline,
					Tolerations: []corev1.Toleration{
						{
							Operator: corev1.TolerationOpExists,
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "etcdctl-check",
							Image:   ExecutorImage,
							Command: []string{"/usr/local/bin/etcd-healthcheck.sh"},
							Env: []corev1.EnvVar{
								{
									Name:  "ETCD_VERSION",
									Value: etcdVersion,
								},
								{
									Name:  "ETCDCTL_SHA256",
									Value: etcdctlSHA,
								},
								{
									Name:  "ALLOW_UNPINNED_CHECKSUMS",
									Value: boolEnv(AllowUnpinnedChecksums),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Capabilities: &corev1.Capabilities{
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
}

// pinnedEtcdctlSHA resolves the pinned etcdctl tarball checksum for the
// running etcd version on the given node's architecture. Same fail-closed
// contract as pinnedSumsFor.
func pinnedEtcdctlSHA(etcdVersion string, node corev1.Node) (string, error) {
	set, err := checksums.LookupEtcd(etcdVersion, node.Status.NodeInfo.Architecture)
	if err != nil {
		if AllowUnpinnedChecksums {
			return "", nil
		}
		return "", fmt.Errorf("refusing to run the etcd health check on node %q: %w", node.Name, err)
	}
	return set.EtcdctlTarball, nil
}

// CheckEtcdQuorumViaEtcdctl runs a real etcdctl health check against
// EACH control-plane nodes own local etcd member independently (never a
// single nodes --cluster cross-discovery, which would make the whole
// determination only as reliable as whichever one node answered it) and
// computes majority here, mirroring k8sutil.CheckEtcdQuorumViaAPIServers's
// arithmetic exactly. Each nodes check runs as its own short-lived Job;
// done is false while any of them is still pending or running, in which
// case the caller should retry later rather than treat this as a result.
func CheckEtcdQuorumViaEtcdctl(ctx context.Context, c client.Client, cpNodes []corev1.Node, etcdVersion string) (done bool, status k8sutil.EtcdQuorumStatus, err error) {
	if len(cpNodes) == 0 {
		return true, k8sutil.EtcdQuorumStatus{Healthy: true, Reason: "no control-plane nodes to check"}, nil
	}

	healthy := 0
	pending := 0
	total := len(cpNodes)

	for _, node := range cpNodes {
		var job batchv1.Job
		key := client.ObjectKey{Namespace: ExecutorNamespace, Name: etcdctlCheckJobName(node.Name)}
		getErr := c.Get(ctx, key, &job)
		switch {
		case apierrors.IsNotFound(getErr):
			etcdctlSHA, shaErr := pinnedEtcdctlSHA(etcdVersion, node)
			if shaErr != nil {
				return false, k8sutil.EtcdQuorumStatus{}, shaErr
			}
			newJob := buildEtcdctlHealthCheckJob(node.Name, etcdVersion, etcdctlSHA)
			if createErr := c.Create(ctx, newJob); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
				return false, k8sutil.EtcdQuorumStatus{}, fmt.Errorf("creating etcdctl health check job for node %q: %w", node.Name, createErr)
			}
			pending++
			continue
		case getErr != nil:
			return false, k8sutil.EtcdQuorumStatus{}, fmt.Errorf("getting etcdctl health check job for node %q: %w", node.Name, getErr)
		}

		// Each Job's terminal state is observed exactly once here - the
		// Delete right after means the next pass sees NotFound and starts
		// a fresh one - so incrementing the counter here does not double
		// count the way polling an undeleted Job would.
		switch jobConditionState(&job) {
		case jobComplete:
			healthy++
			obs.ExecutorJobTotal.WithLabelValues("Kubeadm", "etcd_healthcheck", "succeeded").Inc()
			_ = c.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground))
		case jobFailed:
			obs.ExecutorJobTotal.WithLabelValues("Kubeadm", "etcd_healthcheck", "failed").Inc()
			_ = c.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground))
		default:
			pending++
		}
	}

	if pending > 0 {
		return false, k8sutil.EtcdQuorumStatus{}, nil
	}

	quorum := total/2 + 1
	result := k8sutil.EtcdQuorumStatus{TotalMembers: total, HealthyMembers: healthy}
	if healthy >= quorum {
		result.Healthy = true
		return true, result, nil
	}
	result.Reason = fmt.Sprintf("only %d/%d control-plane etcd members report healthy via etcdctl, need at least %d", healthy, total, quorum)
	return true, result, nil
}

type jobState int

const (
	jobRunning jobState = iota
	jobComplete
	jobFailed
)

func jobConditionState(job *batchv1.Job) jobState {
	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		switch cond.Type {
		case batchv1.JobComplete:
			return jobComplete
		case batchv1.JobFailed:
			return jobFailed
		}
	}
	return jobRunning
}
