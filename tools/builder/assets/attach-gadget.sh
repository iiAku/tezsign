#!/bin/bash
set -euo pipefail

GADGET_DIR="/sys/kernel/config/usb_gadget/g1"

# Find a UDC that actually exists
UDC="$(ls /sys/class/udc 2>/dev/null | head -n1 || true)"

if [[ -z "${UDC}" ]]; then
  echo "No UDC available; cannot attach gadget now."
  exit 1
fi

if grep -q "^${UDC}$" "${GADGET_DIR}/UDC" 2>/dev/null; then
  echo "UDC is already set to ${UDC}."
  exit 0
fi

echo "${UDC}" > "${GADGET_DIR}/UDC"
echo "Attached gadget to UDC: ${UDC}"

# create link to soft_connect
ln -sf "/sys/class/udc/${UDC}/soft_connect" "/tmp/soft_connect"
chown registrar:registrar /tmp/soft_connect # allow ffs_registrar to access it

# Wait for endpoints and fix ownership (with 60s timeout)
ENDPOINT_TIMEOUT=60
for ep in /dev/ffs/tezsign/ep1 /dev/ffs/tezsign/ep2 /dev/ffs/tezsign/ep3 /dev/ffs/tezsign/ep4; do
  count=0
  while [[ ! -e "$ep" ]]; do
    sleep 1
    count=$((count + 1))
    if [[ $count -ge $ENDPOINT_TIMEOUT ]]; then
      echo "Timeout (${ENDPOINT_TIMEOUT}s) waiting for endpoint: $ep"
      exit 1
    fi
  done
  chown tezsign:tezsign "$ep"
done
