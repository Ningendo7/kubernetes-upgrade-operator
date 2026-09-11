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

package checksums

import (
	"regexp"
	"testing"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestLookupKube_KnownVersionReturnsBothChecksums(t *testing.T) {
	// A version we have actually exercised end-to-end against a real
	// cluster - if this ever fails, the embedded table was regenerated
	// without it, which would silently break upgrades to that version.
	set, err := LookupKube("v1.31.9", "amd64")
	if err != nil {
		t.Fatalf("LookupKube: %v", err)
	}
	if !sha256Hex.MatchString(set.Kubeadm) {
		t.Errorf("kubeadm checksum %q is not a 64-char hex sha256", set.Kubeadm)
	}
	if !sha256Hex.MatchString(set.KubeletDeb) {
		t.Errorf("kubelet_deb checksum %q is not a 64-char hex sha256", set.KubeletDeb)
	}
}

func TestLookupKube_UnknownVersionFailsClosed(t *testing.T) {
	if _, err := LookupKube("v9.99.0", "amd64"); err == nil {
		t.Fatal("expected an error for an unpinned version, got nil - callers must fail closed")
	}
}

func TestLookupKube_UnknownArchFailsClosed(t *testing.T) {
	if _, err := LookupKube("v1.31.9", "riscv64"); err == nil {
		t.Fatal("expected an error for an unpinned arch, got nil")
	}
}

func TestLookupEtcd_KnownVersion(t *testing.T) {
	set, err := LookupEtcd("v3.5.24", "amd64")
	if err != nil {
		t.Fatalf("LookupEtcd: %v", err)
	}
	if !sha256Hex.MatchString(set.EtcdctlTarball) {
		t.Errorf("etcdctl_tarball checksum %q is not a 64-char hex sha256", set.EtcdctlTarball)
	}
}

func TestLookupEtcd_UnknownVersionFailsClosed(t *testing.T) {
	if _, err := LookupEtcd("v9.9.9", "amd64"); err == nil {
		t.Fatal("expected an error for an unpinned etcd version, got nil")
	}
}

// TestEmbeddedTableIsWellFormed catches a regenerated table that parsed
// but left entries empty (e.g. update-checksums.sh printed a WARN and the
// value silently became "").
func TestEmbeddedTableIsWellFormed(t *testing.T) {
	if len(parsed.Versions) == 0 {
		t.Fatal("embedded table has no Kubernetes versions")
	}
	for version, byArch := range parsed.Versions {
		if len(byArch) == 0 {
			t.Errorf("kubernetes %s has no arches", version)
		}
		for arch, set := range byArch {
			if !sha256Hex.MatchString(set.Kubeadm) {
				t.Errorf("kubernetes %s/%s: kubeadm checksum %q is not a valid sha256", version, arch, set.Kubeadm)
			}
			if !sha256Hex.MatchString(set.KubeletDeb) {
				t.Errorf("kubernetes %s/%s: kubelet_deb checksum %q is not a valid sha256", version, arch, set.KubeletDeb)
			}
		}
	}
	if len(parsed.Etcd) == 0 {
		t.Fatal("embedded table has no etcd versions")
	}
	for version, byArch := range parsed.Etcd {
		for arch, set := range byArch {
			if !sha256Hex.MatchString(set.EtcdctlTarball) {
				t.Errorf("etcd %s/%s: etcdctl_tarball checksum %q is not a valid sha256", version, arch, set.EtcdctlTarball)
			}
		}
	}
}
