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

package provider

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

type fakeAdapter struct {
	providerType upgradev1alpha1.ProviderType
}

func (f fakeAdapter) Type() upgradev1alpha1.ProviderType                        { return f.providerType }
func (f fakeAdapter) SupportsStrategy(_ upgradev1alpha1.NodeGroupStrategy) bool { return true }
func (f fakeAdapter) Precheck(_ context.Context, _ UpgradeContext) (bool, string, error) {
	return true, "", nil
}
func (f fakeAdapter) BeginBatch(_ context.Context, _ UpgradeContext, _ []corev1.Node) error {
	return nil
}
func (f fakeAdapter) PollBatch(_ context.Context, _ UpgradeContext, _ []corev1.Node) ([]NodeResult, error) {
	return nil, nil
}
func (f fakeAdapter) Verify(_ context.Context, _ UpgradeContext) (bool, string, error) {
	return true, "", nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()

	if _, ok := r.Get(upgradev1alpha1.ProviderKubeadm); ok {
		t.Fatalf("expected no adapter registered yet")
	}

	a := fakeAdapter{providerType: upgradev1alpha1.ProviderKubeadm}
	r.Register(a)

	got, ok := r.Get(upgradev1alpha1.ProviderKubeadm)
	if !ok {
		t.Fatalf("expected adapter to be found after Register")
	}
	if got.Type() != upgradev1alpha1.ProviderKubeadm {
		t.Errorf("got Type() = %v, want %v", got.Type(), upgradev1alpha1.ProviderKubeadm)
	}
}

func TestRegistry_IsolatedFromOtherRegistries(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get(upgradev1alpha1.ProviderGeneric); ok {
		t.Fatalf("expected a fresh registry to start empty")
	}
}
