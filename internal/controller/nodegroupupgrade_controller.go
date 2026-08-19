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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

// NodeGroupUpgradeReconciler reconciles a NodeGroupUpgrade object
type NodeGroupUpgradeReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Adapters is injectable so tests can register fake adapters,
	// completely isolated from provider.DefaultRegistry.
	Adapters *provider.Registry
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=upgrade.k8s-upgrade-operator,resources=nodegroupupgrades,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=upgrade.k8s-upgrade-operator,resources=nodegroupupgrades/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=upgrade.k8s-upgrade-operator,resources=nodegroupupgrades/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=pods/eviction,verbs=create
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives a NodeGroupUpgrade through its state machine:
// Pending -> Draining -> Upgrading -> Verifying -> (loop back to Draining
// for the next batch, or) Complete/Failed.
func (r *NodeGroupUpgradeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var ng upgradev1alpha1.NodeGroupUpgrade
	if err := r.Get(ctx, req.NamespacedName, &ng); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if changed := setNGPausedCondition(&ng); changed {
		if err := r.Status().Update(ctx, &ng); err != nil {
			return ctrl.Result{}, err
		}
	}
	if ng.Spec.Paused {
		log.V(1).Info("group is paused, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if ng.Spec.Hold {
		log.V(1).Info("held by parent KubernetesUpgrade, waiting")
		return ctrl.Result{}, nil
	}

	switch ng.Status.Phase {
	case "", upgradev1alpha1.NGPending:
		return r.reconcilePending(ctx, &ng)
	case upgradev1alpha1.NGDraining:
		return r.reconcileDraining(ctx, &ng)
	case upgradev1alpha1.NGUpgrading:
		return r.reconcileUpgrading(ctx, &ng)
	case upgradev1alpha1.NGVerifying:
		return r.reconcileVerifying(ctx, &ng)
	case upgradev1alpha1.NGComplete, upgradev1alpha1.NGFailed, upgradev1alpha1.NGPaused:
		return ctrl.Result{}, nil
	default:
		log.Info("phase not yet implemented, will be handled in a future step", "phase", ng.Status.Phase)
		return ctrl.Result{}, nil
	}
}

func setNGPausedCondition(ng *upgradev1alpha1.NodeGroupUpgrade) bool {
	status := metav1.ConditionFalse
	reason := "NotPaused"
	if ng.Spec.Paused {
		status = metav1.ConditionTrue
		reason = "UserRequested"
	}
	return meta.SetStatusCondition(&ng.Status.Conditions, metav1.Condition{
		Type:    pausedConditionType,
		Status:  status,
		Reason:  reason,
		Message: "reflects spec.paused",
	})
}

func (r *NodeGroupUpgradeReconciler) reconcilePending(ctx context.Context, ng *upgradev1alpha1.NodeGroupUpgrade) (ctrl.Result, error) {
	ng.Status.TotalNodes = int32(len(ng.Spec.Nodes))
	ng.Status.NodeProgress = initNodeProgress(ng.Spec.Nodes, ng.Spec.TargetVersion)
	ng.Status.Phase = upgradev1alpha1.NGDraining
	ng.Status.ObservedGeneration = ng.Generation
	return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ng)
}

func initNodeProgress(nodes []string, targetVersion string) []upgradev1alpha1.NodeProgress {
	progress := make([]upgradev1alpha1.NodeProgress, 0, len(nodes))
	for _, n := range nodes {
		progress = append(progress, upgradev1alpha1.NodeProgress{
			Name:      n,
			ToVersion: targetVersion,
		})
	}
	return progress
}

// SetupWithManager sets up the controller with the Manager.
func (r *NodeGroupUpgradeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, k8sutil.NodeNameIndexField, func(obj client.Object) []string {
		pod, ok := obj.(*corev1.Pod)
		if !ok || pod.Spec.NodeName == "" {
			return nil
		}
		return []string{pod.Spec.NodeName}
	}); err != nil {
		return fmt.Errorf("indexing pods by spec.nodeName: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&upgradev1alpha1.NodeGroupUpgrade{}).
		Named("nodegroupupgrade").
		Complete(r)
}
