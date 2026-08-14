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

// KubernetesUpgradeSpec defines the desired state of KubernetesUpgrade
type KubernetesUpgradeSpec struct {
	// targetVersion is the Kubernetes version to upgrade the cluster to, e.g. "v1.30.4".
	// +kubebuilder:validation:Pattern=`^v?\d+\.\d+\.\d+.*$`
	// +required
	TargetVersion string `json:"targetVersion"`

	// paused halts all reconciliation for this upgrade when true.
	// +optional
	// +kubebuilder:default=false
	Paused bool `json:"paused,omitempty"`

	// scope narrows which nodes are considered part of this upgrade.
	// A nil scope means "every node in the cluster".
	// +optional
	Scope *UpgradeScope `json:"scope,omitempty"`

	// defaults are cluster-wide fallbacks inherited by every discovered
	// NodeGroupUpgrade child unless overriden per-group.
	// +optional
	Defaults *UpgradeDefaults `json:"defaults,omitempty"`

	// groupOverrides lets you tweak a specific *discovered* node group
	// (matched by groupName) without hand-authoring the whole child object.
	// +optional
	GroupOverrides []NodeGroupOverride `json:"groupOverrides,omitempty"`

	// safety holds cluster-wide guardrails for the upgrade.
	// +optional
	Safety *SafetyPolicy `json:"safety,omitempty"`
}

// UpgradeScope limits which nodes this upgrade considers.
type UpgradeScope struct {
	// nodeSelector limits which nodes are in scope. Nil selects all nodes.
	// +optional
	NodeSelector *metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

// UpgradeDefaults are cluster-wide fallbacks for discovered node groups.
type UpgradeDefaults struct {
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
	// +optional
	BatchSize *int32 `json:"batchSize,omitempty"`
	// +optional
	DrainTimeoutSeconds *int32 `json:"drainTimeoutSeconds,omitempty"`
	// +optional
	ForceDrainAfterTimeout *bool `json:"forceDrainAfterTimeout,omitempty"`
	// +optional
	NodeReadyTimeoutSeconds *int32 `json:"nodeReadyTimeoutSeconds,omitempty"`
}

// NodeGroupOverride overrides discovery-computed settings for one named node group.
type NodeGroupOverride struct {
	// groupName must match a group name produced by discovery.
	// +required
	GroupName string `json:"groupName"`
	// +optional
	Strategy *NodeGroupStrategy `json:"strategy,omitempty"`
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
	// +optional
	BatchSize *int32 `json:"batchSize,omitempty"`
	// pause halts only this group, leaving others to proceed.
	// +optional
	Pause *bool `json:"pause,omitempty"`
	// skip excludes this group from the current upgrade run entirely.
	// +optional
	Skip *bool `json:"skip,omitempty"`
}

// SafetyPolicy holds cluster-wide guardrails.
type SafetyPolicy struct {
	// requireEtcdQuorum gates control-plane node upgrades on a quorum/health check.
	// +optional
	// +kubebuilder:default=true
	RequireEtcdQuorum bool `json:"requireEtcdQuorum,omitempty"`
	// +optional
	ControlPlaneHealthTimeoutSeconds *int32 `json:"controlPlaneHealthTimeoutSeconds,omitempty"`
	// allowDowngrade permits targetVersion to be lower than the cluster's current version.
	// +optional
	// +kubebuilder:default=false
	AllowDowngrade bool `json:"allowDowngrade,omitempty"`
}

// KubernetesUpgradePhase is the current phase of the top-level state machine.
// +kubebuilder:validation:Enum=Pending;Discovering;Prechecks;ControlPlaneUpgrade;WorkersUpgrade;Postchecks;Complete;Failed;Paused
type KubernetesUpgradePhase string

const (
	PhasePending             KubernetesUpgradePhase = "Pending"
	PhaseDiscovering         KubernetesUpgradePhase = "Discovering"
	PhasePrechecks           KubernetesUpgradePhase = "Prechecks"
	PhaseControlPlaneUpgrade KubernetesUpgradePhase = "ControlPlaneUpgrade"
	PhaseWorkersUpgrade      KubernetesUpgradePhase = "WorkersUpgrade"
	PhasePostchecks          KubernetesUpgradePhase = "Postchecks"
	PhaseComplete            KubernetesUpgradePhase = "Complete"
	PhaseFailed              KubernetesUpgradePhase = "Failed"
	PhasePaused              KubernetesUpgradePhase = "Paused"
)

// KubernetesUpgradeStatus defines the observed state of KubernetesUpgrade.
type KubernetesUpgradeStatus struct {
	// +optional
	Phase KubernetesUpgradePhase `json:"phase,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the KubernetesUpgrade resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// startingVersion is captured once, at first reconcile, as the baseline for stepPlan.
	// +optional
	StartingVersion string `json:"startingVersion,omitempty"`

	// stepPlan is the computed sequence of single-minor-version hops needed to
	// reach targetVersion, persisted for idempotency/resumability.
	// +optional
	StepPlan []UpgradeStep `json:"stepPlan,omitempty"`
	// +optional
	CurrentStepIndex *int `json:"currentStepIndex,omitempty"`

	// +optional
	ControlPlane ControlPlaneUpgradeStatus `json:"controlPlane,omitempty"`
	// +optional
	DiscoveredGroups []DiscoveredGroupStatus `json:"discoveredGroups,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`
}

// UpgradeStep is one single-minor-version hop in the overall upgrade.
type UpgradeStep struct {
	FromVersion string       `json:"fromVersion"`
	ToVersion   string       `json:"toVersion"`
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// ControlPlaneUpgradeStatus summarizes control-plane node progress.
type ControlPlaneUpgradeStatus struct {
	// +optional
	TotalNodes int32 `json:"totalNodes,omitempty"`
	// +optional
	UpgradedNodes int32 `json:"upgradedNodes,omitempty"`
	// +optional
	CurrentNode string `json:"currentNode,omitempty"`
	// +optional
	Version string `json:"version,omitempty"`
}

// DiscoveredGroupStatus is a rollup of one discovered NodeGroupUpgrade child.
type DiscoveredGroupStatus struct {
	Name     string            `json:"name"`
	Provider ProviderType      `json:"provider"`
	Strategy NodeGroupStrategy `json:"strategy"`
	Role     NodeGroupRole     `json:"role"`
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	NodeCount int32 `json:"nodeCount,omitempty"`
	// +optional
	ChildRefName string `json:"childRefName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// KubernetesUpgrade is the Schema for the kubernetesupgrades API
type KubernetesUpgrade struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of KubernetesUpgrade
	// +required
	Spec KubernetesUpgradeSpec `json:"spec"`

	// status defines the observed state of KubernetesUpgrade
	// +optional
	Status KubernetesUpgradeStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// KubernetesUpgradeList contains a list of KubernetesUpgrade
type KubernetesUpgradeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KubernetesUpgrade `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &KubernetesUpgrade{}, &KubernetesUpgradeList{})
		return nil
	})
}
