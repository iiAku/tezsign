#!/usr/bin/env bash

err() {
  echo "[error] $1" >&2
}

die() {
  err "$1"
  exit 1
}

if [[ $EUID -ne 0 ]]; then
  die "This script must be run as root (e.g. via sudo)."
fi

DEV_IF_RULE="/etc/udev/rules.d/50-usb-gadget.rules"
DEV_NET_RULE="/etc/udev/rules.d/99-usb-network.rules"

cat <<'RULE' > "${DEV_IF_RULE}" || die "Failed to write ${DEV_IF_RULE}."
SUBSYSTEM=="net", ACTION=="add", ATTR{address}=="ae:d3:e6:cd:ff:f3", NAME="tezsign_dev"
RULE

cat <<'RULE' > "${DEV_NET_RULE}" || die "Failed to write ${DEV_NET_RULE}."
SUBSYSTEM=="net", ACTION=="add", ATTR{address}=="ae:d3:e6:cd:ff:f3", RUN+="/usr/bin/ip addr add 10.10.10.2/24 dev tezsign_dev", RUN+="/usr/bin/ip link set dev tezsign_dev up"
RULE

if ! udevadm control --reload-rules; then
  die "Failed to reload udev rules."
fi

if ! udevadm trigger; then
  die "Failed to trigger udev reload."
fi

printf 'Installed tezsign dev udev rules at %s and %s\n' "${DEV_IF_RULE}" "${DEV_NET_RULE}"
printf 'You can now connect to the dev gadget over SSH via dev@10.10.10.1.\n'
