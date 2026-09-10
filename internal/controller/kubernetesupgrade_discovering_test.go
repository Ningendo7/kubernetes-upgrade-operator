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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/upgrade"
)

func TestBuildDesiredChild_HeuristicGroupFailsClosed(t *testing.T) {
	ku := &upgradev1alpha1.KubernetesUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ku-1", Namespace: "default"},
	}
	group := upgrade.DiscoveredGroup{
		Name:      "ng-1",
		Role:      upgradev1alpha1.RoleWorker,
		Provider:  upgradev1alpha1.ProviderAWSAutoScalingGroup,
		Heuristic: true,
		Nodes:     []string{"node-1"},
	}

	child := buildDesiredChild(ku, group, "v1.30.0", nil, nil)

	if !child.Spec.Paused {
		t.Errorf("expected a heuristically-classified group with no override to be paused by default")
	}
	if reason := child.Annotations[pausedReasonAnnotation]; reason == "" {
		t.Errorf("expected a paused-reason annotation explaining why")
	}
}

func TestBuildDesiredChild_HeuristicGroupUnpausedByExplicitStrategyOverride(t *testing.T) {
	ku := &upgradev1alpha1.KubernetesUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ku-1", Namespace: "default"},
	}
	group := upgrade.DiscoveredGroup{
		Name:      "ng-1",
		Role:      upgradev1alpha1.RoleWorker,
		Provider:  upgradev1alpha1.ProviderAWSAutoScalingGroup,
		Heuristic: true,
		Nodes:     []string{"node-1"},
	}
	strategy := upgradev1alpha1.StrategyReplace
	override := &upgradev1alpha1.NodeGroupOverride{
		GroupName: "ng-1",
		Strategy:  &strategy,
	}

	child := buildDesiredChild(ku, group, "v1.30.0", override, nil)

	if child.Spec.Paused {
		t.Errorf("expected an explicit groupOverrides[].strategy to count as confirmation, not stay paused")
	}
	if _, ok := child.Annotations[pausedReasonAnnotation]; ok {
		t.Errorf("expected no paused-reason annotation once explicitly confirmed")
	}
}

func TestBuildDesiredChild_UnrelatedOverrideDoesNotConfirmHeuristic(t *testing.T) {
	ku := &upgradev1alpha1.KubernetesUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ku-1", Namespace: "default"},
	}
	group := upgrade.DiscoveredGroup{
		Name:      "ng-1",
		Role:      upgradev1alpha1.RoleWorker,
		Provider:  upgradev1alpha1.ProviderAWSAutoScalingGroup,
		Heuristic: true,
		Nodes:     []string{"node-1"},
	}
	batchSize := int32(2)
	override := &upgradev1alpha1.NodeGroupOverride{
		GroupName: "ng-1",
		BatchSize: &batchSize, // unrelated to the provider/strategy guess
	}

	child := buildDesiredChild(ku, group, "v1.30.0", override, nil)

	if !child.Spec.Paused {
		t.Errorf("expected an unrelated override field to NOT count as confirming the heuristic classification")
	}
}

func TestBuildDesiredChild_NonHeuristicGroupNotPaused(t *testing.T) {
	ku := &upgradev1alpha1.KubernetesUpgrade{
		ObjectMeta: metav1.ObjectMeta{Name: "ku-1", Namespace: "default"},
	}
	group := upgrade.DiscoveredGroup{
		Name:     "control-plane",
		Role:     upgradev1alpha1.RoleControlPlane,
		Provider: upgradev1alpha1.ProviderKubeadm,
		Nodes:    []string{"cp-1"},
	}

	child := buildDesiredChild(ku, group, "v1.30.0", nil, nil)

	if child.Spec.Paused {
		t.Errorf("expected a confidently-classified group to not be paused")
	}
}
