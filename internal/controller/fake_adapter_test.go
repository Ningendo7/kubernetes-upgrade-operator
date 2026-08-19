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

	corev1 "k8s.io/api/core/v1"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

// fakeAdapter is a test-only provider.Adapter that never touches real
// infrastructure: BeginBatch is a no-op, PollBatch immediately reports
// every node as upgraded. envtest has no real kubelets to run an actual
// upgrade against, so integration tests simulate "the node came back
// healthy at the new version" by setting the synthetic Node's status
// directly; this fake adapter only skips the mechanics that would
// otherwise require a real Job running on a real host.
type fakeAdapter struct {
	providerType upgradev1alpha1.ProviderType
}

func (f *fakeAdapter) Type() upgradev1alpha1.ProviderType { return f.providerType }

func (f *fakeAdapter) SupportsStrategy(s upgradev1alpha1.NodeGroupStrategy) bool {
	return s == upgradev1alpha1.StrategyInPlace || s == upgradev1alpha1.StrategyReplace
}

func (f *fakeAdapter) Precheck(_ context.Context, _ provider.UpgradeContext) (bool, string, error) {
	return true, "", nil
}

func (f *fakeAdapter) BeginBatch(_ context.Context, _ provider.UpgradeContext, _ []corev1.Node) error {
	return nil
}

func (f *fakeAdapter) PollBatch(_ context.Context, _ provider.UpgradeContext, batch []corev1.Node) ([]provider.NodeResult, error) {
	results := make([]provider.NodeResult, 0, len(batch))
	for _, n := range batch {
		results = append(results, provider.NodeResult{NodeName: n.Name, Phase: provider.NodePhaseUpgraded})
	}
	return results, nil
}

func (f *fakeAdapter) Verify(_ context.Context, _ provider.UpgradeContext) (bool, string, error) {
	return true, "", nil
}
