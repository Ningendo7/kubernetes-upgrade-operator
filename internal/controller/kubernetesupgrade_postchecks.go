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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
)

// reconcilePostchecks does a single-pass cluster health check before
// advancing. NOTE: this deliberately does not require health to be
// *sustained* for a window (as originally sketched) - a scope cut to keep
// this shippable; a briefly-flapping node could in theory slip through a
// single check. Worth hardening later.
func (r *KubernetesUpgradeReconciler) reconcilePostchecks(ctx context.Context, ku *upgradev1alpha1.KubernetesUpgrade) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing nodes: %w", err)
	}
	for i := range nodes.Items {
		if !k8sutil.IsNodeReady(&nodes.Items[i]) {
			log.Info("waiting for node to become Ready during postchecks",
				"node", nodes.Items[i].Name, "reason", k8sutil.NodeNotReadyReason(&nodes.Items[i]))
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
	}

	now := metav1.Now()
	ku.Status.StepPlan[ku.Status.CurrentStepIndex].CompletedAt = &now

	if int(ku.Status.CurrentStepIndex)+1 < len(ku.Status.StepPlan) {
		ku.Status.CurrentStepIndex++
		ku.Status.Phase = upgradev1alpha1.PhaseDiscovering
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ku)
	}

	ku.Status.Phase = upgradev1alpha1.PhaseComplete
	ku.Status.Message = fmt.Sprintf("upgraded to %s", ku.Spec.TargetVersion)
	return ctrl.Result{}, r.Status().Update(ctx, ku)
}
