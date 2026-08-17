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

package linodelke

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

// Adapter is an interface-conformant placeholder for Linode LKE node pool
// upgrades. A real implementation will call LKE's pool-recycle endpoint
// (via linodego) and poll its status; until then, every method reports
// provider.ErrNotImplemented.
type Adapter struct{}

func init() {
	provider.DefaultRegistry.Register(&Adapter{})
}

func (a *Adapter) Type() upgradev1alpha1.ProviderType { return upgradev1alpha1.ProviderLinodeLKE }

func (a *Adapter) SupportsStrategy(s upgradev1alpha1.NodeGroupStrategy) bool {
	return s == upgradev1alpha1.StrategyReplace
}

func (a *Adapter) Precheck(_ context.Context, _ provider.UpgradeContext) (bool, string, error) {
	return false, "", provider.ErrNotImplemented
}

func (a *Adapter) BeginBatch(_ context.Context, _ provider.UpgradeContext, _ []corev1.Node) error {
	return provider.ErrNotImplemented
}

func (a *Adapter) PollBatch(_ context.Context, _ provider.UpgradeContext, _ []corev1.Node) ([]provider.NodeResult, error) {
	return nil, provider.ErrNotImplemented
}

func (a *Adapter) Verify(_ context.Context, _ provider.UpgradeContext) (bool, string, error) {
	return false, "", provider.ErrNotImplemented
}
