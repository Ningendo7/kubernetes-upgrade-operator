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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

func (r *NodeGroupUpgradeReconciler) reconcileVerifying(ctx context.Context, ng *upgradev1alpha1.NodeGroupUpgrade) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	active := activeBatch(ng.Status.NodeProgress)
	if len(active) == 0 {
		ng.Status.Phase = upgradev1alpha1.NGDraining
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ng)
	}

	for _, name := range active {
		node, err := k8sutil.GetNode(ctx, r.Client, name)
		if err != nil {
			return ctrl.Result{}, err
		}

		if !k8sutil.IsNodeReady(node) {
			log.Info("waiting for node to become Ready", "node", name, "reason", k8sutil.NodeNotReadyReason(node))
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}

		atVersion, err := k8sutil.NodeIsAtVersion(node, ng.Spec.TargetVersion)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("checking version for node %q: %w", name, err)
		}
		if !atVersion {
			log.Info("waiting for node to report target kubelet version", "node", name, "target", ng.Spec.TargetVersion)
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
	}

	adapter, ok := r.Adapters.Get(ng.Spec.Provider)
	if !ok {
		ng.Status.Phase = upgradev1alpha1.NGFailed
		ng.Status.Message = fmt.Sprintf("no adapter registered for provider %q", ng.Spec.Provider)
		return ctrl.Result{}, r.Status().Update(ctx, ng)
	}

	uc := provider.UpgradeContext{
		Client:        r.Client,
		Log:           log,
		Group:         ng,
		TargetVersion: ng.Spec.TargetVersion,
	}

	verified, reason, err := adapter.Verify(ctx, uc)
	if err != nil {
		return r.handleAdapterError(ctx, ng, "Verify", err)
	}
	if !verified {
		log.Info("adapter verify not satisfied, waiting", "reason", reason)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	now := metav1.Now()
	for _, name := range active {
		if err := k8sutil.Uncordon(ctx, r.Client, name); err != nil {
			return ctrl.Result{}, fmt.Errorf("uncordon node %q: %w", name, err)
		}
		if idx := findNodeProgress(ng.Status.NodeProgress, name); idx != -1 {
			ng.Status.NodeProgress[idx].CompletedAt = &now
			ng.Status.NodeProgress[idx].Phase = "Ready"
		}
		ng.Status.UpgradedNodes++
	}

	ng.Status.Phase = upgradev1alpha1.NGDraining
	return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ng)
}
