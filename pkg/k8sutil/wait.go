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

// GetNode fetches a single Node by name.
func GetNode(ctx context.Context, c client.Client, name string) (*corev1.Node, error) {
	var node corev1.Node
	if err := c.Get(ctx, client.ObjectKey{Name: name}, &node); err != nil {
		return nil, fmt.Errorf("getting node %q: %w", name, err)
	}

	return &node, nil
}

// IsNodeReady reports whether node's NodeReady condition is True.
func IsNodeReady(node *corev1.Node) bool {
	cond := readyCondition(node)
	return cond != nil && cond.Status == corev1.ConditionTrue
}

// NodeNotReadyReason returns a human-readable explanation for why a node
// is not ready, or "" if it is ready.
func NodeNotReadyReason(node *corev1.Node) string {
	cond := readyCondition(node)
	if cond == nil {
		return "node has not reported a Ready condition yet"
	}
	if cond.Status == corev1.ConditionTrue {
		return ""
	}
	return fmt.Sprintf("condition Ready is %s: %s - %s", cond.Status, cond.Reason, cond.Message)
}

func readyCondition(node *corev1.Node) *corev1.NodeCondition {
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == corev1.NodeReady {
			return &node.Status.Conditions[i]
		}
	}
	return nil
}

// NodeIsAtVersion reports whether the node's kubelet is running exactly targetVersion.
func NodeIsAtVersion(node *corev1.Node, targetVersion string) (bool, error) {
	cmp, err := CompareVersions(node.Status.NodeInfo.KubeletVersion, targetVersion)
	if err != nil {
		return false, err
	}
	return cmp == 0, nil
}
