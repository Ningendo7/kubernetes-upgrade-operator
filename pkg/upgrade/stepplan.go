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

import (
	"fmt"

	upgradev1alpha1 "github.com/Ningendo7/kubernetes-upgrade-operator/api/v1alpha1"
	"github.com/Ningendo7/kubernetes-upgrade-operator/pkg/k8sutil"
)

// ComputeStepPlan breaks an upgrade from current to target into a sequence
// of single-minor-version hops, since Kubernetes only supports upgrading
// one minor version at a time. A nil, nil result means current already
// equals target. Downgrades are rejected unless allowDowngrade is true, in
// which case a single direct hop to target is returned (Kubernetes has no
// supported "downgrade one minor at a time" path, so we don't simulate one).
func ComputeStepPlan(current, target string, allowDowngrade bool,
) ([]upgradev1alpha1.UpgradeStep, error) {
	cmp, err := k8sutil.CompareVersions(current, target)
	if err != nil {
		return nil, err
	}

	if cmp == 0 {
		return nil, nil
	}

	if cmp > 0 {
		if !allowDowngrade {
			return nil, fmt.Errorf("target version %q is lower than current version %q; set spec.safety.allowDowngrade to permit this", target, current)
		}
		return []upgradev1alpha1.UpgradeStep{
			{FromVersion: current, ToVersion: target},
		}, nil
	}

	diff, err := k8sutil.MinorDiff(current, target)
	if err != nil {
		return nil, err
	}

	if diff == 0 {
		// Same major.minor, only a patch/prerelease bump: one direct step.
		return []upgradev1alpha1.UpgradeStep{
			{FromVersion: current, ToVersion: target},
		}, nil
	}

	cv, err := k8sutil.ParseVersion(current)
	if err != nil {
		return nil, err
	}
	major := int64(cv.Major())
	minor := int64(cv.Minor())
	patch := int64(cv.Patch())

	steps := make([]upgradev1alpha1.UpgradeStep, 0, diff)
	from := fmt.Sprintf("v%d.%d.%d", major, minor, patch)
	for i := int64(1); i <= int64(diff); i++ {
		var to string
		if i == int64(diff) {
			// Final hop lands exactly on the requested target, preserving
			// any patch/prerelease detail the caller actually asked for.
			to = target
		} else {
			to = fmt.Sprintf("v%d.%d.0", major, minor+i)
		}
		steps = append(steps, upgradev1alpha1.UpgradeStep{
			FromVersion: from,
			ToVersion:   to,
		})
		from = to
	}

	return steps, nil
}
