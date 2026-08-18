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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

// KubernetesUpgradeReconciler reconciles a KubernetesUpgrade object
type KubernetesUpgradeReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// DiscoveryClient reports the API server's own version - the
	// authoritative source for "what version is the cluster currently on,"
	// used to compute status.startingVersion and status.stepPlan.
	DiscoveryClient discovery.DiscoveryInterface

	// OperatorNamespace is where the mutual-exclusion Lease lives. It must
	// be a single fixed namespace (not the KubernetesUpgrade's own
	// namespace) so upgrades in different namespaces still contend for the
	// same Lease, enforcing "only one active upgrade cluster-wide."
	OperatorNamespace string
}

const pausedConditionType = "Paused"

// +kubebuilder:rbac:groups=upgrade.k8s-upgrade-operator,resources=kubernetesupgrades,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=upgrade.k8s-upgrade-operator,resources=kubernetesupgrades/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=upgrade.k8s-upgrade-operator,resources=kubernetesupgrades/finalizers,verbs=update
// +kubebuilder:rbac:groups=upgrade.k8s-upgrade-operator,resources=nodegroupupgrades,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=upgrade.k8s-upgrade-operator,resources=nodegroupupgrades/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:urls=/version,verbs=get

// Reconcile drives a KubernetesUpgrade through its state machine:
// Pending -> Discovering -> Prechecks -> ControlPlaneUpgrade ->
// WorkersUpgrade -> Postchecks -> (loop back to Discovering for the next
// step, or) Complete/Failed.
func (r *KubernetesUpgradeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var ku upgradev1alpha1.KubernetesUpgrade
	if err := r.Get(ctx, req.NamespacedName, &ku); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if changed := setPausedCondition(&ku); changed {
		if err := r.Status().Update(ctx, &ku); err != nil {
			return ctrl.Result{}, err
		}
	}
	if ku.Spec.Paused {
		log.V(1).Info("upgrade is paused, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	switch ku.Status.Phase {
	case "", upgradev1alpha1.PhasePending:
		return r.reconcilePending(ctx, &ku)
	case upgradev1alpha1.PhaseDiscovering:
		return r.reconcileDiscovering(ctx, &ku)
	case upgradev1alpha1.PhasePrechecks:
		return r.reconcilePrechecks(ctx, &ku)
	case upgradev1alpha1.PhaseControlPlaneUpgrade:
		return r.reconcileControlPlaneUpgrade(ctx, &ku)
	case upgradev1alpha1.PhaseWorkersUpgrade:
		return r.reconcileWorkersUpgrade(ctx, &ku)
	case upgradev1alpha1.PhasePostchecks:
		return r.reconcilePostchecks(ctx, &ku)
	case upgradev1alpha1.PhaseComplete, upgradev1alpha1.PhaseFailed, upgradev1alpha1.PhasePaused:
		if err := r.releaseLease(ctx, ku.Namespace+"/"+ku.Name); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	default:
		log.Info("unknown phase, taking no action", "phase", ku.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// setPausedCondition reflects spec.paused into a status condition without
// ever touching status.phase, so pausing/unpausing never loses track of
// which real phase to resume into.
func setPausedCondition(ku *upgradev1alpha1.KubernetesUpgrade) bool {
	status := metav1.ConditionFalse
	reason := "NotPaused"
	if ku.Spec.Paused {
		status = metav1.ConditionTrue
		reason = "UserRequested"
	}
	return meta.SetStatusCondition(&ku.Status.Conditions, metav1.Condition{
		Type:    pausedConditionType,
		Status:  status,
		Reason:  reason,
		Message: "Reflects spec.paused",
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *KubernetesUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&upgradev1alpha1.KubernetesUpgrade{}).
		Owns(&upgradev1alpha1.NodeGroupUpgrade{}).
		Named("kubernetesupgrade").
		Complete(r)
}
