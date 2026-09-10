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
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newLeaseTestReconciler(objs ...client.Object) *KubernetesUpgradeReconciler {
	scheme := runtime.NewScheme()
	_ = coordinationv1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &KubernetesUpgradeReconciler{Client: c, OperatorNamespace: "operator-ns"}
}

func getLease(t *testing.T, r *KubernetesUpgradeReconciler) coordinationv1.Lease {
	t.Helper()
	var lease coordinationv1.Lease
	key := client.ObjectKey{Namespace: "operator-ns", Name: upgradeLeaseName}
	if err := r.Get(context.Background(), key, &lease); err != nil {
		t.Fatalf("getting lease: %v", err)
	}
	return lease
}

func TestAcquireLease_FreshAcquire(t *testing.T) {
	r := newLeaseTestReconciler()
	acquired, err := r.acquireLease(context.Background(), "ns/upgrade-a")
	if err != nil {
		t.Fatalf("acquireLease: %v", err)
	}
	if !acquired {
		t.Fatalf("expected a fresh lease to be acquired")
	}
}

func TestAcquireLease_SameHolderRenews(t *testing.T) {
	r := newLeaseTestReconciler()
	ctx := context.Background()

	if acquired, err := r.acquireLease(ctx, "ns/upgrade-a"); err != nil || !acquired {
		t.Fatalf("first acquire: acquired=%v err=%v", acquired, err)
	}
	before := getLease(t, r)

	// A short real sleep so RenewTime measurably advances - acquireLease
	// uses metav1.NowMicro() (real wall-clock time) internally, with no
	// injectable clock, so this is the pragmatic way to observe renewal.
	time.Sleep(10 * time.Millisecond)

	if acquired, err := r.acquireLease(ctx, "ns/upgrade-a"); err != nil || !acquired {
		t.Fatalf("second acquire (renew): acquired=%v err=%v", acquired, err)
	}
	after := getLease(t, r)

	if !after.Spec.RenewTime.After(before.Spec.RenewTime.Time) {
		t.Errorf("expected RenewTime to advance on renewal: before=%v after=%v", before.Spec.RenewTime, after.Spec.RenewTime)
	}
}

func TestAcquireLease_DifferentHolderBlockedWhileFresh(t *testing.T) {
	r := newLeaseTestReconciler()
	ctx := context.Background()

	if acquired, err := r.acquireLease(ctx, "ns/upgrade-a"); err != nil || !acquired {
		t.Fatalf("first acquire: acquired=%v err=%v", acquired, err)
	}

	acquired, err := r.acquireLease(ctx, "ns/upgrade-b")
	if err != nil {
		t.Fatalf("acquireLease: %v", err)
	}
	if acquired {
		t.Errorf("expected a second holder to be blocked while the lease is still fresh")
	}
}

func TestAcquireLease_StaleLeaseIsReclaimed(t *testing.T) {
	staleTime := metav1.NewMicroTime(time.Now().Add(-2 * leaseDuration))
	holder := "ns/upgrade-a"
	existing := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: upgradeLeaseName, Namespace: "operator-ns"},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity: &holder,
			RenewTime:      &staleTime,
		},
	}
	r := newLeaseTestReconciler(existing)

	acquired, err := r.acquireLease(context.Background(), "ns/upgrade-b")
	if err != nil {
		t.Fatalf("acquireLease: %v", err)
	}
	if !acquired {
		t.Errorf("expected a stale (crashed-holder) lease to be reclaimable by a different holder")
	}
}

func TestReleaseLease_OnlyOwnerCanRelease(t *testing.T) {
	r := newLeaseTestReconciler()
	ctx := context.Background()

	if acquired, err := r.acquireLease(ctx, "ns/upgrade-a"); err != nil || !acquired {
		t.Fatalf("acquire: acquired=%v err=%v", acquired, err)
	}

	// A non-owner "releasing" is a silent no-op, not an error.
	if err := r.releaseLease(ctx, "ns/upgrade-b"); err != nil {
		t.Fatalf("releaseLease (not owner): %v", err)
	}
	_ = getLease(t, r) // still exists - would Fatalf inside getLease otherwise

	if err := r.releaseLease(ctx, "ns/upgrade-a"); err != nil {
		t.Fatalf("releaseLease (owner): %v", err)
	}
	var deleted coordinationv1.Lease
	key := client.ObjectKey{Namespace: "operator-ns", Name: upgradeLeaseName}
	if err := r.Get(ctx, key, &deleted); err == nil {
		t.Errorf("expected lease to be deleted after the actual owner released it")
	}
}
