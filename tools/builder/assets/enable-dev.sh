#!/bin/bash

readonly DEV_ID="1002"

### 1) Create 'dev' user if it doesn't exist
if ! id -u "dev" >/dev/null 2>&1; then
    useradd -m -u "${DEV_ID}" -s /usr/bin/bash "dev"
fi

### 2) Set password and add to sudo group
echo "dev:tezsign" | chpasswd
usermod -aG sudo "dev"

### 3) (NEW) Generate SSH host keys before making the filesystem read-only
echo "[*] Ensuring SSH host keys exist..."
mkdir -p /etc/ssh
ssh-keygen -A

# Tighten permissions
chmod 600 /etc/ssh/ssh_host_*_key 2>/dev/null
chmod 644 /etc/ssh/ssh_host_*_key.pub 2>/dev/null

### 4) Restore sudo functionality
chmod u+s /usr/bin/sudo

echo "[+] Development mode enabled: 'dev' user created with sudo access."