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

package provider

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
)

// UpgradeContext carries the shared, non-node-specific inputs an adapter
// needs to act on a NodeGroupUpgrade.
type UpgradeContext struct {
	Client		client.Client
	Log		logr.Logger
	Group		*upgradev1alpha1.NodeGroupUpgrade
	TargetVersion	string
}

// NodePhase is a single node's progress within an adapter's batch
// operation, independent of the NodeGroupUpgrade's own phase.
type NodePhase string

const (
	NodePhaseInProgress NodePhase = "InProgress"
	NodePhaseUpgraded   NodePhase = "Upgraded"
	NodePhaseFailed     NodePhase = "Failed"
)

// NodeResult reports one node's outcome from a PollBatch call.
type NodeResult struct {
	NodeName string
	Phase    NodePhase
	Error    error
}

// Adapter implements the actual upgrade mechanics for one ProviderType.
// Every method must be safe to call repeatedly (idempotent) since the
// NodeGroupUpgrade controller drives these from a reconcile loop, not a
// single blocking call.
type Adapter interface {
	// Type identifies which ProviderType this adapter handles.
	Type() upgradev1alpha1.ProviderType

	// SupportsStrategy reports whether this adapter can execute the given
	// strategy at all (e.g. a managed-node-group adapter only supports
	// Replace, never InPlace).
	SupportsStrategy(s upgradev1alpha1.NodeGroupStrategy) bool

	// Precheck reports whether the group is in a state where a batch can
	// safely begin (e.g. control-plane health, cloud API reachability).
	Precheck(ctx context.Context, uc UpgradeContext) (ready bool, reason string, err error)

	// BeginBatch starts upgrading the given batch of already-drained
	// nodes. It should return quickly (e.g. after creating a Job or
	// calling a cloud API) rather than blocking until the batch finishes.
	BeginBatch(ctx context.Context, uc UpgradeContext, batch []corev1.Node) error

	// PollBatch reports current progress for a batch previously started
	// with BeginBatch. Called repeatedly until every node in the batch
	// reaches a terminal NodePhase.
	PollBatch(ctx context.Context, uc UpgradeContext, batch []corev1.Node) ([]NodeResult, error)

	// Verify performs any provider-specific post-upgrade validation for
	// the group as a whole, beyond the generic node-ready/version checks
	// the controller already performs via pkg/k8sutil.
	Verify(ctx context.Context, uc UpgradeContext) (verified bool, reason string, err error)
}