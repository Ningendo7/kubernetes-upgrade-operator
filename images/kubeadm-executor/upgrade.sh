#!/bin/sh
set -eu

: "${TARGET_VERSION:?TARGET_VERSION must be set}"
: "${UPGRADE_MODE:?UPGRADE_MODE must be set(apply or node)}"

case "${UPGRADE_MODE}" in
         apply|node) ;;
         *) echo "UPGRADE_MODE must be 'apply' or 'node', got ${UPGRADE_MODE}" >&2; exit 1 ;;
esac

export TARGET_VERSION UPGRADE_MODE
# The manager passes pinned SHA-256 values for the artifacts fetched below
# (see pkg/checksums). Empty means "not pinned" - only allowed when
# ALLOW_UNPINNED_CHECKSUMS=true, in which case we fall back to a checksum
# fetched alongside the artifact, which is an integrity check only, not an
# authenticity one (see SECURITY.md).
export KUBEADM_SHA256="${KUBEADM_SHA256:-}"
export KUBELET_DEB_SHA256="${KUBELET_DEB_SHA256:-}"
export ALLOW_UNPINNED_CHECKSUMS="${ALLOW_UNPINNED_CHECKSUMS:-false}"

# Everything from here on runs against the HOST's own namespaces - its own
# filesystem, network, and binaries (curl, apt-get/dnf, kubeadm, systemctl),
# not this container's. That's deliberate: it's the closest equivalent to
# an admin SSH'd into the machine running these commands by hand.
exec nsenter --target 1 --mount --uts --ipc --net --pid -- /bin/sh -c '
set -eu

case "$(uname -m)" in
  x86_64)  ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

BASE_URL="${KUBEADM_RELEASE_BASE_URL:-https://dl.k8s.io/release}"

echo "fetching kubeadm ${TARGET_VERSION} for linux/${ARCH}"
curl -fsSL -o /tmp/kubeadm.new "${BASE_URL}/${TARGET_VERSION}/bin/linux/${ARCH}/kubeadm"

EXPECTED_KUBEADM_SHA="${KUBEADM_SHA256}"
if [ -z "${EXPECTED_KUBEADM_SHA}" ]; then
  if [ "${ALLOW_UNPINNED_CHECKSUMS}" != "true" ]; then
    echo "no pinned checksum for kubeadm ${TARGET_VERSION}; refusing an unverifiable fetch" >&2
    exit 1
  fi
  echo "WARNING: kubeadm ${TARGET_VERSION} is not in the pinned checksum table; falling back to the checksum served alongside the binary" >&2
  EXPECTED_KUBEADM_SHA="$(curl -fsSL "${BASE_URL}/${TARGET_VERSION}/bin/linux/${ARCH}/kubeadm.sha256")"
fi
echo "${EXPECTED_KUBEADM_SHA}  /tmp/kubeadm.new" | sha256sum -c -
install -m 0755 /tmp/kubeadm.new /usr/bin/kubeadm
rm -f /tmp/kubeadm.new

KUBE_VERSION="${TARGET_VERSION#v}"

# kubeadm upgrade apply/node must run BEFORE the kubelet package is
# touched: it expects the currently-running kubelet during its own
# health checks and manifest updates. Upgrading kubelet first risks it
# picking up control-plane manifests kubeadm has not finished writing yet.
if [ "${UPGRADE_MODE}" = "apply" ]; then
  # kubeadm upgrade apply also prepulls the new control-plane component
  # images by connecting to the CRI socket directly, which needs
  # DAC_OVERRIDE-ish access to /var/run/containerd/containerd.sock that
  # this capability-limited root does not have. Skipping that check is
  # safe: the images still get pulled normally, by the fully-privileged
  # kubelet, when it actually creates the new static pods below. Newer
  # kubeadm versions also construct a CRI *runtime* service client
  # (not just the image client) as part of this same prepull step,
  # which fails the same way and is not individually named, so this
  # ignores every preflight check rather than guessing at one - the
  # cluster-health and version-skew checks that already ran above are
  # the load-bearing safety checks, not this optional prepull step.
  kubeadm upgrade apply "${TARGET_VERSION}" -y --ignore-preflight-errors=all
else
  kubeadm upgrade node
fi

