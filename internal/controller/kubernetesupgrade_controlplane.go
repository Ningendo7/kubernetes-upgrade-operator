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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/upgrade"
)

func (r *KubernetesUpgradeReconciler) reconcileControlPlaneUpgrade(ctx context.Context, ku *upgradev1alpha1.KubernetesUpgrade) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var child upgradev1alpha1.NodeGroupUpgrade
	key := client.ObjectKey{
		Namespace: ku.Namespace,
		Name:      childName(ku.Name, upgrade.ControlPlaneGroupName),
	}
	if err := r.Get(ctx, key, &child); err != nil {
		if apierrors.IsNotFound(err) {
			// Shouldn't normally happen - Prechecks only routes here when a
			// control-plane group was discovered - but don't get stuck.
			log.Info("no control-plane NodeGroupUpgrade found, skipping to WorkersUpgrade")
			ku.Status.Phase = upgradev1alpha1.PhaseWorkersUpgrade
			return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ku)
		}
		return ctrl.Result{}, fmt.Errorf("getting control-plane NodeGroupUpgrade: %w", err)
	}

	if err := clearHold(ctx, r.Client, &child); err != nil {
		return ctrl.Result{}, fmt.Errorf("clearing hold on control-plane group: %w", err)
	}

	ku.Status.ControlPlane = upgradev1alpha1.ControlPlaneUpgradeStatus{
		TotalNodes:    child.Status.TotalNodes,
		UpgradedNodes: child.Status.UpgradedNodes,
		CurrentNode:   currentlyUpgradingNode(child.Status.NodeProgress),
		Version:       ku.Status.StepPlan[ku.Status.CurrentStepIndex].ToVersion,
	}

	switch child.Status.Phase {
	case upgradev1alpha1.NGComplete:
		ku.Status.Phase = upgradev1alpha1.PhaseWorkersUpgrade
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ku)
	case upgradev1alpha1.NGFailed:
		ku.Status.Phase = upgradev1alpha1.PhaseFailed
		ku.Status.Message = fmt.Sprintf("control-plane upgrade failed: %s", child.Status.Message)
		return ctrl.Result{}, r.Status().Update(ctx, ku)
	default:
		if err := r.Status().Update(ctx, ku); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
}

func currentlyUpgradingNode(progress []upgradev1alpha1.NodeProgress) string {
	for _, np := range progress {
		if np.CompletedAt == nil {
			return np.Name
		}
	}
	return ""
}

// clearHold clears spec.hold on a child NodeGroupUpgrade if currently set,
// allowing the NodeGroupUpgrade controller to begin/continue processing it.
// This is the mechanism the parent uses to sequence "control-plane
// completes before workers start."
func clearHold(ctx context.Context, c client.Client, child *upgradev1alpha1.NodeGroupUpgrade) error {
	if !child.Spec.Hold {
		return nil
	}
	patch := client.MergeFrom(child.DeepCopy())
	child.Spec.Hold = false
	return c.Patch(ctx, child, patch)
}
