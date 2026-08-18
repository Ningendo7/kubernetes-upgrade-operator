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
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/upgrade"
)

const (
	fieldOwner     = "kubernetesupgrade-controller"
	parentLabelKey = "upgrade.k8s-upgrade-operator/kubernetesupgrade"
	groupLabelKey  = "upgrade.k8s-upgrade-operator/node-group"
)

func (r *KubernetesUpgradeReconciler) reconcileDiscovering(ctx context.Context, ku *upgradev1alpha1.KubernetesUpgrade) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if ku.Status.StartingVersion == "" || len(ku.Status.StepPlan) == 0 {
		serverVersion, err := r.DiscoveryClient.ServerVersion()
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("getting apiserver version: %w", err)
		}

		allowDowngrade := ku.Spec.Safety != nil && ku.Spec.Safety.AllowDowngrade
		steps, err := upgrade.ComputeStepPlan(serverVersion.GitVersion, ku.Spec.TargetVersion, allowDowngrade)
		if err != nil {
			ku.Status.Phase = upgradev1alpha1.PhaseFailed
			ku.Status.Message = err.Error()
			return ctrl.Result{}, r.Status().Update(ctx, ku)
		}

		ku.Status.StartingVersion = serverVersion.GitVersion
		ku.Status.StepPlan = steps
		ku.Status.CurrentStepIndex = 0

		if len(steps) == 0 {
			ku.Status.Phase = upgradev1alpha1.PhaseComplete
			ku.Status.Message = "cluster is already at the target version"
			return ctrl.Result{}, r.Status().Update(ctx, ku)
		}

		if err := r.Status().Update(ctx, ku); err != nil {
			return ctrl.Result{}, err
		}
	}

	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing nodes: %w", err)
	}

	groups, err := upgrade.DiscoverGroups(nodes.Items, ku.Spec.Scope)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("discovering node groups: %w", err)
	}

	targetVersion := ku.Status.StepPlan[ku.Status.CurrentStepIndex].ToVersion

	discovered := make([]upgradev1alpha1.DiscoveredGroupStatus, 0, len(groups))
	for _, group := range groups {
		override := findOverride(ku.Spec.GroupOverrides, group.Name)
		if override != nil && override.Skip != nil && *override.Skip {
			continue
		}

		child := buildDesiredChild(ku, group, targetVersion, override, ku.Spec.Defaults)
		if err := controllerutil.SetControllerReference(ku, child, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("setting controller reference for group %q: %w", group.Name, err)
		}

		if err := r.Patch(
			ctx,
			child,
			client.Apply,
			client.FieldOwner(fieldOwner),
			client.ForceOwnership,
		); err != nil {
			return ctrl.Result{}, fmt.Errorf("applying NodeGroupUpgrade for group %q: %w", group.Name, err)
		}

		discovered = append(discovered, upgradev1alpha1.DiscoveredGroupStatus{
			Name:         group.Name,
			Provider:     group.Provider,
			Strategy:     child.Spec.Strategy,
			Role:         group.Role,
			Phase:        string(child.Status.Phase),
			NodeCount:    int32(len(group.Nodes)),
			ChildRefName: child.Name,
			Heuristic:    group.Heuristic,
		})
	}

	if err := r.pruneOrphanedChildren(ctx, ku, groups); err != nil {
		return ctrl.Result{}, err
	}

	ku.Status.DiscoveredGroups = discovered
	ku.Status.Phase = upgradev1alpha1.PhasePrechecks
	if err := r.Status().Update(ctx, ku); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("discovery complete", "groups", len(discovered))
	return ctrl.Result{Requeue: true}, nil
}

func findOverride(overrides []upgradev1alpha1.NodeGroupOverride, groupName string) *upgradev1alpha1.NodeGroupOverride {
	for i := range overrides {
		if overrides[i].GroupName == groupName {
			return &overrides[i]
		}
	}
	return nil
}

