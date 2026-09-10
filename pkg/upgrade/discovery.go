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
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
)

// DiscoveredGroup is one logical node group computed from live cluster state.
type DiscoveredGroup struct {
	Name        string
	Role        upgradev1alpha1.NodeGroupRole
	Provider    upgradev1alpha1.ProviderType
	ProviderRef *upgradev1alpha1.ProviderRef
	Heuristic   bool
	Nodes       []string
}

// DiscoverGroups classifies nodes into logical NodeGroupUpgrade groups.
// scope, if non-nil, limits which nodes are considered.
func DiscoverGroups(nodes []corev1.Node, scope *upgradev1alpha1.UpgradeScope) ([]DiscoveredGroup, error) {
	selector := labels.Everything()
	if scope != nil && scope.NodeSelector != nil {
		s, err := metav1.LabelSelectorAsSelector(scope.NodeSelector)
		if err != nil {
			return nil, fmt.Errorf("parsing scope.nodeSelector: %w", err)
		}
		selector = s
	}

	groups := map[string]*DiscoveredGroup{}

	for i := range nodes {
		node := &nodes[i]
		if !selector.Matches(labels.Set(node.Labels)) {
			continue
		}

		role := classifyRole(node)
		provider, ref, heuristic := classifyProvider(node)
		name := groupName(role, provider, node)

		g, ok := groups[name]
		if !ok {
			g = &DiscoveredGroup{
				Name:        name,
				Role:        role,
				Provider:    provider,
				ProviderRef: ref,
			}
			groups[name] = g
		}
		g.Heuristic = g.Heuristic || heuristic
		g.Nodes = append(g.Nodes, node.Name)
	}

	result := make([]DiscoveredGroup, 0, len(groups))
	for _, g := range groups {
		sort.Strings(g.Nodes)
		result = append(result, *g)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func classifyRole(node *corev1.Node) upgradev1alpha1.NodeGroupRole {
	if _, ok := node.Labels[k8sutil.ControlPlaneLabelKey]; ok {
		return upgradev1alpha1.RoleControlPlane
	}
	if _, ok := node.Labels[k8sutil.LegacyControlPlaneLabelKey]; ok {
		return upgradev1alpha1.RoleControlPlane
	}
	return upgradev1alpha1.RoleWorker
}

func classifyProvider(node *corev1.Node) (upgradev1alpha1.ProviderType, *upgradev1alpha1.ProviderRef, bool) {
	if override, ok := node.Annotations[ProviderOverrideAnnotation]; ok {
		if p := upgradev1alpha1.ProviderType(override); isKnownProvider(p) {
			return p, nil, false
		}
	}

	providerID := node.Spec.ProviderID

	if strings.HasPrefix(providerID, "aws:///") {
		if ng, ok := node.Labels[EKSNodeGroupLabel]; ok {
			return upgradev1alpha1.ProviderAWSEKSManagedNodeGroup, &upgradev1alpha1.ProviderRef{
				AWSEKS: &upgradev1alpha1.AWSEKSNodeGroupRef{
					NodeGroupName: ng,
					ClusterName:   node.Labels[EKSClusterNameLabel],
					Region:        regionFromAWSProviderID(providerID),
				},
			}, false
		}
		// Ambiguous: could be a real self-managed ASG, or a plain EC2
		// instance manually kubeadm-joined with the AWS cloud provider
		// integration enabled (which also sets providerID). We can't tell
		// these apart from Node metadata alone.
		return upgradev1alpha1.ProviderAWSAutoScalingGroup, &upgradev1alpha1.ProviderRef{
			AWSASG: &upgradev1alpha1.AWSAutoScalingGroupRef{
				Region: regionFromAWSProviderID(providerID),
			},
		}, true
	}

	if strings.HasPrefix(providerID, "linode://") {
		if pool, ok := node.Labels[LinodeLKEPoolLabel]; ok {
			return upgradev1alpha1.ProviderLinodeLKE, &upgradev1alpha1.ProviderRef{
				LinodeLKE: &upgradev1alpha1.LinodeLKEPoolRef{
					PoolID:    pool,
					ClusterID: node.Labels[LinodeLKEClusterIDLabel],
				},
			}, false
		}
	}

	if providerID == "" {
		return upgradev1alpha1.ProviderKubeadm, nil, false
	}

	return upgradev1alpha1.ProviderGeneric, nil, false
}

func isKnownProvider(p upgradev1alpha1.ProviderType) bool {
	switch p {
	case upgradev1alpha1.ProviderKubeadm,
		upgradev1alpha1.ProviderAWSEKSManagedNodeGroup,
		upgradev1alpha1.ProviderAWSAutoScalingGroup,
		upgradev1alpha1.ProviderLinodeLKE,
		upgradev1alpha1.ProviderGeneric:
		return true
	default:
		return false

	}
}

func groupName(role upgradev1alpha1.NodeGroupRole, provider upgradev1alpha1.ProviderType, node *corev1.Node) string {
	if role == upgradev1alpha1.RoleControlPlane {
		return ControlPlaneGroupName
	}
	switch provider {
	case upgradev1alpha1.ProviderAWSEKSManagedNodeGroup:
		return sanitizeGroupName(node.Labels[EKSNodeGroupLabel])
	case upgradev1alpha1.ProviderLinodeLKE:
		return sanitizeGroupName(node.Labels[LinodeLKEPoolLabel])
	default:
		return defaultWorkerGroupName
	}
}

func sanitizeGroupName(s string) string {
	if s == "" {
		return defaultWorkerGroupName
	}
	return s
}

// regionFromAWSProviderID extracts the AWS region from a providerID of the
// form "aws:///us-east-1a/i-0123456789abcdef0".
func regionFromAWSProviderID(providerID string) string {
	parts := strings.Split(strings.TrimPrefix(providerID, "aws:///"), "/")
	if len(parts) == 0 || len(parts[0]) < 2 {
		return ""
	}
	az := parts[0]
	return az[:len(az)-1] // drop the trailing zone letter, e.g. "us-east-1a" -> "us-east-1"
}

// GroupCurrentVersion returns the oldest kubelet version reported among
// group's own member nodes, using nodes as the authoritative source of
// live Node objects. This is deliberately per-group, not cluster-wide: the
// apiserver's own version (what the parent KubernetesUpgrade's StepPlan is
// computed from) is not necessarily this group's actual current version -
// e.g. this group started behind the rest of the cluster, or a previous
// upgrade paused partway through. Taking the oldest node in the group
// (rather than the newest, or an arbitrary one) is the conservative choice
// if the group is itself internally mixed-version.
func GroupCurrentVersion(nodes []corev1.Node, group DiscoveredGroup) (string, error) {
	byName := make(map[string]string, len(nodes))
	for i := range nodes {
		byName[nodes[i].Name] = nodes[i].Status.NodeInfo.KubeletVersion
	}

	var oldest string
	for _, name := range group.Nodes {
		v := byName[name]
		if v == "" {
			continue
		}
		if oldest == "" {
			oldest = v
			continue
		}
		cmp, err := k8sutil.CompareVersions(v, oldest)
		if err != nil {
			return "", fmt.Errorf("comparing versions for group %q: %w", group.Name, err)
		}
		if cmp < 0 {
			oldest = v
		}
	}
	if oldest == "" {
		return "", fmt.Errorf("group %q has no member nodes with a known kubelet version", group.Name)
	}
	return oldest, nil
}

// OldestVersionAcrossGroups returns the oldest kubelet version reported
// across ALL of groups' member nodes combined. This is what a
// KubernetesUpgrade's step plan should actually be computed from - the
// apiserver's own version is not necessarily any specific groups actual
// version (see GroupCurrentVersion), and is not necessarily the oldest
// thing in the fleet either: a group that started behind the rest of the
// cluster is exactly the case that must not be missed here, or a
// KubernetesUpgrade whose target already matches the apiservers version
// would short-circuit straight to Complete without ever looking at a
// lagging group at all. A group that cannot report a version (no member
// nodes with known kubelet versions) is skipped rather than failing the
// whole computation.
func OldestVersionAcrossGroups(nodes []corev1.Node, groups []DiscoveredGroup) (string, error) {
	var oldest string
	for _, g := range groups {
		v, err := GroupCurrentVersion(nodes, g)
		if err != nil {
			continue
		}
		if oldest == "" {
			oldest = v
			continue
		}
		cmp, err := k8sutil.CompareVersions(v, oldest)
		if err != nil {
			return "", fmt.Errorf("comparing versions across groups: %w", err)
		}
		if cmp < 0 {
			oldest = v
		}
	}
	if oldest == "" {
		return "", fmt.Errorf("no groups with a known kubelet version")
	}
	return oldest, nil
}