if command -v dpkg >/dev/null 2>&1; then
  # Ubuntu/Debian systems ship /var/lib/apt/lists/partial owned by the
  # unprivileged _apt user - a capability-limited root (this process)
  # cannot chmod or unlink files it does not own there, no matter how
  # apt-get sandboxing is configured, so apt-get itself cannot run here
  # at all. Fetching the exact .deb directly and installing it with
  # dpkg avoids apt-get list management entirely - it only touches the
  # dpkg database, which is root owned. Same checksum-verified direct
  # fetch pattern as the kubeadm binary above.
  MINOR_CHANNEL="$(printf "%s" "${KUBE_VERSION}" | cut -d. -f1,2)"
  DEB_BASE_URL="${KUBE_DEB_REPO_BASE_URL:-https://pkgs.k8s.io/core:/stable:/v${MINOR_CHANNEL}/deb}"
  PKG_INDEX="$(curl -fsSL "${DEB_BASE_URL}/Packages")"
  RESULT="$(printf "%s" "${PKG_INDEX}" | awk -v arch="${ARCH}" -v ver="${KUBE_VERSION}-" "
    BEGIN { RS=\"\"; FS=\"\n\" }
    {
      p=\"\"; a=\"\"; v=\"\"; f=\"\"; s=\"\"
      for (i=1;i<=NF;i++) {
        line=\$i
        if (line ~ /^Package: /)           { p=line; sub(/^Package: /,\"\",p) }
        else if (line ~ /^Architecture: /)  { a=line; sub(/^Architecture: /,\"\",a) }
        else if (line ~ /^Version: /)       { v=line; sub(/^Version: /,\"\",v) }
        else if (line ~ /^Filename: /)      { f=line; sub(/^Filename: /,\"\",f) }
        else if (line ~ /^SHA256: /)        { s=line; sub(/^SHA256: /,\"\",s) }
      }
      if (p==\"kubelet\" && a==arch && index(v, ver)==1) { print f, s; exit }
    }
  ")"
  if [ -z "${RESULT}" ]; then
    echo "could not find a kubelet ${KUBE_VERSION} package for ${ARCH} in ${DEB_BASE_URL}" >&2
    exit 1
  fi
  DEB_PATH="${RESULT%% *}"
  INDEX_DEB_SHA256="${RESULT##* }"

  EXPECTED_KUBELET_DEB_SHA="${KUBELET_DEB_SHA256}"
  if [ -z "${EXPECTED_KUBELET_DEB_SHA}" ]; then
    if [ "${ALLOW_UNPINNED_CHECKSUMS}" != "true" ]; then
      echo "no pinned checksum for the kubelet ${KUBE_VERSION} package; refusing an unverifiable fetch" >&2
      exit 1
    fi
    echo "WARNING: kubelet ${KUBE_VERSION} package is not pinned; falling back to the checksum from the package index" >&2
    EXPECTED_KUBELET_DEB_SHA="${INDEX_DEB_SHA256}"
  fi
  curl -fsSL -o /tmp/kubelet.new.deb "${DEB_BASE_URL}/${DEB_PATH}"
  echo "${EXPECTED_KUBELET_DEB_SHA}  /tmp/kubelet.new.deb" | sha256sum -c -

  # dpkg -i does not resolve dependencies the way apt-get normally would -
  # a host whose kubelet was never installed with real apt dependency
  # resolution (e.g. this dpkg path is all it has ever gone through) can
  # be missing plain packages kubelet depends on (conntrack, ethtool,
  # socat, ebtables, and similar). Read the REAL dependency list out of
  # the .deb itself rather than guessing/hardcoding one, and fetch any
  # that are missing the same checksum-verified way, from the distro
  # archive rather than the Kubernetes package repo.
  MISSING_DEPS="$(
    dpkg-deb -f /tmp/kubelet.new.deb Depends |
      tr "," "\n" |
      sed -e "s/^ *//" -e "s/ *(.*//" -e "s/ .*//" |
      while read -r dep; do
        dpkg -s "$dep" >/dev/null 2>&1 || echo "$dep"
      done
  )"

  if [ -n "${MISSING_DEPS}" ]; then
    . /etc/os-release
    case "${ARCH}" in
      amd64) OS_ARCHIVE_DEFAULT="http://archive.ubuntu.com/ubuntu" ;;
      arm64) OS_ARCHIVE_DEFAULT="http://ports.ubuntu.com/ubuntu-ports" ;;
    esac
    if [ "${ID:-}" = "debian" ]; then
      OS_ARCHIVE_DEFAULT="http://deb.debian.org/debian"
    fi
    OS_ARCHIVE_BASE_URL="${OS_PACKAGE_ARCHIVE_BASE_URL:-${OS_ARCHIVE_DEFAULT}}"
    OS_INDEX="$(curl -fsSL "${OS_ARCHIVE_BASE_URL}/dists/${VERSION_CODENAME}/main/binary-${ARCH}/Packages.gz" | zcat)"

    for dep in ${MISSING_DEPS}; do
      DEP_RESULT="$(printf "%s" "${OS_INDEX}" | awk -v pkg="${dep}" -v arch="${ARCH}" "
        BEGIN { RS=\"\"; FS=\"\n\" }
        {
          p=\"\"; a=\"\"; f=\"\"; s=\"\"
          for (i=1;i<=NF;i++) {
            line=\$i
            if (line ~ /^Package: /)           { p=line; sub(/^Package: /,\"\",p) }
            else if (line ~ /^Architecture: /)  { a=line; sub(/^Architecture: /,\"\",a) }
            else if (line ~ /^Filename: /)      { f=line; sub(/^Filename: /,\"\",f) }
            else if (line ~ /^SHA256: /)        { s=line; sub(/^SHA256: /,\"\",s) }
          }
          if (p==pkg && a==arch) { print f, s; exit }
        }
      ")"
      if [ -z "${DEP_RESULT}" ]; then
        echo "could not find dependency package ${dep} for ${ARCH} in ${OS_ARCHIVE_BASE_URL}" >&2
        exit 1
      fi
      DEP_PATH="${DEP_RESULT%% *}"
      DEP_SHA256="${DEP_RESULT##* }"
      curl -fsSL -o "/tmp/${dep}.deb" "${OS_ARCHIVE_BASE_URL}/${DEP_PATH}"
      echo "${DEP_SHA256}  /tmp/${dep}.deb" | sha256sum -c -
      dpkg -i "/tmp/${dep}.deb"
      rm -f "/tmp/${dep}.deb"
    done
  fi

  dpkg -i /tmp/kubelet.new.deb
  rm -f /tmp/kubelet.new.deb
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y "kubelet-${KUBE_VERSION}"
else
  echo "unsupported package manager: neither dpkg nor dnf found on this host" >&2
  exit 1
fi

systemctl daemon-reload
systemctl restart kubelet

INSTALLED="$(kubelet --version | awk "{print \$2}")"
if [ "${INSTALLED#v}" != "${KUBE_VERSION}" ]; then
  echo "kubelet version mismatch after upgrade: got ${INSTALLED}, want ${TARGET_VERSION}" >&2
  exit 1
fi

echo "upgrade to ${TARGET_VERSION} complete"
'