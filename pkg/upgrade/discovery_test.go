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

package upgrade

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
)

func node(name string, labels, annotations map[string]string, providerID string) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.NodeSpec{ProviderID: providerID},
	}
}

func TestDiscoverGroups_Classification(t *testing.T) {
	tests := []struct {
		name          string
		node          corev1.Node
		wantRole      upgradev1alpha1.NodeGroupRole
		wantProvider  upgradev1alpha1.ProviderType
		wantGroup     string
		wantHeuristic bool
		checkRef      func(t *testing.T, ref *upgradev1alpha1.ProviderRef)
	}{
		{
			name:          "kubeadm control-plane node",
			node:          node("cp-1", map[string]string{k8sutil.ControlPlaneLabelKey: ""}, nil, ""),
			wantRole:      upgradev1alpha1.RoleControlPlane,
			wantProvider:  upgradev1alpha1.ProviderKubeadm,
			wantGroup:     "control-plane",
			wantHeuristic: false,
		},
		{
			name:          "kubeadm worker node",
			node:          node("worker-1", nil, nil, ""),
			wantRole:      upgradev1alpha1.RoleWorker,
			wantProvider:  upgradev1alpha1.ProviderKubeadm,
			wantGroup:     "workers",
			wantHeuristic: false,
		},
		{
			name: "EKS managed node group worker",
			node: node("eks-1",
				map[string]string{EKSNodeGroupLabel: "ng-1", EKSClusterNameLabel: "my-cluster"},
				nil,
				"aws:///us-east-1a/i-0123456789abcdef0"),
			wantRole:      upgradev1alpha1.RoleWorker,
			wantProvider:  upgradev1alpha1.ProviderAWSEKSManagedNodeGroup,
			wantGroup:     "ng-1",
			wantHeuristic: false,
			checkRef: func(t *testing.T, ref *upgradev1alpha1.ProviderRef) {
				if ref == nil || ref.AWSEKS == nil {
					t.Fatalf("expected AWSEKS providerRef, got %+v", ref)
				}
				if ref.AWSEKS.NodeGroupName != "ng-1" || ref.AWSEKS.ClusterName != "my-cluster" || ref.AWSEKS.Region != "us-east-1" {
					t.Fatalf("unexpected AWSEKS ref: %+v", ref.AWSEKS)
				}
			},
		},
		{
			name:          "AWS self-managed ASG worker (no EKS label)",
			node:          node("asg-1", nil, nil, "aws:///us-west-2b/i-0fedcba9876543210"),
			wantRole:      upgradev1alpha1.RoleWorker,
			wantProvider:  upgradev1alpha1.ProviderAWSAutoScalingGroup,
			wantGroup:     "workers",
			wantHeuristic: true,
			checkRef: func(t *testing.T, ref *upgradev1alpha1.ProviderRef) {
				if ref == nil || ref.AWSASG == nil || ref.AWSASG.Region != "us-west-2" {
					t.Fatalf("unexpected AWSASG ref: %+v", ref)
				}
			},
		},
		{
			name: "Linode LKE worker",
			node: node("lke-1",
				map[string]string{LinodeLKEPoolLabel: "pool-1", LinodeLKEClusterIDLabel: "12345"},
				nil,
				"linode://98765"),
			wantRole:      upgradev1alpha1.RoleWorker,
			wantProvider:  upgradev1alpha1.ProviderLinodeLKE,
			wantGroup:     "pool-1",
			wantHeuristic: false,
			checkRef: func(t *testing.T, ref *upgradev1alpha1.ProviderRef) {
				if ref == nil || ref.LinodeLKE == nil || ref.LinodeLKE.PoolID != "pool-1" || ref.LinodeLKE.ClusterID != "12345" {
					t.Fatalf("unexpected LinodeLKE ref: %+v", ref)
				}
			},
		},
		{
			name:          "unrecognized providerID falls back to Generic",
			node:          node("mystery-1", nil, nil, "somecloud://abc123"),
			wantRole:      upgradev1alpha1.RoleWorker,
			wantProvider:  upgradev1alpha1.ProviderGeneric,
			wantGroup:     "workers",
			wantHeuristic: false,
		},
		{
			name: "provider-override annotation wins over providerID heuristics",
			node: node("override-1",
				nil,
				map[string]string{ProviderOverrideAnnotation: "Kubeadm"},
				"aws:///us-east-1a/i-0123456789abcdef0"),
			wantRole:      upgradev1alpha1.RoleWorker,
			wantProvider:  upgradev1alpha1.ProviderKubeadm,
			wantGroup:     "workers",
			wantHeuristic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups, err := DiscoverGroups([]corev1.Node{tt.node}, nil)
			if err != nil {
				t.Fatalf("DiscoverGroups: %v", err)
			}
			if len(groups) != 1 {
				t.Fatalf("got %d groups, want 1: %+v", len(groups), groups)
			}
			g := groups[0]
			if g.Role != tt.wantRole {
				t.Errorf("Role = %v, want %v", g.Role, tt.wantRole)
			}
			if g.Provider != tt.wantProvider {
				t.Errorf("Provider = %v, want %v", g.Provider, tt.wantProvider)
			}
			if g.Name != tt.wantGroup {
				t.Errorf("Name = %v, want %v", g.Name, tt.wantGroup)
			}
			if g.Heuristic != tt.wantHeuristic {
				t.Errorf("Heuristic = %v, want %v", g.Heuristic, tt.wantHeuristic)
			}
			if tt.checkRef != nil {
				tt.checkRef(t, g.ProviderRef)
			}
		})
	}
}

