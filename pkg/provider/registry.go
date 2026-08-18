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
	"sync"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

// Registry holds provider adapters keyed by their ProviderType. Construct
// one with NewRegistry; the zero value is not usable.
type Registry struct {
	mu       sync.RWMutex
	adapters map[upgradev1alpha1.ProviderType]Adapter
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		adapters: map[upgradev1alpha1.ProviderType]Adapter{},
	}
}

// Register makes an adapter available under its own Type().
func (r *Registry) Register(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Type()] = a
}

// Get looks up the adapter registered for a provider type.
func (r *Registry) Get(t upgradev1alpha1.ProviderType) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[t]
	return a, ok
}

// DefaultRegistry is populated by each provider adapter package's init()
// function (see pkg/provider/kubeadm, pkg/provider/generic, etc.) once
// cmd/main.go blank-imports them. Production wiring passes DefaultRegistry
// into the NodeGroupUpgradeReconciler; tests construct their own Registry
// with NewRegistry() and register fakes instead, fully isolated from this.
var DefaultRegistry = NewRegistry()
