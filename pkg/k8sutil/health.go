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
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ControlPlaneLabelKey is the current well-known node label marking control-plane nodes.
	ControlPlaneLabelKey = "node-role.kubernetes.io/control-plane"
	// legacyControlPlaneLabelKey was used by older kubeadm versions; some
	// clusters still carry it instead of (or alongside) ControlPlaneLabelKey.
	LegacyControlPlaneLabelKey = "node-role.kubernetes.io/master"
)

// EtcdQuorumStatus is a point-in-time check of etcd's health as reported
// by each control-plane node's own apiserver.
type EtcdQuorumStatus struct {
	Healthy        bool
	TotalMembers   int
	HealthyMembers int
	Reason         string
}

// CheckEtcdQuorumViaAPIServers queries each control-plane node's own
// apiserver /healthz/etcd endpoint directly - by IP, bypassing any load
// balancer or Service, so each replica's own view is checked individually
// - and reports whether a majority report etcd healthy. This is a real
// signal (the apiserver's own etcd client), not an inference from
// unrelated Node conditions - but it's still a proxy for genuine
// cross-member quorum: it reflects "does this apiserver's local etcd
// connection look healthy," not a raft-level view of the whole etcd
// cluster (see the README for the tradeoff and the deferred, more
// thorough etcdctl-based alternative).
func CheckEtcdQuorumViaAPIServers(ctx context.Context, cfg *rest.Config, nodes []corev1.Node) (EtcdQuorumStatus, error) {
	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return EtcdQuorumStatus{}, fmt.Errorf("building transport for control-plane health checks: %w", err)
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	urls := make([]string, 0, len(nodes))
	for i := range nodes {
		if addr := nodeInternalIP(&nodes[i]); addr != "" {
			urls = append(urls, fmt.Sprintf("https://%s:6443/healthz/etcd", addr))
		}
	}
	return checkEtcdHealthzEndpoints(ctx, httpClient, urls), nil
}

// checkEtcdHealthzEndpoints is the directly-testable core: given a set of
// /healthz/etcd URLs and an http.Client already configured with whatever
// TLS/auth it needs, report whether a majority respond 200 OK.
func checkEtcdHealthzEndpoints(ctx context.Context, httpClient *http.Client, urls []string) EtcdQuorumStatus {
	if len(urls) == 0 {
		return EtcdQuorumStatus{
			Healthy: true,
			Reason:  "no control-plane apiservers to check",
		}
	}

	healthy := 0
	for _, url := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}
		ok := resp.StatusCode == http.StatusOK
		_ = resp.Body.Close()
		if ok {
			healthy++
		}
	}

	total := len(urls)
	quorum := total/2 + 1
	status := EtcdQuorumStatus{
		TotalMembers:   total,
		HealthyMembers: healthy,
	}
	if healthy >= quorum {
		status.Healthy = true
		return status
	}
	status.Reason = fmt.Sprintf("only %d/%d control-plane apiservers report etcd healthy, need at least %d", healthy, total, quorum)
	return status
}

func nodeInternalIP(node *corev1.Node) string {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			return addr.Address
		}
	}
	return ""
}

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
	Healthy    bool
	TotalNodes int
	ReadyNodes int
	Reason     string
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

// GetRunningEtcdVersion returns the etcd version actually running in the
// cluster (e.g. "v3.5.24"), read from a live etcd static pod's own image
// tag rather than assumed - so a real etcdctl fetch can be pinned to a
// version known to match, rather than guessing one that might not
// interoperate with whatever this specific cluster is actually running.
func GetRunningEtcdVersion(ctx context.Context, c client.Client) (string, error) {
	var pods corev1.PodList
	if err := c.List(ctx, &pods,
		client.InNamespace("kube-system"),
		client.MatchingLabels{"component": "etcd"},
	); err != nil {
		return "", fmt.Errorf("listing etcd pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no etcd pods found in kube-system")
	}
	if len(pods.Items[0].Spec.Containers) == 0 {
		return "", fmt.Errorf("etcd pod %q has no containers", pods.Items[0].Name)
	}

	image := pods.Items[0].Spec.Containers[0].Image
	_, tag, ok := strings.Cut(image, ":")
	if !ok {
		return "", fmt.Errorf("etcd pod image %q has no tag", image)
	}
	// Kubernetes' own etcd image tags carry a build revision suffix (e.g.
	// "3.5.24-0") that is not part of etcd's own release versioning.
	version, _, _ := strings.Cut(tag, "-")
	return "v" + version, nil
}
