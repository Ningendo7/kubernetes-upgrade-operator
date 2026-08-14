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

const (
	// ControlPlaneLabelKey is the current well-known node label marking control-plane nodes.
	ControlPlaneLabelKey = "node-role.kubernetes.io/control-plane"
	// legacyControlPlaneLabelKey was used by older kubeadm versions; some
	// clusters still carry it instead of (or alongside) ControlPlaneLabelKey.
	LegacyControlPlaneLabelKey = "node-role.kubernetes.io/master"
)

// ListControlPlaneNodes returns all nodes labeled as control-plane, under
// either the current or legacy label key, deduplicated.
func ListControlPlaneNodes(ctx context.Context, c client.Client) ([]corev1.Node, error) {
	seen := map[string]corev1.Node{}
	for _, key := range []string{ControlPlaneLabelKey, LegacyControlPlaneLabelKey} {
		var nodes corev1.NodeList
		if err := c.List(ctx, &nodes, client.MatchingLabels{key: ""}); err != nil {
			return nil, fmt.Errorf("listing nodes labeled %q: %w", key, err)
		}
		for _, n := range nodes.Items {
			seen[n.Name] = n
		}
	}
	result := make([]corev1.Node, 0, len(seen))
	for _, n := range seen {
		result = append(result, n)
	}
	return result, nil
}

// ControlPlaneHealth summarizes a point-in-time proxy health check.
type ControlPlaneHealth struct {
	Healthy 	bool
	TotalNodes int
	ReadyNodes int
	Reason  	string
}

// CheckControlPlaneHealth reports whether a majority of control-plane nodes
// are Ready. This is a proxy for etcd quorum health: reaching etcd directly
// requires privileged access this controller doesn't have in MVP, so a
// Ready-majority of CP nodes stands in for it. Clusters with no visible
// control-plane Nodes (e.g. EKS/LKE, where the control plane is managed and
// not represented as schedulable Nodes) report Healthy=true trivially, since
// there's nothing for this controller to gate on.
func CheckControlPlaneHealth(ctx context.Context, c client.Client) (ControlPlaneHealth, error) {
	nodes, err := ListControlPlaneNodes(ctx, c)
	if err != nil {
		return ControlPlaneHealth{}, err
	}

	total := len(nodes)
	ready := 0
	for i := range nodes {
		if IsNodeReady(&nodes[i]) {
			ready++
		}
	}

	health := ControlPlaneHealth{
		TotalNodes: total,
		ReadyNodes: ready,
	}
	
	if total == 0 {
		health.Healthy = true
		health.Reason = "no control-plane nodes found (managed control plane, or labels missing)"
		return health, nil
	}

	quorum := total/2 + 1
	if ready >= quorum {
		health.Healthy = true
		return health, nil
	}

	health.Reason = fmt.Sprintf("only %d/%d control-plane nodes Ready, need at least %d for quorum", ready, total, quorum)
	return health, nil
}