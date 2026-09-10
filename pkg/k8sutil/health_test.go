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

package k8sutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func cpNode(name string, ready bool) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{ControlPlaneLabelKey: ""},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
		},
	}
}

func newHealthTestClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestCheckControlPlaneHealth(t *testing.T) {
	tests := []struct {
		name        string
		nodes       []client.Object
		wantHealthy bool
	}{
		{name: "no control-plane nodes (managed control plane)", nodes: nil, wantHealthy: true},
		{name: "single node, ready", nodes: []client.Object{cpNode("cp-1", true)}, wantHealthy: true},
		{name: "3 nodes, 2 ready - quorum met", nodes: []client.Object{cpNode("cp-1", true), cpNode("cp-2", true), cpNode("cp-3", false)}, wantHealthy: true},
		{name: "3 nodes, 1 ready - quorum lost", nodes: []client.Object{cpNode("cp-1", true), cpNode("cp-2", false), cpNode("cp-3", false)}, wantHealthy: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newHealthTestClient(tt.nodes...)
			health, err := CheckControlPlaneHealth(context.Background(), c)
			if err != nil {
				t.Fatalf("CheckControlPlaneHealth: %v", err)
			}
			if health.Healthy != tt.wantHealthy {
				t.Errorf("Healthy = %v, want %v (reason: %s)", health.Healthy, tt.wantHealthy, health.Reason)
			}
		})
	}
}

// These tests exercise checkEtcdHealthzEndpoints directly (the
// testable core), not the public CheckEtcdQuorumViaAPIServers wrapper -
// that split is what lets this test use a plain httptest.NewServer
// (a real, goroutine-backed HTTP server, genuinely listening on a random
// local port) instead of needing a fake rest.Config/TLS setup. So this is
// a real HTTP round-trip, closer to production behavior than most of our
// other unit tests get to be, without any cluster/envtest needed.

func healthzServer(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestCheckEtcdHealthzEndpoints_MajorityHealthy(t *testing.T) {
	healthyA := healthzServer(t, http.StatusOK)
	healthyB := healthzServer(t, http.StatusOK)
	unhealthy := healthzServer(t, http.StatusInternalServerError)

	status := checkEtcdHealthzEndpoints(context.Background(), http.DefaultClient, []string{healthyA, healthyB, unhealthy})

	if !status.Healthy {
		t.Errorf("expected healthy with a 2/3 majority, got %+v", status)
	}
	if status.HealthyMembers != 2 || status.TotalMembers != 3 {
		t.Errorf("got HealthyMembers=%d TotalMembers=%d, want 2 and 3", status.HealthyMembers, status.TotalMembers)
	}
}

func TestCheckEtcdHealthzEndpoints_NoQuorum(t *testing.T) {
	// Same 3-member shape as the majority case, but only 1 healthy - this
	// is the actual safety-relevant case: it's what stops the controller
	// from advancing to the next control-plane node.
	healthy := healthzServer(t, http.StatusOK)
	unhealthyA := healthzServer(t, http.StatusInternalServerError)
	unhealthyB := healthzServer(t, http.StatusInternalServerError)

	status := checkEtcdHealthzEndpoints(context.Background(), http.DefaultClient, []string{healthy, unhealthyA, unhealthyB})

	if status.Healthy {
		t.Errorf("expected unhealthy with only 1/3 reporting healthy, got %+v", status)
	}
	if status.Reason == "" {
		t.Errorf("expected a reason explaining the lost quorum")
	}
}

func TestCheckEtcdHealthzEndpoints_UnreachableCountsAsUnhealthy(t *testing.T) {
	// Nothing listens on this address. A network error (as opposed to a
	// non-200 response) must still count against quorum, not be silently
	// skipped - we have zero evidence that apiserver is fine, so it must
	// never count toward the majority. Failing open here would be exactly
	// the kind of "good enough" gap this whole review has been about
	// closing.
	status := checkEtcdHealthzEndpoints(context.Background(), http.DefaultClient, []string{"http://127.0.0.1:1"})

	if status.Healthy {
		t.Errorf("expected an unreachable endpoint to count as unhealthy, got %+v", status)
	}
	if status.HealthyMembers != 0 || status.TotalMembers != 1 {
		t.Errorf("got HealthyMembers=%d TotalMembers=%d, want 0 and 1", status.HealthyMembers, status.TotalMembers)
	}
}

func TestCheckEtcdHealthzEndpoints_NoEndpointsIsTriviallyHealthy(t *testing.T) {
	// Mirrors CheckControlPlaneHealth's "no control-plane nodes found"
	// case: a managed control plane (EKS/LKE) has nothing for this check
	// to gate on, so it must not block anything.
	status := checkEtcdHealthzEndpoints(context.Background(), http.DefaultClient, nil)

	if !status.Healthy {
		t.Errorf("expected no endpoints to check to be trivially healthy, got %+v", status)
	}
}

func TestGetRunningEtcdVersion(t *testing.T) {
	tests := []struct {
		name    string
		pods    []client.Object
		want    string
		wantErr bool
	}{
		{
			name: "strips the Kubernetes build revision suffix and adds v prefix",
			pods: []client.Object{&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "etcd-master", Namespace: "kube-system", Labels: map[string]string{"component": "etcd"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Image: "registry.k8s.io/etcd:3.5.24-0"}}},
			}},
			want: "v3.5.24",
		},
		{
			name:    "no etcd pods found",
			pods:    nil,
			wantErr: true,
		},
		{
			name: "image with no tag at all",
			pods: []client.Object{&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "etcd-master", Namespace: "kube-system", Labels: map[string]string{"component": "etcd"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Image: "registry.k8s.io/etcd"}}},
			}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.pods...).Build()

			got, err := GetRunningEtcdVersion(context.Background(), c)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
