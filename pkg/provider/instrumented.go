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
	"time"

	corev1 "k8s.io/api/core/v1"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	obs "github.com/Ningendo7/kubernetes-upgrade-operator/pkg/observability"
)

// Outcome label values - a fixed set of three, never raw error text.
const (
	outcomeOK       = "ok"
	outcomeNotReady = "not_ready"
	outcomeError    = "error"
)

// instrumentedAdapter wraps an Adapter and records kuo_adapter_operation_*
// metrics around its four operation methods (Precheck, BeginBatch,
// PollBatch, Verify). Type and SupportsStrategy are pure lookups and pass
// straight through. Every Adapter goes through here automatically - see
// Registry.Register - so a new provider is instrumented the moment it
// registers, with no per-adapter code.
type instrumentedAdapter struct {
	inner    Adapter
	provider string
}

// instrument wraps a in an instrumentedAdapter, unless it already is one.
func instrument(a Adapter) Adapter {
	if _, already := a.(*instrumentedAdapter); already {
		return a
	}
	return &instrumentedAdapter{inner: a, provider: string(a.Type())}
}

func (i *instrumentedAdapter) Type() upgradev1alpha1.ProviderType { return i.inner.Type() }

func (i *instrumentedAdapter) SupportsStrategy(s upgradev1alpha1.NodeGroupStrategy) bool {
	return i.inner.SupportsStrategy(s)
}

func (i *instrumentedAdapter) record(operation, outcome string, start time.Time) {
	obs.AdapterOperationDuration.WithLabelValues(i.provider, operation).Observe(time.Since(start).Seconds())
	obs.AdapterOperationTotal.WithLabelValues(i.provider, operation, outcome).Inc()
}

func (i *instrumentedAdapter) Precheck(ctx context.Context, uc UpgradeContext) (bool, string, error) {
	start := time.Now()
	ready, reason, err := i.inner.Precheck(ctx, uc)
	i.record("Precheck", classifyReady(ready, err), start)
	return ready, reason, err
}

func (i *instrumentedAdapter) BeginBatch(ctx context.Context, uc UpgradeContext, batch []corev1.Node) error {
	start := time.Now()
	err := i.inner.BeginBatch(ctx, uc, batch)
	i.record("BeginBatch", classifyErr(err), start)
	return err
}

func (i *instrumentedAdapter) PollBatch(ctx context.Context, uc UpgradeContext, batch []corev1.Node) ([]NodeResult, error) {
	start := time.Now()
	results, err := i.inner.PollBatch(ctx, uc, batch)
	i.record("PollBatch", classifyErr(err), start)
	return results, err
}

func (i *instrumentedAdapter) Verify(ctx context.Context, uc UpgradeContext) (bool, string, error) {
	start := time.Now()
	verified, reason, err := i.inner.Verify(ctx, uc)
	i.record("Verify", classifyReady(verified, err), start)
	return verified, reason, err
}

// classifyReady maps a (ready/verified, err) pair to one of the three
// fixed outcome values.
func classifyReady(ready bool, err error) string {
	switch {
	case err != nil:
		return outcomeError
	case ready:
		return outcomeOK
	default:
		return outcomeNotReady
	}
}

func classifyErr(err error) string {
	if err != nil {
		return outcomeError
	}
	return outcomeOK
}
