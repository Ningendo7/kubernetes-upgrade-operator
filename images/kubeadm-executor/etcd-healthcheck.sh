#!/bin/sh
set -eu

: "${ETCD_VERSION:?ETCD_VERSION must be set}"

export ETCD_VERSION
# Pinned etcdctl tarball checksum from the manager (see pkg/checksums).
# Empty means not pinned - only valid with ALLOW_UNPINNED_CHECKSUMS=true.
export ETCDCTL_SHA256="${ETCDCTL_SHA256:-}"
export ALLOW_UNPINNED_CHECKSUMS="${ALLOW_UNPINNED_CHECKSUMS:-false}"

# Everything from here on runs against the HOST's own namespaces - its own
# filesystem (for etcd's client certs) and network (for etcd's own client
# port), not this containers. Only --mount --net --pid are needed: this
# check touches no other host subsystem.
exec nsenter --target 1 --mount --net --pid -- /bin/sh -c '
set -eu

case "$(uname -m)" in
  x86_64)  ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

BASE_URL="${ETCD_RELEASE_BASE_URL:-https://github.com/etcd-io/etcd/releases/download}"
TARBALL="etcd-${ETCD_VERSION}-linux-${ARCH}.tar.gz"

echo "fetching etcdctl ${ETCD_VERSION} for linux/${ARCH}"
curl -fsSL -o /tmp/etcd.tar.gz "${BASE_URL}/${ETCD_VERSION}/${TARBALL}"

EXPECTED_SHA="${ETCDCTL_SHA256}"
if [ -z "${EXPECTED_SHA}" ]; then
  if [ "${ALLOW_UNPINNED_CHECKSUMS}" != "true" ]; then
    echo "no pinned checksum for etcdctl ${ETCD_VERSION}; refusing an unverifiable fetch" >&2
    exit 1
  fi
  echo "WARNING: etcdctl ${ETCD_VERSION} is not pinned; falling back to the SHA256SUMS served alongside the release" >&2
  curl -fsSL -o /tmp/etcd.sha256sums "${BASE_URL}/${ETCD_VERSION}/SHA256SUMS"
  EXPECTED_SHA="$(grep " ${TARBALL}\$" /tmp/etcd.sha256sums | awk "{print \$1}")"
  rm -f /tmp/etcd.sha256sums
fi
if [ -z "${EXPECTED_SHA}" ]; then
  echo "could not determine a checksum for ${TARBALL}" >&2
  exit 1
fi
echo "${EXPECTED_SHA}  /tmp/etcd.tar.gz" | sha256sum -c -

mkdir -p /tmp/etcd-extract
tar --no-same-owner -xzf /tmp/etcd.tar.gz -C /tmp/etcd-extract --strip-components=1 "etcd-${ETCD_VERSION}-linux-${ARCH}/etcdctl"
chmod 0755 /tmp/etcd-extract/etcdctl
rm -f /tmp/etcd.tar.gz

# Deliberately this nodes OWN local member only - never --cluster
# cross-discovery from a single node, which would make the whole quorum
# determination only as reliable as whichever one node answered it. The
# caller checks every control-plane nodes own member this same way and
# computes majority itself, mirroring the apiserver-proxy healthz check.
ETCDCTL_API=3 /tmp/etcd-extract/etcdctl \
  --endpoints=https://127.0.0.1:2379 \
  --cacert=/etc/kubernetes/pki/etcd/ca.crt \
  --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
  --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \
  endpoint health

rm -rf /tmp/etcd-extract
'
