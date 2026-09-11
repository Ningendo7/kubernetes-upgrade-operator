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
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	obs "github.com/Ningendo7/kubernetes-upgrade-operator/pkg/observability"
)

// stubAdapter returns whatever it is told to, so the decorator's outcome
// classification can be checked against every branch.
type stubAdapter struct {
	ready    bool
	verified bool
	err      error
}

func (s *stubAdapter) Type() upgradev1alpha1.ProviderType { return upgradev1alpha1.ProviderGeneric }
func (s *stubAdapter) SupportsStrategy(upgradev1alpha1.NodeGroupStrategy) bool {
	return true
}
func (s *stubAdapter) Precheck(context.Context, UpgradeContext) (bool, string, error) {
	return s.ready, "", s.err
}
func (s *stubAdapter) BeginBatch(context.Context, UpgradeContext, []corev1.Node) error {
	return s.err
}
func (s *stubAdapter) PollBatch(context.Context, UpgradeContext, []corev1.Node) ([]NodeResult, error) {
	return nil, s.err
}
func (s *stubAdapter) Verify(context.Context, UpgradeContext) (bool, string, error) {
	return s.verified, "", s.err
}

func operationCount(t *testing.T, provider, operation, outcome string) float64 {
	t.Helper()
	return testutil.ToFloat64(obs.AdapterOperationTotal.WithLabelValues(provider, operation, outcome))
}

func TestInstrumentedAdapter_OutcomeClassification(t *testing.T) {
	obs.AdapterOperationTotal.Reset()
	obs.AdapterOperationDuration.Reset()
	t.Cleanup(func() {
		obs.AdapterOperationTotal.Reset()
		obs.AdapterOperationDuration.Reset()
	})

	const p = "Generic"
	ctx := context.Background()
	boom := errors.New("boom")

	// Precheck: ready -> ok, not ready -> not_ready, err -> error.
	_, _, _ = instrument(&stubAdapter{ready: true}).Precheck(ctx, UpgradeContext{})
	_, _, _ = instrument(&stubAdapter{ready: false}).Precheck(ctx, UpgradeContext{})
	_, _, _ = instrument(&stubAdapter{err: boom}).Precheck(ctx, UpgradeContext{})
	if got := operationCount(t, p, "Precheck", "ok"); got != 1 {
		t.Errorf("Precheck ok = %v, want 1", got)
	}
	if got := operationCount(t, p, "Precheck", "not_ready"); got != 1 {
		t.Errorf("Precheck not_ready = %v, want 1", got)
	}
	if got := operationCount(t, p, "Precheck", "error"); got != 1 {
		t.Errorf("Precheck error = %v, want 1", got)
	}

	// BeginBatch / PollBatch: only ok vs error.
	_ = instrument(&stubAdapter{}).BeginBatch(ctx, UpgradeContext{}, nil)
	_ = instrument(&stubAdapter{err: boom}).BeginBatch(ctx, UpgradeContext{}, nil)
	if got := operationCount(t, p, "BeginBatch", "ok"); got != 1 {
		t.Errorf("BeginBatch ok = %v, want 1", got)
	}
	if got := operationCount(t, p, "BeginBatch", "error"); got != 1 {
		t.Errorf("BeginBatch error = %v, want 1", got)
	}

	// Verify: verified -> ok, not verified -> not_ready.
	_, _, _ = instrument(&stubAdapter{verified: true}).Verify(ctx, UpgradeContext{})
	_, _, _ = instrument(&stubAdapter{verified: false}).Verify(ctx, UpgradeContext{})
	if got := operationCount(t, p, "Verify", "ok"); got != 1 {
		t.Errorf("Verify ok = %v, want 1", got)
	}
	if got := operationCount(t, p, "Verify", "not_ready"); got != 1 {
		t.Errorf("Verify not_ready = %v, want 1", got)
	}

	// A duration observation is recorded for every call above (8 total).
	if n := testutil.CollectAndCount(obs.AdapterOperationDuration); n == 0 {
		t.Errorf("expected duration histogram series to have been recorded")
	}
}

func TestInstrumentedAdapter_PassesResultsThrough(t *testing.T) {
	obs.AdapterOperationTotal.Reset()
	t.Cleanup(func() { obs.AdapterOperationTotal.Reset() })

	boom := errors.New("boom")
	a := instrument(&stubAdapter{ready: true, verified: true, err: boom})

	ready, _, err := a.Precheck(context.Background(), UpgradeContext{})
	if !ready || !errors.Is(err, boom) {
		t.Errorf("Precheck should pass the inner (ready=%v, err=%v) straight through, got (%v, %v)", true, boom, ready, err)
	}
}

func TestRegistry_RegisterInstrumentsButKeepsTypeAndStrategy(t *testing.T) {
	obs.AdapterOperationTotal.Reset()
	t.Cleanup(func() { obs.AdapterOperationTotal.Reset() })

	reg := NewRegistry()
	reg.Register(&stubAdapter{ready: true})

	got, ok := reg.Get(upgradev1alpha1.ProviderGeneric)
	if !ok {
		t.Fatal("expected the adapter to be registered")
	}
	if _, isInstrumented := got.(*instrumentedAdapter); !isInstrumented {
		t.Errorf("expected Get to return an *instrumentedAdapter, got %T", got)
	}
	if got.Type() != upgradev1alpha1.ProviderGeneric {
		t.Errorf("Type() should pass through, got %v", got.Type())
	}
	if !got.SupportsStrategy(upgradev1alpha1.StrategyInPlace) {
		t.Errorf("SupportsStrategy() should pass through")
	}

	// Registering an already-instrumented adapter must not double-wrap.
	reg.Register(got)
	again, _ := reg.Get(upgradev1alpha1.ProviderGeneric)
	if inner := again.(*instrumentedAdapter).inner; inner != got.(*instrumentedAdapter).inner {
		t.Errorf("double-registration should not re-wrap")
	}
}
