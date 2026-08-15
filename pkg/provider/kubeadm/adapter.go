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

package kubeadm

import (
	"context"
	"errors"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/provider"
)

// Adapter implements provider.Adapter for on-prem/bare-metal kubeadm
// clusters, upgrading nodes in place via a privileged per-node Job.
type Adapter struct{}

func (a *Adapter) Type() upgradev1alpha1.ProviderType { return upgradev1alpha1.ProviderKubeadm }

func init() {
	provider.DefaultRegistry.Register(&Adapter{})
}

func (a *Adapter) SupportsStrategy(s upgradev1alpha1.NodeGroupStrategy) bool {
	return s == upgradev1alpha1.StrategyInPlace
}

// Precheck gates on control-plane health before touching any node in the
// group - both when this group *is* the control plane (the per-CP-node
// quorum/health gate) and defensively for worker groups too.
func (a *Adapter) Precheck(ctx context.Context, uc provider.UpgradeContext) (bool, string, error) {
	health, err := k8sutil.CheckControlPlaneHealth(ctx, uc.Client)
	if err != nil {
		return false, "", err
	}
	return health.Healthy, health.Reason, nil
}

// BeginBatch creates one executor Job per node in the batch. At most one
// node across the whole group's upgrade of this hop ever runs "kubeadm
// upgrade apply" - the first control-plane node, determined by checking
// whether any node has already completed this exact target version.
func (a *Adapter) BeginBatch(ctx context.Context, uc provider.UpgradeContext, batch []corev1.Node) error {
	isControlPlane := uc.Group != nil && uc.Group.Spec.Role == upgradev1alpha1.RoleControlPlane
	applyAvailable := isControlPlane && isFirstControlPlaneUpgrade(uc.Group, uc.TargetVersion)

	var errs []error
	for _, node := range batch {
		useApply := applyAvailable
		applyAvailable = false // at most one node claims "apply", even across a multi-node batch

		job := buildUpgradeJob(node.Name, uc.TargetVersion, useApply)
		if err := uc.Client.Create(ctx, job); err != nil {
			if apierrors.IsAlreadyExists(err) {
				continue // already started this pass or a prior one; idempotent
			}
			errs = append(errs, fmt.Errorf("creating upgrade job for node %q: %w", node.Name, err))
		}
	}
	return errors.Join(errs...)
}

func isFirstControlPlaneUpgrade(group *upgradev1alpha1.NodeGroupUpgrade, targetVersion string) bool {
	for _, np := range group.Status.NodeProgress {
		if np.ToVersion == targetVersion && np.CompletedAt != nil {
			return false
		}
	}
	return true
}

// PollBatch looks up each node's executor Job and maps its condition to a
// NodeResult. A missing Job is treated as a failure: BeginBatch should
// always have created one, so its absence means something deleted it out
// from under us.
func (a *Adapter) PollBatch(ctx context.Context, uc provider.UpgradeContext, batch []corev1.Node) ([]provider.NodeResult, error) {
	results := make([]provider.NodeResult, 0, len(batch))
	for _, node := range batch {
		var job batchv1.Job
		key := client.ObjectKey{
			Namespace:	ExecutorNamespace,
			Name:		jobNameFor(node.Name, uc.TargetVersion),
		}
		if err := uc.Client.Get(ctx, key, &job); err != nil {
			if apierrors.IsNotFound(err) {
				results = append(results, provider.NodeResult{
					NodeName: node.Name,
					Phase:	 provider.NodePhaseFailed,
					Error:	fmt.Errorf("upgrade job for node %q not found", node.Name),
				})
				continue
			}
			return nil, fmt.Errorf("getting upgrade job for node %q: %w", node.Name, err)
		}
		results = append(results, jobResult(node.Name, &job))
	}
	return results, nil
}

func jobResult(nodeName string, job *batchv1.Job) provider.NodeResult {
	for _, cond := range job.Status.Conditions {
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		switch cond.Type {
		case batchv1.JobComplete:
			return provider.NodeResult{
				NodeName: nodeName,
				Phase:    provider.NodePhaseUpgraded,
			}
		case batchv1.JobFailed:
			return provider.NodeResult{
				NodeName: nodeName,
				Phase:    provider.NodePhaseFailed,
				Error:    fmt.Errorf("upgrade job failed for node %q failed: %s", nodeName, cond.Message),
			}
		}
	}
	return provider.NodeResult{
		NodeName: nodeName,
		Phase:    provider.NodePhaseInProgress,
	}
}

// Verify re-checks control-plane health after a batch completes, on top of
// the generic node-ready/version checks the NodeGroupUpgrade controller
// already performs via pkg/k8sutil.
func (a *Adapter) Verify(ctx context.Context, uc provider.UpgradeContext) (bool, string, error) {
	health, err := k8sutil.CheckControlPlaneHealth(ctx, uc.Client)
	if err != nil {
		return false, "", err
	}
	return health.Healthy, health.Reason, nil
}
