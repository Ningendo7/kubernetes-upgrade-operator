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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// NodeGroupRole says whether this group is control-plane or worker nodes.
// +kubebuilder:validation:Enum=ControlPlane;Worker
type NodeGroupRole string

const (
	RoleControlPlane NodeGroupRole = "ControlPlane"
	RoleWorker       NodeGroupRole = "Worker"
)

// ProviderType identifies which provider adapter manages this group's nodes.
// +kubebuilder:validation:Enum=Kubeadm;AWSEKSManagedNodeGroup;AWSAutoScalingGroup;LinodeLKE;Generic
type ProviderType string

const (
	ProviderKubeadm                ProviderType = "Kubeadm"
	ProviderAWSEKSManagedNodeGroup ProviderType = "AWSEKSManagedNodeGroup"
	ProviderAWSAutoScalingGroup    ProviderType = "AWSAutoScalingGroup"
	ProviderLinodeLKE              ProviderType = "LinodeLKE"
	ProviderGeneric                ProviderType = "Generic"
)

// NodeGroupStrategy is how nodes in this group get upgraded.
// +kubebuilder:validation:Enum=InPlace;Replace
type NodeGroupStrategy string

const (
	StrategyInPlace NodeGroupStrategy = "InPlace"
	StrategyReplace NodeGroupStrategy = "Replace"
)

// NodeGroupUpgradeSpec defines the desired state of NodeGroupUpgrade
type NodeGroupUpgradeSpec struct {
	// targetVersion is always a single-minor-version hop, set by the parent KubernetesUpgrade.
	// +required
	TargetVersion string `json:"targetVersion"`

	// +required
	Role NodeGroupRole `json:"role"`
	// +required
	Provider ProviderType `json:"provider"`
	// +required
	Strategy NodeGroupStrategy `json:"strategy"`

	// nodeSelector is how group membership is computed live, each reconcile.
	// +optional
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
	// nodes is a stable snapshot of member node names, used for ordering/progress tracking.
	// +optional
	Nodes []string `json:"nodes,omitempty"`

	// providerRef carries provider-specific identifiers. Exactly one variant
	// should be set, matching Provider.
	// +optional
	ProviderRef *ProviderRef `json:"providerRef,omitempty"`

	// +optional
	BatchSize *int32 `json:"batchSize,omitempty"`
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
	// +optional
	Drain DrainPolicy `json:"drain,omitempty"`

	// hold is set/cleared by the parent KubernetesUpgrade controller to
	// sequence control-plane-before-workers. Not normally hand-edited.
	// +optional
	Hold bool `json:"hold,omitempty"`

	// +optional
	Paused bool `json:"paused,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="(has(self.awsEKS)?1:0)+(has(self.awsASG)?1:0)+(has(self.linodeLKE)?1:0)<=1",message="only one providerRef variant may be set"
type ProviderRef struct {
	// +optional
	AWSEKS *AWSEKSNodeGroupRef `json:"awsEKS,omitempty"`
	// +optional
	AWSASG *AWSAutoScalingGroupRef `json:"awsASG,omitempty"`
	// +optional
	LinodeLKE *LinodeLKEPoolRef `json:"linodeLKE,omitempty"`
}

type AWSEKSNodeGroupRef struct {
	ClusterName   string `json:"clusterName"`
	NodeGroupName string `json:"nodeGroupName"`
	Region        string `json:"region"`
}

type AWSAutoScalingGroupRef struct {
	ASGName string `json:"asgName"`
	Region  string `json:"region"`
}

type LinodeLKEPoolRef struct {
	ClusterID string `json:"clusterID"`
	PoolID    string `json:"poolID"`
}

// DrainPolicy controls how nodes in this group are cordoned/drained.
type DrainPolicy struct {
	// +optional
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`
	// force, if true, force-evicts pods after timeoutSeconds instead of pausing.
	// +optional
	// +kubebuilder:default=false
	Force bool `json:"force,omitempty"`
	// +optional
	GracePeriodSeconds *int32 `json:"gracePeriodSeconds,omitempty"`
	// +optional
	// +kubebuilder:default=true
	IgnoreDaemonSets bool `json:"ignoreDaemonSets,omitempty"`
	// +optional
	// +kubebuilder:default=true
	DeleteEmptyDirData bool `json:"deleteEmptyDirData,omitempty"`
}

// NodeGroupUpgradePhase is the current phase of this group's batch loop.
// +kubebuilder:validation:Enum=Pending;Draining;Upgrading;Verifying;Complete;Failed;Paused
type NodeGroupUpgradePhase string

const (
	NGPending   NodeGroupUpgradePhase = "Pending"
	NGDraining  NodeGroupUpgradePhase = "Draining"
	NGUpgrading NodeGroupUpgradePhase = "Upgrading"
	NGVerifying NodeGroupUpgradePhase = "Verifying"
	NGComplete  NodeGroupUpgradePhase = "Complete"
	NGFailed    NodeGroupUpgradePhase = "Failed"
	NGPaused    NodeGroupUpgradePhase = "Paused"
)

// NodeGroupUpgradeStatus defines the observed state of NodeGroupUpgrade.
type NodeGroupUpgradeStatus struct {
	// +optional
	Phase NodeGroupUpgradePhase `json:"phase,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the NodeGroupUpgrade resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	TotalNodes int32 `json:"totalNodes,omitempty"`
	// +optional
	UpgradedNodes int32 `json:"upgradedNodes,omitempty"`
	// +optional
	NodeProgress []NodeProgress `json:"nodeProgress,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
}

// NodeProgress tracks one node's upgrade progress within the group.
type NodeProgress struct {
	Name string `json:"name"`
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	FromVersion string `json:"fromVersion,omitempty"`
	// +optional
	ToVersion string `json:"toVersion,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NodeGroupUpgrade is the Schema for the nodegroupupgrades API
type NodeGroupUpgrade struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of NodeGroupUpgrade
	// +required
	Spec NodeGroupUpgradeSpec `json:"spec"`

	// status defines the observed state of NodeGroupUpgrade
	// +optional
	Status NodeGroupUpgradeStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NodeGroupUpgradeList contains a list of NodeGroupUpgrade
type NodeGroupUpgradeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NodeGroupUpgrade `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &NodeGroupUpgrade{}, &NodeGroupUpgradeList{})
		return nil
	})
}
