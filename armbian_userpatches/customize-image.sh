#!/bin/bash

set -e

# This command checks if the package status is NOT 'no such package' (i.e., it exists)
# and then purges only the packages that are found.

grep -Fvf <(dpkg-query -W -f='${Package}\n' | sed 's/^/#/') /tmp/overlay/packages_to_purge.txt | \
    xargs sudo apt purge --assume-yes

touch /root/.no_rootfs_resize