func TestDiscoverGroups_GroupsMultipleNodesTogether(t *testing.T) {
	nodes := []corev1.Node{
		node("cp-1", map[string]string{k8sutil.ControlPlaneLabelKey: ""}, nil, ""),
		node("cp-2", map[string]string{k8sutil.ControlPlaneLabelKey: ""}, nil, ""),
		node("eks-b", map[string]string{EKSNodeGroupLabel: "ng-1"}, nil, "aws:///us-east-1a/i-2"),
		node("eks-a", map[string]string{EKSNodeGroupLabel: "ng-1"}, nil, "aws:///us-east-1a/i-1"),
	}

	groups, err := DiscoverGroups(nodes, nil)
	if err != nil {
		t.Fatalf("DiscoverGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(groups), groups)
	}

	byName := map[string]DiscoveredGroup{}
	for _, g := range groups {
		byName[g.Name] = g
	}

	cp, ok := byName["control-plane"]
	if !ok {
		t.Fatalf("expected a control-plane group, got %+v", byName)
	}
	if !reflect.DeepEqual(cp.Nodes, []string{"cp-1", "cp-2"}) {
		t.Errorf("control-plane nodes = %v, want [cp-1 cp-2]", cp.Nodes)
	}

	ng1, ok := byName["ng-1"]
	if !ok {
		t.Fatalf("expected an ng-1 group, got %+v", byName)
	}
	if !reflect.DeepEqual(ng1.Nodes, []string{"eks-a", "eks-b"}) {
		t.Errorf("ng-1 nodes = %v, want [eks-a eks-b] (sorted)", ng1.Nodes)
	}
}

func TestDiscoverGroups_ScopeFiltersNodes(t *testing.T) {
	nodes := []corev1.Node{
		node("in-scope", map[string]string{"env": "prod"}, nil, ""),
		node("out-of-scope", map[string]string{"env": "staging"}, nil, ""),
	}
	scope := &upgradev1alpha1.UpgradeScope{
		NodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"env": "prod"}},
	}

	groups, err := DiscoverGroups(nodes, scope)
	if err != nil {
		t.Fatalf("DiscoverGroups: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Nodes) != 1 || groups[0].Nodes[0] != "in-scope" {
		t.Fatalf("got %+v, want exactly node in-scope", groups)
	}
}
