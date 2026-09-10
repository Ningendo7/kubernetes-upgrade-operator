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

	Context("When a target is more than one minor version ahead", func() {
		const namespace = "default"

		ctx := context.Background()

		It("drives every hop to completion, including control-plane-before-workers sequencing", func() {
			discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
			serverVersion, err := discoveryClient.ServerVersion()
			Expect(err).NotTo(HaveOccurred())
			current, err := k8sutil.ParseVersion(serverVersion.GitVersion)
			Expect(err).NotTo(HaveOccurred())
			major := int64(current.Major())
			minor := int64(current.Minor())
			targetVersion := fmt.Sprintf("v%d.%d.0", major, minor+2) // 2 hops

			cpNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "cycle-cp-1",
					Labels: map[string]string{k8sutil.ControlPlaneLabelKey: ""},
				},
			}
			workerNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "cycle-worker-1"}}
			testNodes := []*corev1.Node{cpNode, workerNode}

			for _, n := range testNodes {
				Expect(k8sClient.Create(ctx, n)).To(Succeed())
				n.Status = corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
					NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: serverVersion.GitVersion},
				}
				Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())
			}
			DeferCleanup(func() {
				for _, n := range testNodes {
					Expect(k8sClient.Delete(ctx, n)).To(Succeed())
				}
			})

			ku := &upgradev1alpha1.KubernetesUpgrade{
				ObjectMeta: metav1.ObjectMeta{Name: "full-cycle-test", Namespace: namespace},
				Spec:       upgradev1alpha1.KubernetesUpgradeSpec{TargetVersion: targetVersion},
			}
			Expect(k8sClient.Create(ctx, ku)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, ku)).To(Succeed())
			})

			key := types.NamespacedName{Name: ku.Name, Namespace: namespace}

			// Nothing in this test simulates a real kubeadm/cloud upgrade -
			// the envtest suite's NodeGroupUpgrade controller uses a fake
			// adapter that always reports success instantly. But Verifying
			// still checks the real Node object's reported kubelet version
			// against the CURRENT hop's target, and nothing updates that
			// automatically. So this polling function does double duty:
			// it's both the Eventually condition AND the simulation of
			// "the adapter successfully upgraded the node," keeping both
			// synthetic nodes' reported version in sync with whichever hop
			// is currently active.
			Eventually(func(g Gomega) upgradev1alpha1.KubernetesUpgradePhase {
				var got upgradev1alpha1.KubernetesUpgrade
				g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

				if len(got.Status.StepPlan) > 0 && int(got.Status.CurrentStepIndex) < len(got.Status.StepPlan) {
					hopTarget := got.Status.StepPlan[got.Status.CurrentStepIndex].ToVersion
					for _, n := range testNodes {
						var live corev1.Node
						g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: n.Name}, &live)).To(Succeed())
						if live.Status.NodeInfo.KubeletVersion != hopTarget {
							live.Status.NodeInfo.KubeletVersion = hopTarget
							g.Expect(k8sClient.Status().Update(ctx, &live)).To(Succeed())
						}
					}
				}

				return got.Status.Phase
			}, 60*time.Second, 500*time.Millisecond).Should(Equal(upgradev1alpha1.PhaseComplete))

			var final upgradev1alpha1.KubernetesUpgrade
			Expect(k8sClient.Get(ctx, key, &final)).To(Succeed())
			Expect(final.Status.StepPlan).To(HaveLen(2), "expected exactly 2 single-minor hops")
			for _, step := range final.Status.StepPlan {
				Expect(step.CompletedAt).NotTo(BeNil(), "expected every hop to be marked completed")
			}
		})
	})

	Context("When one group starts multiple minors behind another (found via real-cluster testing)", func() {
		const namespace = "default"

		ctx := context.Background()

		It("never skips a minor version for the lagging group, even though the global step plan has only one hop", func() {
			// The bug: status.startingVersion (and so the global step
			// plan) comes from the apiserver's own version, not any
			// specific group's actual version. A control-plane node
			// already caught up to the apiserver looks like a single
			// one-minor hop to target; a worker group still several
			// minors behind must NOT be handed that same jump.
			discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
			serverVersion, err := discoveryClient.ServerVersion()
			Expect(err).NotTo(HaveOccurred())
			current, err := k8sutil.ParseVersion(serverVersion.GitVersion)
			Expect(err).NotTo(HaveOccurred())
			major := int64(current.Major())
			minor := int64(current.Minor())
			Expect(minor).To(BeNumerically(">=", 2), "test needs room below the pinned envtest version")

			targetVersion := fmt.Sprintf("v%d.%d.0", major, minor+1) // one hop from the apiserver's own version

			cpNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "skew-cp-1",
					Labels: map[string]string{k8sutil.ControlPlaneLabelKey: ""},
				},
			}
			laggingWorker := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "skew-worker-1"}}
			testNodes := []*corev1.Node{cpNode, laggingWorker}

			statuses := map[string]string{
				cpNode.Name:        serverVersion.GitVersion,               // already caught up: v(minor)
				laggingWorker.Name: fmt.Sprintf("v%d.%d.0", major, minor-2), // two minors behind
			}
			for _, n := range testNodes {
				Expect(k8sClient.Create(ctx, n)).To(Succeed())
				n.Status = corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
					NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: statuses[n.Name]},
				}
				Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())
			}
			DeferCleanup(func() {
				for _, n := range testNodes {
					Expect(k8sClient.Delete(ctx, n)).To(Succeed())
				}
			})

			ku := &upgradev1alpha1.KubernetesUpgrade{
				ObjectMeta: metav1.ObjectMeta{Name: "skew-test", Namespace: namespace},
				Spec:       upgradev1alpha1.KubernetesUpgradeSpec{TargetVersion: targetVersion},
			}
			Expect(k8sClient.Create(ctx, ku)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, ku)).To(Succeed())
			})

			key := types.NamespacedName{Name: ku.Name, Namespace: namespace}
			cpChildKey := types.NamespacedName{Name: childName(ku.Name, "control-plane"), Namespace: namespace}
			workersChildKey := types.NamespacedName{Name: childName(ku.Name, "workers"), Namespace: namespace}

			seenWorkerTargets := map[string]bool{}

			// Same double-duty simulation as the multi-hop test above,
			// but per-group rather than per-global-hop: each group's
			// child can be asking for a different intermediate version
			// at the same time, since the lagging group needs more
			// passes than the caught-up one.
			Eventually(func(g Gomega) upgradev1alpha1.KubernetesUpgradePhase {
				var got upgradev1alpha1.KubernetesUpgrade
				g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

				for nodeName, childKey := range map[string]types.NamespacedName{
					cpNode.Name:        cpChildKey,
					laggingWorker.Name: workersChildKey,
				} {
					var child upgradev1alpha1.NodeGroupUpgrade
					if err := k8sClient.Get(ctx, childKey, &child); err != nil {
						continue
					}
					if nodeName == laggingWorker.Name {
						seenWorkerTargets[child.Spec.TargetVersion] = true
					}
					var live corev1.Node
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, &live)).To(Succeed())
					if live.Status.NodeInfo.KubeletVersion != child.Spec.TargetVersion {
						live.Status.NodeInfo.KubeletVersion = child.Spec.TargetVersion
						g.Expect(k8sClient.Status().Update(ctx, &live)).To(Succeed())
					}
				}

				return got.Status.Phase
			}, 90*time.Second, 250*time.Millisecond).Should(Equal(upgradev1alpha1.PhaseComplete))

			// The core assertion: the lagging worker group must have been
			// driven through its OWN intermediate waypoint(s), never
			// handed the final target directly from two minors behind.
			intermediateTarget := fmt.Sprintf("v%d.%d.0", major, minor-1)
			Expect(seenWorkerTargets).To(HaveKey(intermediateTarget),
				"expected the lagging worker group to pass through its own one-minor waypoint %q, saw targets %v",
				intermediateTarget, seenWorkerTargets)
			Expect(seenWorkerTargets).To(HaveKey(targetVersion),
				"expected the lagging worker group to eventually reach the real target %q, saw targets %v",
				targetVersion, seenWorkerTargets)

			var laggingLive corev1.Node
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: laggingWorker.Name}, &laggingLive)).To(Succeed())
			Expect(laggingLive.Status.NodeInfo.KubeletVersion).To(Equal(targetVersion))
		})
	})

	Context("When the target equals the apiservers own version but a group is behind it (found via real-cluster testing)", func() {
		const namespace = "default"

		ctx := context.Background()

		It("still drives the lagging group forward instead of short-circuiting to Complete", func() {
			// The bug: status.startingVersion (and whether there is
			// anything to do at all) was decided purely by comparing the
			// apiservers own version against the target - if they already
			// matched, reconcileDiscovering returned Complete immediately,
			// without ever listing nodes or discovering groups. A real
			// cluster where the control plane already sits at the target
			// but a worker group does not (e.g. right after upgrading the
			// control plane first) hit exactly this: the KubernetesUpgrade
			// reported "already at the target version" while a real node
			// sat two minors behind.
			discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
			serverVersion, err := discoveryClient.ServerVersion()
			Expect(err).NotTo(HaveOccurred())
			current, err := k8sutil.ParseVersion(serverVersion.GitVersion)
			Expect(err).NotTo(HaveOccurred())
			major := int64(current.Major())
			minor := int64(current.Minor())
			Expect(minor).To(BeNumerically(">=", 2), "test needs room below the pinned envtest version")

			targetVersion := serverVersion.GitVersion // deliberately NOT ahead of the apiserver

			laggingWorker := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "already-at-target-worker-1"}}
			Expect(k8sClient.Create(ctx, laggingWorker)).To(Succeed())
			laggingWorker.Status = corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: fmt.Sprintf("v%d.%d.0", major, minor-2)},
			}
			Expect(k8sClient.Status().Update(ctx, laggingWorker)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, laggingWorker)).To(Succeed())
			})

			ku := &upgradev1alpha1.KubernetesUpgrade{
				ObjectMeta: metav1.ObjectMeta{Name: "already-at-target-test", Namespace: namespace},
				Spec:       upgradev1alpha1.KubernetesUpgradeSpec{TargetVersion: targetVersion},
			}
			Expect(k8sClient.Create(ctx, ku)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, ku)).To(Succeed())
			})

			key := types.NamespacedName{Name: ku.Name, Namespace: namespace}
			workersChildKey := types.NamespacedName{Name: childName(ku.Name, "workers"), Namespace: namespace}

			Eventually(func(g Gomega) upgradev1alpha1.KubernetesUpgradePhase {
				var got upgradev1alpha1.KubernetesUpgrade
				g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

				var child upgradev1alpha1.NodeGroupUpgrade
				if err := k8sClient.Get(ctx, workersChildKey, &child); err == nil {
					var live corev1.Node
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: laggingWorker.Name}, &live)).To(Succeed())
					if live.Status.NodeInfo.KubeletVersion != child.Spec.TargetVersion {
						live.Status.NodeInfo.KubeletVersion = child.Spec.TargetVersion
						g.Expect(k8sClient.Status().Update(ctx, &live)).To(Succeed())
					}
				}

				return got.Status.Phase
			}, 60*time.Second, 250*time.Millisecond).Should(Equal(upgradev1alpha1.PhaseComplete))

			var final upgradev1alpha1.KubernetesUpgrade
			Expect(k8sClient.Get(ctx, key, &final)).To(Succeed())
			Expect(final.Status.Message).NotTo(Equal("cluster is already at the target version"),
				"expected the lagging group to actually be driven forward, not short-circuited")

			var finalNode corev1.Node
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: laggingWorker.Name}, &finalNode)).To(Succeed())
			Expect(finalNode.Status.NodeInfo.KubeletVersion).To(Equal(targetVersion))
		})
	})

	Context("When one group is already at the final target while another needs multiple hops (found via real-cluster testing)", func() {
		const namespace = "default"

		ctx := context.Background()

		It("never asks the already-finished group to regress to an earlier hop", func() {
			// The real bug this covers: NextGroupTarget's original clamp
			// only handled a group being BEHIND a hop's target - a group
			// already strictly AHEAD of it (like a control-plane group
			// that finished a previous run entirely) got handed the hop's
			// target directly, which is a downgrade instruction. On a
			// real 3-control-plane cluster this made an already-upgraded
			// control plane get re-pointed at an EARLIER intermediate
			// version while a lagging worker group climbed through its
			// own waypoints.
			discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
			serverVersion, err := discoveryClient.ServerVersion()
			Expect(err).NotTo(HaveOccurred())
			current, err := k8sutil.ParseVersion(serverVersion.GitVersion)
			Expect(err).NotTo(HaveOccurred())
			major := int64(current.Major())
			minor := int64(current.Minor())
			Expect(minor).To(BeNumerically(">=", 2), "test needs room below the pinned envtest version")

			targetVersion := fmt.Sprintf("v%d.%d.0", major, minor+1) // one hop above the apiserver

			cpNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "finished-cp-1",
					Labels: map[string]string{k8sutil.ControlPlaneLabelKey: ""},
				},
			}
			laggingWorker := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "climbing-worker-1"}}
			testNodes := []*corev1.Node{cpNode, laggingWorker}

			statuses := map[string]string{
				cpNode.Name:        targetVersion,                          // already fully done
				laggingWorker.Name: fmt.Sprintf("v%d.%d.0", major, minor-2), // needs multiple hops
			}
			for _, n := range testNodes {
				Expect(k8sClient.Create(ctx, n)).To(Succeed())
				n.Status = corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
					NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: statuses[n.Name]},
				}
				Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())
			}
			DeferCleanup(func() {
				for _, n := range testNodes {
					Expect(k8sClient.Delete(ctx, n)).To(Succeed())
				}
			})

			ku := &upgradev1alpha1.KubernetesUpgrade{
				ObjectMeta: metav1.ObjectMeta{Name: "no-regression-test", Namespace: namespace},
				Spec:       upgradev1alpha1.KubernetesUpgradeSpec{TargetVersion: targetVersion},
			}
			Expect(k8sClient.Create(ctx, ku)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, ku)).To(Succeed())
			})

			key := types.NamespacedName{Name: ku.Name, Namespace: namespace}
			cpChildKey := types.NamespacedName{Name: childName(ku.Name, "control-plane"), Namespace: namespace}
			workersChildKey := types.NamespacedName{Name: childName(ku.Name, "workers"), Namespace: namespace}

			seenCPTargets := map[string]bool{}

			Eventually(func(g Gomega) upgradev1alpha1.KubernetesUpgradePhase {
				var got upgradev1alpha1.KubernetesUpgrade
				g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

				for nodeName, childKey := range map[string]types.NamespacedName{
					cpNode.Name:        cpChildKey,
					laggingWorker.Name: workersChildKey,
				} {
					var child upgradev1alpha1.NodeGroupUpgrade
					if err := k8sClient.Get(ctx, childKey, &child); err != nil {
						continue
					}
					if nodeName == cpNode.Name {
						seenCPTargets[child.Spec.TargetVersion] = true
					}
					var live corev1.Node
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, &live)).To(Succeed())
					if live.Status.NodeInfo.KubeletVersion != child.Spec.TargetVersion {
						live.Status.NodeInfo.KubeletVersion = child.Spec.TargetVersion
						g.Expect(k8sClient.Status().Update(ctx, &live)).To(Succeed())
					}
				}

				return got.Status.Phase
			}, 90*time.Second, 250*time.Millisecond).Should(Equal(upgradev1alpha1.PhaseComplete))

			Expect(seenCPTargets).To(Equal(map[string]bool{targetVersion: true}),
				"expected the already-finished control-plane group to only ever be asked for the final target, never an earlier hop; saw %v", seenCPTargets)

			var finalCPNode corev1.Node
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cpNode.Name}, &finalCPNode)).To(Succeed())
			Expect(finalCPNode.Status.NodeInfo.KubeletVersion).To(Equal(targetVersion),
				"the already-finished control-plane group must not have been regressed")
		})
	})

	Context("When a Generic-provider group requests strategy=Replace", func() {
		const namespace = "default"

		ctx := context.Background()

		currentServerVersion := func() string {
			discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
			serverVersion, err := discoveryClient.ServerVersion()
			Expect(err).NotTo(HaveOccurred())
			return serverVersion.GitVersion
		}

		computeTargetVersion := func() string {
			current, err := k8sutil.ParseVersion(currentServerVersion())
			Expect(err).NotTo(HaveOccurred())
			return fmt.Sprintf("v%d.%d.0", int64(current.Major()), int64(current.Minor())+1)
		}

		createGenericNode := func(name string) {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec:       corev1.NodeSpec{ProviderID: "unknown-cloud://" + name},
			}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())
			node.Status = corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				// This group's own current version drives how far
				// upgrade.NextGroupTarget lets it move in one pass - match
				// the apiserver's version, same baseline computeTargetVersion
				// bumps one minor above, so this stays the single-hop case
				// these tests are actually exercising.
				NodeInfo: corev1.NodeSystemInfo{KubeletVersion: currentServerVersion()},
			}
			Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, node)).To(Succeed())
			})
		}

		waitForGenericReplaceRiskCondition := func(key types.NamespacedName) metav1.Condition {
			var found metav1.Condition
			Eventually(func(g Gomega) bool {
				var got upgradev1alpha1.KubernetesUpgrade
				g.Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
				for _, c := range got.Status.Conditions {
					if c.Type == "GenericReplaceRisk" {
						found = c
						return true
					}
				}
				return false
			}, 30*time.Second, 250*time.Millisecond).Should(BeTrue(), "expected a GenericReplaceRisk condition")
			return found
		}

		It("rejects it without acknowledgeReplaceRisk and reports why", func() {
			createGenericNode("generic-unack-1")

			ku := &upgradev1alpha1.KubernetesUpgrade{
				ObjectMeta: metav1.ObjectMeta{Name: "generic-unack-test", Namespace: namespace},
				Spec: upgradev1alpha1.KubernetesUpgradeSpec{
					TargetVersion: computeTargetVersion(),
					GroupOverrides: []upgradev1alpha1.NodeGroupOverride{
						{GroupName: "workers", Strategy: strategyPtr(upgradev1alpha1.StrategyReplace)},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ku)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, ku)).To(Succeed())
			})

			key := types.NamespacedName{Name: ku.Name, Namespace: namespace}
			risk := waitForGenericReplaceRiskCondition(key)
			Expect(risk.Status).To(Equal(metav1.ConditionFalse))
			Expect(risk.Reason).To(Equal("NotAcknowledged"))

			var children upgradev1alpha1.NodeGroupUpgradeList
			Expect(k8sClient.List(ctx, &children,
				client.InNamespace(namespace),
				client.MatchingLabels{parentLabelKey: ku.Name},
			)).To(Succeed())
			Expect(children.Items).To(HaveLen(1))
			Expect(children.Items[0].Spec.Strategy).To(Equal(upgradev1alpha1.StrategyInPlace))
		})

		It("allows it once acknowledgeReplaceRisk is set and reports it as active", func() {
			createGenericNode("generic-ack-1")

			ku := &upgradev1alpha1.KubernetesUpgrade{
				ObjectMeta: metav1.ObjectMeta{Name: "generic-ack-test", Namespace: namespace},
				Spec: upgradev1alpha1.KubernetesUpgradeSpec{
					TargetVersion: computeTargetVersion(),
					GroupOverrides: []upgradev1alpha1.NodeGroupOverride{
						{
							GroupName:              "workers",
							Strategy:               strategyPtr(upgradev1alpha1.StrategyReplace),
							AcknowledgeReplaceRisk: true,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ku)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, ku)).To(Succeed())
			})

			key := types.NamespacedName{Name: ku.Name, Namespace: namespace}
			risk := waitForGenericReplaceRiskCondition(key)
			Expect(risk.Status).To(Equal(metav1.ConditionTrue))
			Expect(risk.Reason).To(Equal("AcknowledgedAndActive"))

			var children upgradev1alpha1.NodeGroupUpgradeList
			Expect(k8sClient.List(ctx, &children,
				client.InNamespace(namespace),
				client.MatchingLabels{parentLabelKey: ku.Name},
			)).To(Succeed())
			Expect(children.Items).To(HaveLen(1))
			Expect(children.Items[0].Spec.Strategy).To(Equal(upgradev1alpha1.StrategyReplace))
		})
	})
})

func strategyPtr(s upgradev1alpha1.NodeGroupStrategy) *upgradev1alpha1.NodeGroupStrategy { return &s }
