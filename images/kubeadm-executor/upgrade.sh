#!/bin/sh
set -eu

: "${TARGET_VERSION:?TARGET_VERSION must be set}"
: "${UPGRADE_MODE:?UPGRADE_MODE must be set(apply or node)}"

case "${UPGRADE_MODE}" in
         apply|node) ;;
         *) echo "UPGRADE_MODE must be 'apply' or 'node', got ${UPGRADE_MODE}" >&2; exit 1 ;;
esac

export TARGET_VERSION UPGRADE_MODE

# Everything from here on runs against the HOST's own namespaces - its own
# filesystem, network, and binaries (curl, apt-get/dnf, kubeadm, systemctl),
# not this container's. That's deliberate: it's the closest equivalent to
# an admin SSH'd into the machine running these commands by hand.
exec nsenter --target 1 --mount --uts --ipc --net --pid -- /bin/sh -c '
set -eu

case "$(uname -m)" in
    x86_64) ARCH=amd64 ;;
    aarch64) ARCH=arm64 ;;
    *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

BASE_URL="${KUBEADM_RELEASE_BASE_URL:-https://dl.k8s.io/release}"

echo "fetching kubeadm ${TARGET_VERSION} for linux/${ARCH}"
curl -fsSL -o /tmp/kubeadm.new "${BASE_URL}/${TARGET_VERSION}/bin/linux/${ARCH}/kubeadm"
curl -fsSL -o /tmp/kubeadm.new.sha256 "${BASE_URL}/${TARGET_VERSION}/bin/linux/${ARCH}/kubeadm.sha256"
echo "$(cat /tmp/kubeadm.new.sha256)  /tmp/kubeadm.new" | sha256sum -c -
install -m 0755 /tmp/kubeadm.new /usr/bin/kubeadm
rm -f /tmp/kubeadm.new /tmp/kubeadm.new.sha256

KUBE_VERSION="${TARGET_VERSION#v}"

if command -v apt-get >/dev/null 2>&1; then
         apt-mark unhold kubelet >/dev/null 2>&1 || true
         apt-get update -y
         apt-get install -y --allow-change-held-packages "kubelet=${KUBE_VERSION}-*"
         apt-mark hold kubelet
elif command -v dnf >/dev/null 2>&1; then
         dnf install -y "kubelet-${KUBE_VERSION}-*"
else
         echo "Unsupported package manager: neither apt-get nor dnf found on this host" >&2
         exit 1
fi

if [ "${UPGRADE_MODE}" = "apply" ]; then
    kubeadm upgrade apply "${TARGET_VERSION}" -y
else
    kubeadm upgrade node
fi

systemctl daemon-reload
systemctl restart kubelet

INSTALLED="$(kubelet --version | awk "{print \$2}")"
if [ "${INSTALLED#v}" != "$KUBE_VERSION" ]; then
    echo "Kubelet version mismatch after upgrade: got ${INSTALLED}, want ${TARGET_VERSION}" >&2
    exit 1
fi

echo "upgrade to ${TARGET_VERSION} complete"
'