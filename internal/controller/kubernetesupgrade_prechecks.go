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

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
)

const (
	upgradeLeaseName = "kubernetes-upgrade-operator-active-upgrade"
	leaseDuration    = 5 * time.Minute
)

func (r *KubernetesUpgradeReconciler) reconcilePrechecks(ctx context.Context, ku *upgradev1alpha1.KubernetesUpgrade) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	holderID := ku.Namespace + "/" + ku.Name
	acquired, err := r.acquireLease(ctx, holderID)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("acquiring upgrade lease: %w", err)
	}
	if !acquired {
		log.Info("another KubernetesUpgrade is already active, waiting")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing nodes: %w", err)
	}
	for i := range nodes.Items {
		if !k8sutil.IsNodeReady(&nodes.Items[i]) {
			log.Info("waiting for node to become Ready before proceeding",
				"node", nodes.Items[i].Name, "reason", k8sutil.NodeNotReadyReason(&nodes.Items[i]))
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
	}

	ku.Status.Phase = nextPhaseAfterPrechecks(ku.Status.DiscoveredGroups)
	if err := r.Status().Update(ctx, ku); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// nextPhaseAfterPrechecks skips ControlPlaneUpgrade entirely when this
// step has no control-plane group - e.g. a pure EKS/LKE cluster, where
// control-plane nodes aren't visible Node objects at all.
func nextPhaseAfterPrechecks(groups []upgradev1alpha1.DiscoveredGroupStatus) upgradev1alpha1.KubernetesUpgradePhase {
	for _, g := range groups {
		if g.Role == upgradev1alpha1.RoleControlPlane {
			return upgradev1alpha1.PhaseControlPlaneUpgrade
		}
	}
	return upgradev1alpha1.PhaseWorkersUpgrade
}

// acquireLease implements mutual exclusion via a coordination.k8s.io Lease
// in the operator's own namespace (not the KubernetesUpgrade's namespace),
// so upgrades in different namespaces still contend for the same Lease -
// enforcing only one active KubernetesUpgrade cluster-wide at a time. A
// lease not renewed within leaseDuration is treated as abandoned (e.g. its
// holder crashed) and can be reclaimed by another KubernetesUpgrade.
func (r *KubernetesUpgradeReconciler) acquireLease(ctx context.Context, holderID string) (bool, error) {
	now := metav1.NowMicro()

	var lease coordinationv1.Lease
	err := r.Get(ctx, client.ObjectKey{Namespace: r.OperatorNamespace, Name: upgradeLeaseName}, &lease)
	if apierrors.IsNotFound(err) {
		lease = coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      upgradeLeaseName,
				Namespace: r.OperatorNamespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity: &holderID,
				RenewTime:      &now,
			},
		}
		if createErr := r.Create(ctx, &lease); createErr != nil {
			if apierrors.IsAlreadyExists(createErr) {
				return false, nil // lost the race to create it
			}
			return false, createErr
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}

	held := lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != ""
	isUs := held && *lease.Spec.HolderIdentity == holderID
	stale := lease.Spec.RenewTime == nil || now.Sub(lease.Spec.RenewTime.Time) > leaseDuration

	if held && !isUs && !stale {
		return false, nil // actively held by someone else
	}

	lease.Spec.HolderIdentity = &holderID
	lease.Spec.RenewTime = &now
	if updateErr := r.Update(ctx, &lease); updateErr != nil {
		if apierrors.IsConflict(updateErr) {
			return false, nil // lost a race to claim/renew; try again next reconcile
		}
		return false, updateErr
	}
	return true, nil
}

// releaseLease is called when a KubernetesUpgrade reaches a terminal
// phase, so the next upgrade doesn't have to wait out leaseDuration.
func (r *KubernetesUpgradeReconciler) releaseLease(ctx context.Context, holderID string) error {
	var lease coordinationv1.Lease
	err := r.Get(ctx, client.ObjectKey{Namespace: r.OperatorNamespace, Name: upgradeLeaseName}, &lease)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != holderID {
		return nil // not ours to release
	}
	return r.Delete(ctx, &lease)
}
