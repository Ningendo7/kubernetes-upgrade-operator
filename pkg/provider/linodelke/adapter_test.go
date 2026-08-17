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
	"errors"
	"testing"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

func TestAdapter_ReportsNotImplemented(t *testing.T) {
	a := &Adapter{}
	ctx := context.Background()
	uc := provider.UpgradeContext{}

	if a.Type() != upgradev1alpha1.ProviderLinodeLKE {
		t.Errorf("unexpected Type(): %v", a.Type())
	}
	if !a.SupportsStrategy(upgradev1alpha1.StrategyReplace) {
		t.Errorf("expected Replace to be supported")
	}
	if a.SupportsStrategy(upgradev1alpha1.StrategyInPlace) {
		t.Errorf("expected InPlace to not be supported")
	}

	if _, _, err := a.Precheck(ctx, uc); !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("Precheck: got %v, want ErrNotImplemented", err)
	}
	if err := a.BeginBatch(ctx, uc, nil); !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("BeginBatch: got %v, want ErrNotImplemented", err)
	}
	if _, err := a.PollBatch(ctx, uc, nil); !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("PollBatch: got %v, want ErrNotImplemented", err)
	}
	if _, _, err := a.Verify(ctx, uc); !errors.Is(err, provider.ErrNotImplemented) {
		t.Errorf("Verify: got %v, want ErrNotImplemented", err)
	}
}
