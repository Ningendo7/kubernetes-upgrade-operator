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
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

func (r *NodeGroupUpgradeReconciler) reconcileUpgrading(ctx context.Context, ng *upgradev1alpha1.NodeGroupUpgrade) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	active := activeBatch(ng.Status.NodeProgress)
	if len(active) == 0 {
		ng.Status.Phase = upgradev1alpha1.NGDraining
		return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ng)
	}

	adapter, ok := r.Adapters.Get(ng.Spec.Provider)
	if !ok {
		ng.Status.Phase = upgradev1alpha1.NGFailed
		ng.Status.Message = fmt.Sprintf("no adapter registered for provider %q", ng.Spec.Provider)
		return ctrl.Result{}, r.Status().Update(ctx, ng)
	}

	uc := provider.UpgradeContext{
		Client:		r.Client,
		Log:       	log,
		Group:     	ng,
		TargetVersion: 	ng.Spec.TargetVersion,
	}

	ready, reason, err := adapter.Precheck(ctx, uc)
	if err != nil {
		return r.handleAdapterError(ctx, ng, "Precheck", err)
	}
	if !ready {
		log.Info("adapter precheck not ready, waiting", "reason", reason)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	batch, err := r.activeBatchNodes(ctx, active)
	if err != nil {
		return ctrl.Result{}, err
	}

	if needsBegin(ng.Status.NodeProgress, active) {
		if err := adapter.BeginBatch(ctx, uc, batch); err != nil {
			return r.handleAdapterError(ctx, ng, "BeginBatch", err)
		}
		for _, name := range active {
			if idx := findNodeProgress(ng.Status.NodeProgress, name); idx != -1 {
				ng.Status.NodeProgress[idx].Phase = "Upgrading"
			}
		}
		if err := r.Status().Update(ctx, ng); err != nil {
			return ctrl.Result{}, err
		}
	}

	results, err := adapter.PollBatch(ctx, uc, batch)
	if err != nil {
		return r.handleAdapterError(ctx, ng, "PollBatch", err)
	}

	allUpgraded := true
	for _, res := range results {
		idx := findNodeProgress(ng.Status.NodeProgress, res.NodeName)
		if idx == -1 {
			continue
		}
		switch res.Phase {
		case provider.NodePhaseUpgraded:
			ng.Status.NodeProgress[idx].Phase = "Upgraded"
		case provider.NodePhaseFailed:
			msg := "upgrade failed"
			if res.Error != nil {
				msg = res.Error.Error()
			}
			ng.Status.NodeProgress[idx].Phase = "Failed"
			ng.Status.NodeProgress[idx].Error = msg
			ng.Status.Phase = upgradev1alpha1.NGFailed
			ng.Status.Message = fmt.Sprintf("node %q failed to upgrade: %s", res.NodeName, msg)
			return ctrl.Result{}, r.Status().Update(ctx, ng)
		default:
			allUpgraded = false
		}
	}

	if !allUpgraded {
		if err := r.Status().Update(ctx, ng); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	ng.Status.Phase = upgradev1alpha1.NGVerifying
	return ctrl.Result{Requeue: true}, r.Status().Update(ctx, ng)
}

// handleAdapterError distinguishes "this provider isn't implemented yet"
// (pauses - a config/deployment issue retrying won't fix) from a genuine
// runtime failure (fails immediately).
func (r *NodeGroupUpgradeReconciler) handleAdapterError(ctx context.Context, ng *upgradev1alpha1.NodeGroupUpgrade, step string, err error) (ctrl.Result, error) {
	if errors.Is(err, provider.ErrNotImplemented) {
		ng.Status.Phase = upgradev1alpha1.NGPaused
		ng.Status.Message = fmt.Sprintf("%s: provider %q is not yet implemented", step, ng.Spec.Provider)
		if r.Recorder != nil {
			r.Recorder.Event(ng, corev1.EventTypeWarning, "ProviderNotImplemented", ng.Status.Message)
		}
		return ctrl.Result{}, r.Status().Update(ctx, ng)
	}
	ng.Status.Phase = upgradev1alpha1.NGFailed
	ng.Status.Message = fmt.Sprintf("%s: %v", step, err)
	return ctrl.Result{}, r.Status().Update(ctx, ng)
}

func needsBegin(progress []upgradev1alpha1.NodeProgress, active []string) bool {
	for _, name := range active {
		idx := findNodeProgress(progress, name)
		if idx != -1 && progress[idx].Phase != "Upgrading" && progress[idx].Phase != "Upgraded" {
			return true
		}
	}
	return false
}

func (r *NodeGroupUpgradeReconciler) activeBatchNodes(ctx context.Context, names []string) ([]corev1.Node, error) {
	nodes := make([]corev1.Node, 0, len(names))
	for _, name := range names {
		var node corev1.Node
		if err := r.Get(ctx, client.ObjectKey{Name: name}, &node); err != nil {
			return nil, fmt.Errorf("getting node %q: %w", name, err)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}