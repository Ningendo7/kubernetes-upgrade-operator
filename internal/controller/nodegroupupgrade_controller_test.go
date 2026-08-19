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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

var _ = Describe("NodeGroupUpgrade Controller", func() {
	Context("When a group has one node that is already healthy at the target version", func() {
		const (
			nodeName  = "envtest-worker-1"
			groupName = "test-group"
			namespace = "default"
			toVersion = "v1.30.0"
		)

		ctx := context.Background()

		It("drains, upgrades (via the fake adapter), verifies, and completes", func() {
			By("creating a synthetic Node already reporting the target version")
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())

			node.Status = corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
				NodeInfo: corev1.NodeSystemInfo{KubeletVersion: toVersion},
			}
			Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, node)).To(Succeed())
			})

			By("creating a NodeGroupUpgrade for that node")
			group := &upgradev1alpha1.NodeGroupUpgrade{
				ObjectMeta: metav1.ObjectMeta{Name: groupName, Namespace: namespace},
				Spec: upgradev1alpha1.NodeGroupUpgradeSpec{
					TargetVersion: toVersion,
					Role:          upgradev1alpha1.RoleWorker,
					Provider:      upgradev1alpha1.ProviderKubeadm,
					Strategy:      upgradev1alpha1.StrategyInPlace,
					Nodes:         []string{nodeName},
				},
			}
			Expect(k8sClient.Create(ctx, group)).To(Succeed())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, group)).To(Succeed())
			})

			key := types.NamespacedName{Name: groupName, Namespace: namespace}

			By("waiting for the group to reach Complete")
			Eventually(func() upgradev1alpha1.NodeGroupUpgradePhase {
				var got upgradev1alpha1.NodeGroupUpgrade
				if err := k8sClient.Get(ctx, key, &got); err != nil {
					return ""
				}
				return got.Status.Phase
			}, 30*time.Second, 250*time.Millisecond).Should(Equal(upgradev1alpha1.NGComplete))

			var final upgradev1alpha1.NodeGroupUpgrade
			Expect(k8sClient.Get(ctx, key, &final)).To(Succeed())
			Expect(final.Status.UpgradedNodes).To(Equal(int32(1)))
			Expect(final.Status.NodeProgress).To(HaveLen(1))
			Expect(final.Status.NodeProgress[0].CompletedAt).NotTo(BeNil())

			By("confirming the node was uncordoned again")
			var finalNode corev1.Node
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, &finalNode)).To(Succeed())
			Expect(finalNode.Spec.Unschedulable).To(BeFalse())
		})
	})
})
