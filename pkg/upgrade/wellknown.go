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

const (
	// EKSNodeGroupLabel identifies which EKS managed node group a node belongs to.
	EKSNodeGroupLabel = "eks.amazonaws.com/nodegroup"
	// EKSClusterNameLabel carries the eksctl-managed cluster name, when present.
	EKSClusterNameLabel = "alpha.eksctl.io/cluster-name"

	// LinodeLKEPoolLabel identifies which LKE node pool a node belongs to.
	LinodeLKEPoolLabel = "lke.linode.com/pool-id"
	// LinodeLKEClusterIDLabel carries the LKE cluster ID.
	LinodeLKEClusterIDLabel = "lke.linode.com/clusterid"

	// ProviderOverrideAnnotation lets an operator force a node's provider
	// classification when the automatic heuristics are ambiguous or wrong.
	ProviderOverrideAnnotation = "upgrade.k8s-upgrade-operator/provider-override"

	// ControlPlaneGroupName is the fixed group name for all control-plane
	// nodes, exported so the KubernetesUpgrade controller can look up this
	// specific well-known child by name.
	ControlPlaneGroupName = "control-plane"
	// defaultWorkerGroupName is used when worker nodes can't be distinguished
	// into more specific pools.
	defaultWorkerGroupName = "workers"
)
