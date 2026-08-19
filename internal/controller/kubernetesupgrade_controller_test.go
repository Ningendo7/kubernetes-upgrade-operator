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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/upgrade"
)

var _ = Describe("KubernetesUpgrade Controller", func() {
	Context("When discovering node groups across different providers", func() {
		const namespace = "default"

		ctx := context.Background()

		It("classifies nodes and creates one NodeGroupUpgrade child per discovered group", func() {
			// Compute a valid, one-minor-ahead target relative to the real
			// envtest apiserver's own reported version, so this test
			// doesn't silently break if the pinned envtest version changes.
			discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
			serverVersion, err := discoveryClient.ServerVersion()
			Expect(err).NotTo(HaveOccurred())
			current, err := k8sutil.ParseVersion(serverVersion.GitVersion)
			Expect(err).NotTo(HaveOccurred())
			targetVersion := fmt.Sprintf("v%d.%d.0", int64(current.Major()), int64(current.Minor())+1)

			readyStatus := corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: serverVersion.GitVersion},
			}

			nodes := []*corev1.Node{
				{
					// Empty providerID + control-plane label -> Kubeadm, ControlPlane.
					ObjectMeta: metav1.ObjectMeta{
						Name:   "discovery-cp-1",
						Labels: map[string]string{k8sutil.ControlPlaneLabelKey: ""},
					},
				},
				{
					// Empty providerID, no labels -> Kubeadm, Worker, group "workers".
					ObjectMeta: metav1.ObjectMeta{Name: "discovery-worker-1"},
				},
				{
					// AWS providerID + EKS nodegroup label -> AWSEKSManagedNodeGroup, group "ng-1".
					ObjectMeta: metav1.ObjectMeta{
						Name:   "discovery-eks-1",
						Labels: map[string]string{upgrade.EKSNodeGroupLabel: "ng-1"},
					},
					Spec: corev1.NodeSpec{ProviderID: "aws:///us-east-1a/i-0123456789abcdef0"},
				},
			}

			for _, n := range nodes {
				Expect(k8sClient.Create(ctx, n)).To(Succeed())
				n.Status = readyStatus
				Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())
			}
			DeferCleanup(func() {
				for _, n := range nodes {
					Expect(k8sClient.Delete(ctx, n)).To(Succeed())
				}
			})

			ku := &upgradev1alpha1.KubernetesUpgrade{
				ObjectMeta: metav1.ObjectMeta{Name: "discovery-test", Namespace: namespace},
				Spec:       upgradev1alpha1.KubernetesUpgradeSpec{TargetVersion: targetVersion},
			}
			Expect(k8sClient.Create(ctx, ku)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, ku)).To(Succeed())
			})

			key := types.NamespacedName{Name: ku.Name, Namespace: namespace}

			By("waiting for discovery to populate status.discoveredGroups")
			Eventually(func() int {
				var got upgradev1alpha1.KubernetesUpgrade
				if err := k8sClient.Get(ctx, key, &got); err != nil {
					return 0
				}
				return len(got.Status.DiscoveredGroups)
			}, 30*time.Second, 250*time.Millisecond).Should(Equal(3))

			var final upgradev1alpha1.KubernetesUpgrade
			Expect(k8sClient.Get(ctx, key, &final)).To(Succeed())

			byName := map[string]upgradev1alpha1.DiscoveredGroupStatus{}
			for _, g := range final.Status.DiscoveredGroups {
				byName[g.Name] = g
			}

			cp, ok := byName["control-plane"]
			Expect(ok).To(BeTrue(), "expected a control-plane group")
			Expect(cp.Provider).To(Equal(upgradev1alpha1.ProviderKubeadm))
			Expect(cp.Role).To(Equal(upgradev1alpha1.RoleControlPlane))
			Expect(cp.Strategy).To(Equal(upgradev1alpha1.StrategyInPlace))

			workers, ok := byName["workers"]
			Expect(ok).To(BeTrue(), "expected a workers group")
			Expect(workers.Provider).To(Equal(upgradev1alpha1.ProviderKubeadm))
			Expect(workers.Strategy).To(Equal(upgradev1alpha1.StrategyInPlace))

			eks, ok := byName["ng-1"]
			Expect(ok).To(BeTrue(), "expected an ng-1 group")
			Expect(eks.Provider).To(Equal(upgradev1alpha1.ProviderAWSEKSManagedNodeGroup))
			Expect(eks.Strategy).To(Equal(upgradev1alpha1.StrategyReplace))

			By("confirming a matching NodeGroupUpgrade child exists for each group")
			var children upgradev1alpha1.NodeGroupUpgradeList
			Expect(k8sClient.List(ctx, &children,
				client.InNamespace(namespace),
				client.MatchingLabels{parentLabelKey: ku.Name},
			)).To(Succeed())
			Expect(children.Items).To(HaveLen(3))
		})
	})
})
