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

	"k8s.io/apimachinery/pkg/util/intstr"
)

// ResolveConcurrency computes how many nodes in a group of size total may
// be acted on concurrently, given optional batchSize/maxUnavailable
// overrides. batchSize takes priority if both are set. If neither is set,
// a conservative default of 1 is used. The result is always clamped to
// [1, total] when total > 0.
func ResolveConcurrency(total int, batchSize *int32, maxUnavailable *intstr.IntOrString) (int, error) {
	if total <= 0 {
		return 0, nil
	}

	limit := 1
	switch {
	case batchSize != nil && *batchSize > 0:
		limit = int(*batchSize)
	case maxUnavailable != nil:
		v, err := intstr.GetScaledValueFromIntOrPercent(maxUnavailable, total, false)
		if err != nil {
			return 0, fmt.Errorf("resolving maxUnavailable: %w", err)
		}
		limit = v
	}

	if limit < 1 {
		limit = 1
	}
	if limit > total {
		limit = total
	}
	return limit, nil
}

// NextBatch selects which not-yet-done, not-yet-in-progress nodes should
// start this pass, without exceeding the group's concurrency limit. It is
// safe to call every reconcile: nodes already done or already in progress
// are never re-selected, so resuming after a partial pass is idempotent.
func NextBatch(nodes []string, done, inProgress map[string]bool, batchSize *int32, maxUnavailable *intstr.IntOrString) ([]string, error) {
	limit, err := ResolveConcurrency(len(nodes), batchSize, maxUnavailable)
	if err != nil {
		return nil, err
	}

	available := limit - len(inProgress)
	if available <= 0 {
		return nil, nil
	}

	next := make([]string, 0, available)
	for _, n := range nodes {
		if done[n] || inProgress[n] {
			continue
		}
		next = append(next, n)
		if len(next) >= available {
			break
		}
	}
	return next, nil
}
