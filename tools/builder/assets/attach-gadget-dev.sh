#!/bin/bash
set -euo pipefail

IP_ADDRESS="10.10.10.1/24"
INTERFACE="usb0"

if [[ ! -e "/sys/class/net/${INTERFACE}" ]]; then
  echo "Interface ${INTERFACE} did not appear within yet"
  exit 1
fi

# If already configured, exit cleanly (idempotent)
if ! /sbin/ip addr show "${INTERFACE}" | grep -q "${IP_ADDRESS}"; then
  echo "Configuring ${INTERFACE} with ${IP_ADDRESS}..."
  /sbin/ip addr add "${IP_ADDRESS}" dev "${INTERFACE}"
fi

/sbin/ip link set "${INTERFACE}" up
echo "Done."
