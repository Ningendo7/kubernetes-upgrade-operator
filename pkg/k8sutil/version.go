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

package k8sutil

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// maxMinorSkew is the maximum number of minor versions a kubelet may lag
// behind the apiserver, per upstream Kubernetes version skew policy
const maxMinorSkew = 3

// ParseVersion parses a Kubernetes version string, tolerating an optional
// leading "v" (e.g. "v1.29.0" or "1.29.0").
func ParseVersion(v string) (*semver.Version, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(v), "v")
	parsed, err := semver.NewVersion(trimmed)
	if err != nil {
		return nil, fmt.Errorf("parsing version %q: %w", v, err)
	}
	return parsed, nil
}

// CompareVersions returns -1, 0, or 1 if a is less than, equal to, or
// greater than b, following semver ordering.
func CompareVersions(a, b string) (int, error) {
	av, err := ParseVersion(a)
	if err != nil {
		return 0, err
	}
	bv, err := ParseVersion(b)
	if err != nil {
		return 0, err
	}
	return av.Compare(bv), nil
}

// MinorDiff returns target's minor version number minus current's,
// assuming both share the same major version. A non-zero error is
// returned if the major versions differ, since that's not a supported
// Kubernetes upgrade path.
func MinorDiff(current, target string) (int, error) {
	cv, err := ParseVersion(current)
	if err != nil {
		return 0, err
	}
	tv, err := ParseVersion(target)
	if err != nil {
		return 0, err
	}
	if cv.Major() != tv.Major() {
		return 0, fmt.Errorf("major version change %d -> %d is not supported", cv.Major(), tv.Major())
	}
	return int(tv.Minor()) - int(cv.Minor()), nil
}

// IsSingleMinorHop reports whether target is exactly one minor version
// ahead of current (patch version differences are ignored).
func IsSingleMinorHop(current, target string) (bool, error) {
	diff, err := MinorDiff(current, target)
	if err != nil {
		return false, err
	}
	return diff == 1, nil
}

// CheckVersionSkew validates that kubeletVersion is compatible with
// apiserverVersion under the Kubernetes version skew policy: the kubelet
// must not be newer than the apiserver, and must not lag by more than
// maxMinorSkew minor versions.
func CheckVersionSkew(apiserverVersion, kubeletVersion string) error {
	diff, err := MinorDiff(kubeletVersion, apiserverVersion)
	if err != nil {
		return err
	}
	if diff < 0 {
		return fmt.Errorf("kubelet version %q is newer than apiserver version %q", kubeletVersion, apiserverVersion)
	}
	if diff > maxMinorSkew {
		return fmt.Errorf("kubelet version %q is more than %d minor versions behind apiserver version %q", kubeletVersion, maxMinorSkew, apiserverVersion)
	}
	return nil
}
