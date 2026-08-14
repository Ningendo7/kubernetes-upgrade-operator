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

// BlockedPod describes a pod that could not be evicted this pass.
type BlockedPod struct {
	Namespace string
	Name      string
	Reason    string
}

// DrainResult reports drain progress for a single pass.
type DrainResult struct {
	// Remaining is how many pods still need to leave the node before it's
	// safe to consider the node drained.
	Remaining int
	// Blocked lists pods that could not be evicted this pass (e.g. a PDB
	// currently disallows it, or DeleteEmptyDirData forbids it).
	Blocked []BlockedPod
}

// DrainNode attempts to evict every evictable pod from nodeName. It is
// safe to call repeatedly: pods already terminating are left alone, and
// pods blocked by a PodDisruptionBudget are reported in Blocked rather
// than treated as a hard failure, so callers can requeue and retry.
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
			continue
		}
		if isMirrorPod(pod) {
			continue
		}
		if opts.IgnoreDaemonSets && isDaemonSetPod(pod) {
			continue
		}

		result.Remaining++

		if !opts.DeleteEmptyDirData && usesEmptyDir(pod) {
			result.Blocked = append(result.Blocked, BlockedPod{
				Namespace: pod.Namespace,
				Name:      pod.Name,
				Reason:    "pod uses emptyDir volumes; set drain.deleteEmptyDirData to allow eviction",
			})
			continue
		}

		if pod.DeletionTimestamp != nil {
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
			if apierrors.IsTooManyRequests(err) {
				result.Blocked = append(result.Blocked, BlockedPod{
					Namespace: pod.Namespace,
					Name:      pod.Name,
					Reason:    fmt.Sprintf("eviction blocked, likely by a PodDisruptionBudget: %v", err),
				})
				continue
			}
			if apierrors.IsNotFound(err) {
				result.Remaining--
				continue
			}
			errs = append(errs, fmt.Errorf("evicting pod %s/%s: %w", pod.Namespace, pod.Name, err))
		}
	}

	return result, errors.Join(errs...)
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
