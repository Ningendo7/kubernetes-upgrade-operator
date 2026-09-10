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

package k8sutil

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NodeNameIndexField is the field index key that must be registered via
// mgr.GetFieldIndexer().IndexField(ctx, &corev1.Pod{}, NodeNameIndexField, ...)
// before DrainNode can list pods by node.
const NodeNameIndexField = "spec.nodeName"

// DrainOptions controls how DrainNode evicts pods from a node.
type DrainOptions struct {
	GracePeriodSeconds *int64
	IgnoreDaemonSets   bool
	DeleteEmptyDirData bool
}

// PodDrainState is one pod's explicit state within a drain pass. Every pod
// DrainNode considers gets exactly one of these - there's no gap for a
// caller to have to infer.
type PodDrainState string

const (
	// PodDrainEvicting means an eviction request was just issued (or was
	// already in progress from a previous pass) - the pod is expected to
	// terminate; once it actually does, it simply won't appear in a
	// future DrainNode call anymore.
	PodDrainEvicting PodDrainState = "Evicting"
	// PodDrainBlocked means eviction could not be attempted, or was
	// rejected, this pass - see Reason for why (a PodDisruptionBudget, an
	// emptyDir policy, or a genuine API error).
	PodDrainBlocked PodDrainState = "Blocked"
	// PodDrainSkipped means this pod isn't DrainNode's concern at all
	// (DaemonSet-owned, a static/mirror pod, or already terminal) and was
	// deliberately left alone. Skipped pods never count toward Remaining.
	PodDrainSkipped PodDrainState = "Skipped"
)

// PodStatus is one pod's explicit state as of this drain pass.
type PodStatus struct {
	Namespace string
	Name      string
	State     PodDrainState
	Reason    string
}

// DrainResult reports drain progress for a single pass, as an explicit
// per-pod state list rather than a count plus a partial explanation -
// every pod DrainNode saw is in here with a reason, nothing is left for
// the caller to infer.
type DrainResult struct {
	Pods []PodStatus
}

// Remaining is how many pods still need to leave the node before it's safe
// to consider the node drained - every pod not Skipped. Computed from Pods
// rather than tracked separately, so it can never drift out of sync with
// the per-pod detail.
func (r DrainResult) Remaining() int {
	n := 0
	for _, p := range r.Pods {
		if p.State != PodDrainSkipped {
			n++
		}
	}
	return n
}

// Blocked returns the pods currently blocking drain, with their reasons.
func (r DrainResult) Blocked() []PodStatus {
	var blocked []PodStatus
	for _, p := range r.Pods {
		if p.State == PodDrainBlocked {
			blocked = append(blocked, p)
		}
	}
	return blocked
}

// DrainNode attempts to evict every evictable pod from nodeName. It is
// safe to call repeatedly: pods already terminating are reported as
// Evicting rather than re-attempted, and pods blocked by a
// PodDisruptionBudget are reported as Blocked rather than treated as a
// hard failure, so callers can requeue and retry.
func DrainNode(ctx context.Context, c client.Client, nodeName string, opts DrainOptions) (DrainResult, error) {
	var pods corev1.PodList
	if err := c.List(ctx, &pods, client.MatchingFields{NodeNameIndexField: nodeName}); err != nil {
		return DrainResult{}, fmt.Errorf("listing pods on node %q: %w", nodeName, err)
	}

	result := DrainResult{}
	var errs []error

	for i := range pods.Items {
		pod := &pods.Items[i]

		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			result.Pods = append(result.Pods, skipped(pod, "pod has already terminated"))
			continue
		}
		if isMirrorPod(pod) {
			result.Pods = append(result.Pods, skipped(pod, "static/mirror pod, managed directly by kubelet"))
			continue
		}
		if opts.IgnoreDaemonSets && isDaemonSetPod(pod) {
			result.Pods = append(result.Pods, skipped(pod, "DaemonSet-owned pod, ignored per drain policy"))
			continue
		}

		if !opts.DeleteEmptyDirData && usesEmptyDir(pod) {
			result.Pods = append(result.Pods, blocked(pod, "pod uses emptyDir volumes; set drain.deleteEmptyDirData to allow eviction"))
			continue
		}

		if pod.DeletionTimestamp != nil {
			result.Pods = append(result.Pods, PodStatus{
				Namespace: pod.Namespace, Name: pod.Name,
				State: PodDrainEvicting, Reason: "eviction already in progress",
			})
			continue
		}

		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
			DeleteOptions: &metav1.DeleteOptions{
				GracePeriodSeconds: opts.GracePeriodSeconds,
			},
		}

		if err := c.SubResource("eviction").Create(ctx, pod, eviction); err != nil {
			switch {
			case apierrors.IsTooManyRequests(err):
				result.Pods = append(result.Pods, blocked(pod, fmt.Sprintf("eviction blocked, likely by a PodDisruptionBudget: %v", err)))
			case apierrors.IsNotFound(err):
				// Already gone - not this node's concern anymore.
			default:
				errs = append(errs, fmt.Errorf("evicting pod %s/%s: %w", pod.Namespace, pod.Name, err))
				result.Pods = append(result.Pods, blocked(pod, fmt.Sprintf("eviction attempt errored: %v", err)))
			}
			continue
		}

		result.Pods = append(result.Pods, PodStatus{
			Namespace: pod.Namespace, Name: pod.Name,
			State: PodDrainEvicting, Reason: "eviction just requested",
		})
	}

	return result, errors.Join(errs...)
}

func skipped(pod *corev1.Pod, reason string) PodStatus {
	return PodStatus{Namespace: pod.Namespace, Name: pod.Name, State: PodDrainSkipped, Reason: reason}
}

func blocked(pod *corev1.Pod, reason string) PodStatus {
	return PodStatus{Namespace: pod.Namespace, Name: pod.Name, State: PodDrainBlocked, Reason: reason}
}

func isMirrorPod(pod *corev1.Pod) bool {
	_, ok := pod.Annotations["kubernetes.io/config.mirror"]
	return ok
}

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func usesEmptyDir(pod *corev1.Pod) bool {
	for _, vol := range pod.Spec.Volumes {
		if vol.EmptyDir != nil {
			return true
		}
	}
	return false
}
