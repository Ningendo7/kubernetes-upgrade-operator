#!/usr/bin/env bash
#
# Regenerates pkg/checksums/checksums.yaml - the pinned, known-good SHA-256
# table the executor Jobs verify fetched binaries against, instead of
# trusting a checksum served alongside the binary from the same source.
#
# Run this by hand when adding support for a new Kubernetes (or etcd)
# version, review the diff, and commit it. The trust model is: a human
# runs this, cross-checks the output against the official release notes /
# signatures, and the committed file is what a reviewer signs off on -
# NOT whatever a CDN happens to serve at upgrade time, unattended, forever.
#
# Requires: curl, sha256sum, gzip.
set -euo pipefail

# --- what to pin ------------------------------------------------------------
# Add versions here, rerun, review the diff, commit.
KUBE_VERSIONS=(
  v1.29.15
  v1.30.0
  v1.30.14
  v1.31.0
  v1.31.9
  v1.31.14
  v1.32.13
)
ETCD_VERSIONS=(
  v3.5.16
  v3.5.24
)
ARCHES=(amd64 arm64)

KUBE_RELEASE_BASE="${KUBE_RELEASE_BASE:-https://dl.k8s.io/release}"
KUBE_DEB_REPO_BASE="${KUBE_DEB_REPO_BASE:-https://pkgs.k8s.io/core:/stable:/v%s/deb}"
ETCD_RELEASE_BASE="${ETCD_RELEASE_BASE:-https://github.com/etcd-io/etcd/releases/download}"

OUT="$(cd "$(dirname "$0")/.." && pwd)/pkg/checksums/checksums.yaml"
# -------------------------------------------------------------------------

kubeadm_sha() { # version arch
  curl -fsSL "${KUBE_RELEASE_BASE}/$1/bin/linux/$2/kubeadm.sha256"
}

kubelet_deb_sha() { # version arch  (version without leading v)
  local ver="${1#v}" arch="$2" minor base tmp
  minor="$(printf %s "$ver" | cut -d. -f1,2)"
  base="$(printf "$KUBE_DEB_REPO_BASE" "$minor")"
  tmp="$(mktemp)"
  curl -fsSL "${base}/Packages" >"$tmp"
  awk -v arch="$arch" -v ver="${ver}-" '
    BEGIN { RS=""; FS="\n" }
    {
      p=""; a=""; v=""; s=""
      for (i=1;i<=NF;i++) {
        line=$i
        if (line ~ /^Package: /)          { p=line; sub(/^Package: /,"",p) }
        else if (line ~ /^Architecture: /) { a=line; sub(/^Architecture: /,"",a) }
        else if (line ~ /^Version: /)      { v=line; sub(/^Version: /,"",v) }
        else if (line ~ /^SHA256: /)       { s=line; sub(/^SHA256: /,"",s) }
      }
      if (p=="kubelet" && a==arch && index(v, ver)==1) { print s; found=1; exit }
    }
    END { if (!found) exit 0 }
  ' "$tmp"
  rm -f "$tmp"
}

etcdctl_tarball_sha() { # version arch
  local tmp
  tmp="$(mktemp)"
  curl -fsSL "${ETCD_RELEASE_BASE}/$1/SHA256SUMS" >"$tmp"
  awk -v want="etcd-$1-linux-$2.tar.gz" '$2 == want { print $1; exit }' "$tmp"
  rm -f "$tmp"
}

emit() { printf '%s\n' "$1" >>"$OUT"; }

: >"$OUT"
emit "# Pinned, known-good SHA-256 checksums for the artifacts the executor"
emit "# Jobs fetch. Regenerate with hack/update-checksums.sh; review and commit."
emit "# DO NOT edit values by hand."
emit "versions:"
for v in "${KUBE_VERSIONS[@]}"; do
  emit "  ${v}:"
  for arch in "${ARCHES[@]}"; do
    ka="$(kubeadm_sha "$v" "$arch")"
    kd="$(kubelet_deb_sha "$v" "$arch")"
    if [ -z "$ka" ] || [ -z "$kd" ]; then
      echo "WARN: missing checksum for kubernetes ${v}/${arch} (kubeadm='${ka}' kubelet_deb='${kd}')" >&2
    fi
    emit "    ${arch}:"
    emit "      kubeadm: ${ka}"
    emit "      kubelet_deb: ${kd}"
  done
done
emit "etcd:"
for v in "${ETCD_VERSIONS[@]}"; do
  emit "  ${v}:"
  for arch in "${ARCHES[@]}"; do
    et="$(etcdctl_tarball_sha "$v" "$arch")"
    [ -z "$et" ] && echo "WARN: missing etcdctl checksum for ${v}/${arch}" >&2
    emit "    ${arch}:"
    emit "      etcdctl_tarball: ${et}"
  done
done

echo "wrote ${OUT}"
