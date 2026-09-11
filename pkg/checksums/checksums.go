package checksums

import (
	_ "embed"
	"fmt"

	"sigs.k8s.io/yaml"
)

//go:embed checksums.yaml
var raw []byte

// KubeSet is the pinned SHA-256 checksums for one (Kubernetes version, arch).
type KubeSet struct {
	Kubeadm    string `json:"kubeadm"`
	KubeletDeb string `json:"kubelet_deb"`
}

// EtcdSet is the pinned SHA-256 checksum for one (etcd version, arch).
type EtcdSet struct {
	EtcdctlTarball string `json:"etcdctl_tarball"`
}

type table struct {
	Versions map[string]map[string]KubeSet `json:"versions"`
	Etcd     map[string]map[string]EtcdSet `json:"etcd"`
}

var parsed table

func init() {
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		// The file is embedded at build time and regenerated only by
		// hack/update-checksums.sh - a parse failure is a build-time
		// mistake, not a runtime condition to handle.
		panic(fmt.Sprintf("checksums: embedded checksums.yaml is invalid: %v", err))
	}
}

// LookupKube returns the pinned checksums for a Kubernetes version and
// arch (e.g. "v1.31.9", "amd64"). A missing or incomplete entry is an
// error, never a zero value: callers must fail closed - refuse the
// upgrade - rather than fall back to fetching something unverifiable.
func LookupKube(version, arch string) (KubeSet, error) {
	byArch, ok := parsed.Versions[version]
	if !ok {
		return KubeSet{}, fmt.Errorf("no pinned checksums for Kubernetes %s; add it via hack/update-checksums.sh", version)
	}
	set, ok := byArch[arch]
	if !ok {
		return KubeSet{}, fmt.Errorf("no pinned checksums for Kubernetes %s on %s", version, arch)
	}
	if set.Kubeadm == "" || set.KubeletDeb == "" {
		return KubeSet{}, fmt.Errorf("pinned checksums for Kubernetes %s/%s are incomplete", version, arch)
	}
	return set, nil
}

// LookupEtcd is the etcd equivalent of LookupKube.
func LookupEtcd(version, arch string) (EtcdSet, error) {
	byArch, ok := parsed.Etcd[version]
	if !ok {
		return EtcdSet{}, fmt.Errorf("no pinned checksum for etcd %s", version)
	}
	set, ok := byArch[arch]
	if !ok || set.EtcdctlTarball == "" {
		return EtcdSet{}, fmt.Errorf("no pinned checksum for etcd %s on %s", version, arch)
	}
	return set, nil
}
