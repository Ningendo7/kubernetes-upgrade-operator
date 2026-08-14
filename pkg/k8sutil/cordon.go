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
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Cordon marks a node unschedulable, preventing new pods from being
// scheduled onto it. It is a no-op if the node is already cordoned.
func Cordon(ctx context.Context, c client.Client, nodeName string) error {
	return setUnschedulable(ctx, c, nodeName, true)
}

// Uncordon marks a node schedulable again. It is a no-op if the node is
// already schedulable.
func Uncordon(ctx context.Context, c client.Client, nodeName string) error {
	return setUnschedulable(ctx, c, nodeName, false)
}

func setUnschedulable(ctx context.Context, c client.Client, nodeName string, unschedulable bool) error {
	var node corev1.Node
	if err := c.Get(ctx, client.ObjectKey{Name: nodeName}, &node); err != nil {
		return fmt.Errorf("getting node %q: %w", nodeName, err)
	}

	if node.Spec.Unschedulable == unschedulable {
		return nil
	}

	patch := client.MergeFrom(node.DeepCopy())
	node.Spec.Unschedulable = unschedulable

	if err := c.Patch(ctx, &node, patch); err != nil {
		return fmt.Errorf("patching node %q unschedulable=%v: %w", nodeName, unschedulable, err)
	}
	return nil
}
