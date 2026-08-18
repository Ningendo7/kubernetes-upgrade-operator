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

package awsasg

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

// Adapter is an interface-conformant placeholder for self-managed AWS Auto
// Scaling Group upgrades. A real Replace implementation will call
// autoscaling:StartInstanceRefresh, coordinated with lifecycle hooks so
// draining happens before an instance actually terminates - materially
// harder than the managed-node-group case and not yet built. (A real
// InPlace implementation, for ASG nodes that turn out to be plain
// kubeadm-joined "pets," could delegate to pkg/provider/kubeadm exactly
// like pkg/provider/generic does - a natural extension point, also not yet
// built.) Every method reports provider.ErrNotImplemented for now.
type Adapter struct{}

func init() {
	provider.DefaultRegistry.Register(&Adapter{})
}

func (a *Adapter) Type() upgradev1alpha1.ProviderType {
	return upgradev1alpha1.ProviderAWSAutoScalingGroup
}

func (a *Adapter) SupportsStrategy(s upgradev1alpha1.NodeGroupStrategy) bool {
	return s == upgradev1alpha1.StrategyReplace || s == upgradev1alpha1.StrategyInPlace
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
