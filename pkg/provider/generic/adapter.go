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

package generic

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider/kubeadm"
)

// Adapter implements provider.Adapter for infrastructure that isn't one of
// the operator's more specific providers.
//
// Replace is pure client-go: cordoning and draining already happened
// generically in the NodeGroupUpgrade controller before this is ever
// called, so BeginBatch just deletes the Node object, and PollBatch
// confirms it's gone. It does not try to detect a specific replacement -
// that's not knowable from Node metadata alone. Whatever new node shows up
// (recreated by whatever external system manages this infrastructure) gets
// picked up fresh by discovery and version-checked like any other node.
//
// InPlace reuses the kubeadm adapter's executor outright: the mechanics
// ("no cloud API, mutate the host directly") are identical for any node
// the operator can't call a cloud API to replace.

type Adapter struct {
	inPlace *kubeadm.Adapter
}

func init() {
	provider.DefaultRegistry.Register(&Adapter{inPlace: &kubeadm.Adapter{}})
}

func (a *Adapter) Type() upgradev1alpha1.ProviderType { return upgradev1alpha1.ProviderGeneric }

func (a *Adapter) SupportsStrategy(s upgradev1alpha1.NodeGroupStrategy) bool {
	return s == upgradev1alpha1.StrategyInPlace || s == upgradev1alpha1.StrategyReplace
}

func (a *Adapter) Precheck(ctx context.Context, uc provider.UpgradeContext) (bool, string, error) {
	if isInPlace(uc) {
		return a.inPlace.Precheck(ctx, uc)
	}
	return true, "", nil
}

func (a *Adapter) BeginBatch(ctx context.Context, uc provider.UpgradeContext, batch []corev1.Node) error {
	if isInPlace(uc) {
		return a.inPlace.BeginBatch(ctx, uc, batch)
	}
	return beginReplaceBatch(ctx, uc, batch)
}

func (a *Adapter) PollBatch(ctx context.Context, uc provider.UpgradeContext, batch []corev1.Node) ([]provider.NodeResult, error) {
	if isInPlace(uc) {
		return a.inPlace.PollBatch(ctx, uc, batch)
	}
	return pollReplaceBatch(ctx, uc, batch)
}

func (a *Adapter) Verify(ctx context.Context, uc provider.UpgradeContext) (bool, string, error) {
	if isInPlace(uc) {
		return a.inPlace.Verify(ctx, uc)
	}
	return true, "", nil
}

func isInPlace(uc provider.UpgradeContext) bool {
	return uc.Group != nil && uc.Group.Spec.Strategy == upgradev1alpha1.StrategyInPlace
}

func beginReplaceBatch(ctx context.Context, uc provider.UpgradeContext, batch []corev1.Node) error {
	var errs []error
	for i := range batch {
		node := &batch[i]
		if err := uc.Client.Delete(ctx, node); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("deleting node %q: %w", node.Name, err))
		}
	}
	return errors.Join(errs...)
}

func pollReplaceBatch(ctx context.Context, uc provider.UpgradeContext, batch []corev1.Node) ([]provider.NodeResult, error) {
	results := make([]provider.NodeResult, 0, len(batch))
	for i := range batch {
		node := &batch[i]
		var current corev1.Node
		err := uc.Client.Get(ctx, client.ObjectKey{Name: node.Name}, &current)
		switch {
		case apierrors.IsNotFound(err):
			results = append(results, provider.NodeResult{NodeName: node.Name, Phase: provider.NodePhaseUpgraded})
		case err != nil:
			return nil, fmt.Errorf("getting node %q: %w", node.Name, err)
		default:
			results = append(results, provider.NodeResult{NodeName: node.Name, Phase: provider.NodePhaseInProgress})
		}
	}
	return results, nil
}