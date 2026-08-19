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

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/upgrade"
)

func (r *NodeGroupUpgradeReconciler) reconcileDraining(ctx context.Context, ng *upgradev1alpha1.NodeGroupUpgrade) (ctrl.Result, error) {
	// log := logf.FromContext(ctx)

	active := activeBatch(ng.Status.NodeProgress)
	if len(active) == 0 {
		next, err := nextBatchFor(ng)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(next) == 0 {
			ng.Status.Phase = upgradev1alpha1.NGComplete
			return ctrl.Result{}, r.Status().Update(ctx, ng)
		}

		now := metav1.Now()
		for _, name := range next {
			idx := findNodeProgress(ng.Status.NodeProgress, name)
			if idx == -1 {
				continue
			}
			ng.Status.NodeProgress[idx].StartedAt = &now
			ng.Status.NodeProgress[idx].Phase = "Draining"
			if node, err := k8sutil.GetNode(ctx, r.Client, name); err == nil {
				ng.Status.NodeProgress[idx].FromVersion = node.Status.NodeInfo.KubeletVersion
			}
		}
		active = next
		if err := r.Status().Update(ctx, ng); err != nil {
			return ctrl.Result{}, err
		}
	}

	drainOpts := k8sutil.DrainOptions{
		GracePeriodSeconds: toInt64Ptr(ng.Spec.Drain.GracePeriodSeconds),
		IgnoreDaemonSets:   ng.Spec.Drain.IgnoreDaemonSets,
		DeleteEmptyDirData: ng.Spec.Drain.DeleteEmptyDirData,
	}

	allDrained := true
	for _, name := range active {
		if err := k8sutil.Cordon(ctx, r.Client, name); err != nil {
			return ctrl.Result{}, fmt.Errorf("cordoning node %q: %w", name, err)
		}

		result, err := k8sutil.DrainNode(ctx, r.Client, name, drainOpts)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("draining node %q: %w", name, err)
		}

		if result.Remaining == 0 {
			if idx := findNodeProgress(ng.Status.NodeProgress, name); idx != -1 {
				ng.Status.NodeProgress[idx].Phase = "Drained"
			}
			continue
		}

		allDrained = false
		if err := r.handleStuckDrain(ctx, ng, name, result); err != nil {
			return ctrl.Result{}, err
		}
	}

	if !allDrained {
		if err := r.Status().Update(ctx, ng); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	ng.Status.Phase = upgradev1alpha1.NGUpgrading
	return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ng)
}

// handleStuckDrain reports (via a condition) or, if the caller has opted
// into spec.drain.force and the timeout has elapsed, resolves a drain
// that's blocked on one or more pods (typically a PodDisruptionBudget).
func (r *NodeGroupUpgradeReconciler) handleStuckDrain(ctx context.Context, ng *upgradev1alpha1.NodeGroupUpgrade, nodeName string, result k8sutil.DrainResult) error {
	log := logf.FromContext(ctx)

	idx := findNodeProgress(ng.Status.NodeProgress, nodeName)
	if idx == -1 || ng.Status.NodeProgress[idx].StartedAt == nil {
		return nil
	}

	timeout := 10 * time.Minute
	if ng.Spec.Drain.TimeoutSeconds != nil {
		timeout = time.Duration(*ng.Spec.Drain.TimeoutSeconds) * time.Second
	}
	elapsed := time.Since(ng.Status.NodeProgress[idx].StartedAt.Time)
	if elapsed < timeout {
		return nil
	}

	if !ng.Spec.Drain.Force {
		meta.SetStatusCondition(&ng.Status.Conditions, metav1.Condition{
			Type:    "DrainStuck",
			Status:  metav1.ConditionTrue,
			Reason:  "TimeoutExceeded",
			Message: fmt.Sprintf("node %q still has pods blocking eviction after %s; set drain.force to override", nodeName, timeout),
		})
		return nil
	}

	log.Info("drain timeout exceeded and force is set, force-deleting blocked pods", "node", nodeName, "blocked", result.Blocked)
	for _, b := range result.Blocked {
		pod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: b.Namespace,
				Name:      b.Name,
			},
		}
		if err := r.Client.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("force-deleting pod %s/%s: %w", b.Namespace, b.Name, err)
		}
	}
	return nil
}

func activeBatch(progress []upgradev1alpha1.NodeProgress) []string {
	var active []string
	for _, np := range progress {
		if np.StartedAt != nil && np.CompletedAt == nil {
			active = append(active, np.Name)
		}
	}
	return active
}

func findNodeProgress(progress []upgradev1alpha1.NodeProgress, name string) int {
	for i, np := range progress {
		if np.Name == name {
			return i
		}
	}
	return -1
}

func nextBatchFor(ng *upgradev1alpha1.NodeGroupUpgrade) ([]string, error) {
	done := map[string]bool{}
	inProgress := map[string]bool{}
	for _, np := range ng.Status.NodeProgress {
		switch {
		case np.CompletedAt != nil:
			done[np.Name] = true
		case np.StartedAt != nil:
			inProgress[np.Name] = true
		}
	}
	return upgrade.NextBatch(ng.Spec.Nodes, done, inProgress, ng.Spec.BatchSize, ng.Spec.MaxUnavailable)
}

func toInt64Ptr(v *int32) *int64 {
	if v == nil {
		return nil
	}
	i := int64(*v)
	return &i
}
