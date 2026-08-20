# This image's only job is to nsenter into the host's namespaces and hand
# off to whatever's already on the host (kubeadm's own package manager,
# curl, systemctl). It deliberately does NOT bundle kubeadm/kubelet
# binaries, curl, or a package manager itself - once nsenter switches
# namespaces, commands resolve against the HOST's own binaries and
# filesystem, not this image's. Keeping this image's own footprint this
# small is itself a hardening measure: it runs with SYS_ADMIN on every
# node it touches, so less software in here means less attack surface.
FROM debian:12-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends util-linux && \
    rm -rf /var/lib/apt/lists/*

COPY upgrade.sh /usr/local/bin/upgrade.sh
RUN chmod 0755 /usr/local/bin/upgrade.sh

ENTRYPOINT ["/usr/local/bin/upgrade.sh"]