func buildDesiredChild(
	ku *upgradev1alpha1.KubernetesUpgrade,
	group upgrade.DiscoveredGroup,
	targetVersion string,
	override *upgradev1alpha1.NodeGroupOverride,
	defaults *upgradev1alpha1.UpgradeDefaults,
) *upgradev1alpha1.NodeGroupUpgrade {
	spec := upgradev1alpha1.NodeGroupUpgradeSpec{
		TargetVersion: targetVersion,
		Role:          group.Role,
		Provider:      group.Provider,
		Strategy:      upgrade.ResolveStrategy(group, override),
		ProviderRef:   group.ProviderRef,
		Nodes:         group.Nodes,
		Hold:          true,
	}

	switch {
	case group.Role == upgradev1alpha1.RoleControlPlane:
		batchSize := int32(1) // hard-pinned, never user-configurable
		spec.BatchSize = &batchSize
	case override != nil && override.BatchSize != nil:
		spec.BatchSize = override.BatchSize
	case override != nil && override.MaxUnavailable != nil:
		spec.MaxUnavailable = override.MaxUnavailable
	case defaults != nil && defaults.BatchSize != nil:
		spec.BatchSize = defaults.BatchSize
	case defaults != nil:
		spec.MaxUnavailable = defaults.MaxUnavailable
	}

	if defaults != nil {
		spec.Drain.TimeoutSeconds = defaults.DrainTimeoutSeconds
		if defaults.ForceDrainAfterTimeout != nil {
			spec.Drain.Force = *defaults.ForceDrainAfterTimeout
		}
	}

	if override != nil && override.Pause != nil {
		spec.Paused = *override.Pause
	}

	child := &upgradev1alpha1.NodeGroupUpgrade{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName(ku.Name, group.Name),
			Namespace: ku.Namespace,
			Labels: map[string]string{
				parentLabelKey: ku.Name,
				groupLabelKey:  truncateLabelValue(group.Name),
			},
		},
		Spec: spec,
	}
	child.TypeMeta = metav1.TypeMeta{
		APIVersion: upgradev1alpha1.GroupVersion.String(),
		Kind:       "NodeGroupUpgrade",
	}
	return child
}

func (r *KubernetesUpgradeReconciler) pruneOrphanedChildren(
	ctx context.Context,
	ku *upgradev1alpha1.KubernetesUpgrade,
	groups []upgrade.DiscoveredGroup,
) error {
	var children upgradev1alpha1.NodeGroupUpgradeList
	if err := r.List(
		ctx,
		&children,
		client.InNamespace(ku.Namespace),
		client.MatchingLabels{parentLabelKey: ku.Name}); err != nil {
		return fmt.Errorf("listing existing NodeGroupUpgrade children: %w", err)
	}

	existing := make([]upgrade.ExistingChild, 0, len(children.Items))
	byGroupName := make(map[string]upgradev1alpha1.NodeGroupUpgrade, len(children.Items))
	for _, c := range children.Items {
		groupName := c.Labels[groupLabelKey]
		existing = append(existing, upgrade.ExistingChild{
			Name:  groupName,
			Phase: c.Status.Phase,
		})
		byGroupName[groupName] = c
	}

	for _, name := range upgrade.PruneCandidates(groups, existing) {
		child, ok := byGroupName[name]
		if !ok {
			continue
		}
		if err := r.Delete(ctx, &child); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("pruning NodeGroupUpgrade %q: %w", child.Name, err)
		}
	}
	return nil
}

// childName produces a valid (lowercase) Kubernetes object name. This is a
// best-effort pass, not a full RFC1123 sanitizer - a pathological group
// name could still produce an invalid name, which will surface as a clear
// API error rather than silently misbehaving.
func childName(parentName, groupName string) string {
	return strings.ToLower(fmt.Sprintf("%s-%s", parentName, groupName))
}

// truncateLabelValue guards against a group name exceeding the
// 63-character Kubernetes label-value limit. Unlike childName, this
// preserves original casing, since label values (unlike object names)
// allow it - and preserving it here is what keeps pruneOrphanedChildren's
// identity matching exact.
func truncateLabelValue(s string) string {
	if len(s) > 63 {
		return s[:63]
	}
	return s
}
