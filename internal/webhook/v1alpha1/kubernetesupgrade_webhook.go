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
	"context"
	"fmt"

	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
)

// nolint:unused
// log is for logging in this package.
var kubernetesupgradelog = logf.Log.WithName("kubernetesupgrade-resource")

// maxHopCount is a sanity ceiling on how many single-minor-version hops a
// targetVersion may imply. A request this far ahead of the cluster's
// current version is almost certainly a typo, not a real upgrade plan.
const maxHopCount = 5

// SetupKubernetesUpgradeWebhookWithManager registers the webhook for KubernetesUpgrade in the manager.
func SetupKubernetesUpgradeWebhookWithManager(mgr ctrl.Manager) error {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("creating discovery client for webhook: %w", err)
	}
	return ctrl.NewWebhookManagedBy(mgr, &upgradev1alpha1.KubernetesUpgrade{}).
		WithValidator(&KubernetesUpgradeCustomValidator{DiscoveryClient: discoveryClient}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-upgrade-k8s-upgrade-operator-v1alpha1-kubernetesupgrade,mutating=false,failurePolicy=fail,sideEffects=None,groups=upgrade.k8s-upgrade-operator,resources=kubernetesupgrades,verbs=create;update,versions=v1alpha1,name=vkubernetesupgrade-v1alpha1.kb.io,admissionReviewVersions=v1

// KubernetesUpgradeCustomValidator validates KubernetesUpgrade resources
// against live cluster state - specifically, whether targetVersion would
// be a downgrade or an implausibly large jump. Checks that don't need live
// cluster state (semver syntax, ProviderRef exclusivity) live as CRD CEL
// validation instead; this webhook only handles what genuinely requires a
// live read.
type KubernetesUpgradeCustomValidator struct {
	DiscoveryClient discovery.DiscoveryInterface
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type KubernetesUpgrade.
func (v *KubernetesUpgradeCustomValidator) ValidateCreate(_ context.Context, obj *upgradev1alpha1.KubernetesUpgrade) (admission.Warnings, error) {
	kubernetesupgradelog.Info("Validation for KubernetesUpgrade upon creation", "name", obj.GetName())
	return v.validate(obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type KubernetesUpgrade.
func (v *KubernetesUpgradeCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj *upgradev1alpha1.KubernetesUpgrade) (admission.Warnings, error) {
	kubernetesupgradelog.Info("Validation for KubernetesUpgrade upon update", "name", newObj.GetName())
	return v.validate(newObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type KubernetesUpgrade.
func (v *KubernetesUpgradeCustomValidator) ValidateDelete(_ context.Context, obj *upgradev1alpha1.KubernetesUpgrade) (admission.Warnings, error) {
	kubernetesupgradelog.Info("Validation for KubernetesUpgrade upon deletion", "name", obj.GetName())
	return nil, nil
}

func (v *KubernetesUpgradeCustomValidator) validate(ku *upgradev1alpha1.KubernetesUpgrade) (admission.Warnings, error) {
	target, err := k8sutil.ParseVersion(ku.Spec.TargetVersion)
	if err != nil {
		return nil, fmt.Errorf("spec.targetVersion: %w", err)
	}

	serverVersion, err := v.DiscoveryClient.ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("checking cluster version: %w", err)
	}
	current, err := k8sutil.ParseVersion(serverVersion.GitVersion)
	if err != nil {
		return nil, fmt.Errorf("parsing cluster version %q: %w", serverVersion.GitVersion, err)
	}

	cmp := target.Compare(current)
	allowDowngrade := ku.Spec.Safety != nil && ku.Spec.Safety.AllowDowngrade
	if cmp < 0 && !allowDowngrade {
		return nil, fmt.Errorf(
			"spec.targetVersion %q is lower than the cluster's current version %q; set spec.safety.allowDowngrade to permit this",
			ku.Spec.TargetVersion, serverVersion.GitVersion)
	}

	if cmp > 0 {
		diff, err := k8sutil.MinorDiff(serverVersion.GitVersion, ku.Spec.TargetVersion)
		if err != nil {
			return nil, fmt.Errorf("spec.targetVersion: %w", err)
		}
		if diff > maxHopCount {
			return nil, fmt.Errorf(
				"spec.targetVersion %q is %d minor versions ahead of the cluster's current version %q; this is almost certainly a mistake (max allowed: %d)",
				ku.Spec.TargetVersion, diff, serverVersion.GitVersion, maxHopCount)
		}
	}

	return nil, nil
}
