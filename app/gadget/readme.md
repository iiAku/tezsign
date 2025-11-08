# Raspberry Pi 4 USB FunctionFS Gadget

If you’re cross-compiling from x86_64 → arm64 u need these for blst:
```bash
sudo apt-get install gcc-aarch64-linux-gnu g++-aarch64-linux-gnu
export CC="zig cc -target aarch64-linux-musl"
export CXX="zig c++ -target aarch64-linux-musl"
export GOOS="linux"
export GOARCH="arm64"
export CGO_ENABLED="1"
go build -v -ldflags='-s -w -extldflags "-static"' -trimpath -o ./tools/builder/assets/tezsign ./app/gadget
```

This folder contains the **gadget-side** components that run on a Raspberry Pi 4
(DietPi distro) to expose a USB gadget over the USB-C port using **FunctionFS**.

## Contents
- `server.go` — Go program that:
  1. Writes USB descriptors + strings into FunctionFS (`/dev/ffs/myfunc/ep0`)
  2. Waits for endpoints to appear
  3. Binds the UDC (`fe980000.usb`)
  4. Runs a bulk **ping/pong** service

- `scripts/`
  - `setup_ffs_gadget.sh` — prepares configfs, creates gadget, mounts FunctionFS
  - `teardown_ffs_gadget.sh` — unbinds and removes the gadget

- `services/`
  - `ffs-prepare.service` — systemd unit to run setup/teardown scripts
  - `ffs-server.service` — systemd unit to run the Go server after prepare

## Raspberry Pi prerequisites

### 1. Enable USB device mode
Edit `/boot/config.txt`:
```ini
dtoverlay=dwc2,dr_mode=peripheral
```
Reboot the Pi:
```bash
sudo reboot
```
### 2. Kernel modules
These will be loaded automatically by the scripts:
* libcomposite
* usb_f_fs

### 3. Data-capable cable
Use a proper USB-C **data** cable on the Pi’s USB-C port (the same port also powers the Pi).

## Build
On the Pi (arm64 / aarch64):
```bash
GOARCH=arm64 go build -o server server.go
```
On the Pi (32-bit armhf):
```bash
GOARCH=arm GOARM=7 go build -o server server.go
```
Cross-compile from a PC (32-bit target):
```bash
GOOS=linux GOARCH=arm GOARM=7 go build -o server server.go
```
Install:
```bash
sudo install -m 0755 server /usr/local/sbin/server
sudo install -m 0755 scripts/*.sh /usr/local/sbin/
sudo install -m 0644 services/*.service /etc/systemd/system/
```
## Enable service
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now ffs-prepare.service ffs-server.service
```
Check status:
```bash
systemctl status ffs-server.service
```
## Verify gadget state
```bash
ls -l /dev/ffs/myfunc         # should show ep0 and bulk endpoints
cat /sys/kernel/config/usb_gadget/g1/UDC
cat /sys/class/udc/fe980000.usb/state
dmesg | grep -E 'f_fs|dwc2|usb'
```
When configured, the Pi will enumerate on the PC as VID=0x9997 PID=0x0001.

## Teardown
```bash
sudo systemctl stop ffs-server.service ffs-prepare.service
```

or manually:
```bash
sudo /usr/local/sbin/teardown_ffs_gadget.sh
```