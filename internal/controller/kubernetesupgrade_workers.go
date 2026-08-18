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

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

// reconcileWorkersUpgrade clears Hold on every worker group up front, so
// they proceed in parallel (not one at a time) - that's the whole point of
// giving each group its own NodeGroupUpgrade/controller.
func (r *KubernetesUpgradeReconciler) reconcileWorkersUpgrade(ctx context.Context, ku *upgradev1alpha1.KubernetesUpgrade) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var children upgradev1alpha1.NodeGroupUpgradeList
	if err := r.List(
		ctx,
		&children,
		client.InNamespace(ku.Namespace),
		client.MatchingLabels{parentLabelKey: ku.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing NodeGroupUpgrade children: %w", err)
	}

	allComplete := true
	for i := range children.Items {
		child := &children.Items[i]
		if child.Spec.Role != upgradev1alpha1.RoleWorker {
			continue
		}

		if err := clearHold(ctx, r.Client, child); err != nil {
			return ctrl.Result{}, fmt.Errorf("clearing hold on worker group: %w", err)
		}

		switch child.Status.Phase {
		case upgradev1alpha1.NGComplete:
			continue
		case upgradev1alpha1.NGFailed:
			ku.Status.Phase = upgradev1alpha1.PhaseFailed
			ku.Status.Message = fmt.Sprintf("Worker group %q failed: %s", child.Name, child.Status.Message)
			return ctrl.Result{}, r.Status().Update(ctx, ku)
		default:
			allComplete = false
		}
	}

	if !allComplete {
		log.V(1).Info("waiting for worker groups to complete")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	ku.Status.Phase = upgradev1alpha1.PhasePostchecks
	return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ku)
}
