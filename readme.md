# tezsign

`tezsign` is a secure, air-gapped signing solution for Tezos consensus operations. It uses a dedicated hardware gadget (like a Raspberry Pi) connected via USB to a host machine, ensuring your keys remain isolated.

## 🚀 Get Started

### What you need:

* **Hardware Gadget:** Radxa Zero 3 OR any Raspberry Pi newer than 2 (e.g., RPi Zero 2W). An RPi 5 is *not* ideal.
* **SD Card:** 4GB or larger. A high-quality, industrial-grade/endurance SD card is **highly recommended**.

---

## 🏛️ Architecture

`tezsign` consists of two parts:

* **Gadget:** The external, air-gapped device connected to the host over USB, acting as a peripheral. This is where your keys live and signing operations happen.
* **Host App:** Your companion application (the `tezsign` command-line tool) which you use to control the gadget from your host machine.

---

## ⚙️ Setup

1.  Download the **gadget image** for your specific device and the **host app**.
    - https://github.com/tez-capital/tezsign/releases
2.  Use Balena Etcher (or a tool you are familiar with) to flash the gadget image to your SD card.
3.  Plug the SD card into your board (e.g., Radxa Zero 3, RPi Zero 2W).
4.  Connect the board to your host machine.
    * **Important:** Make sure you use a good quality USB cable and connect it to the **OTG port** of your board.

After the initial connection, the device will configure itself and reboot. This process takes approximately 30 seconds.

> **NOTE:** The Radxa Zero 3 currently has an issue where it may not boot correctly after the initial configuration. If this happens, simply unplug it and plug it back in once you see the LED diode has stopped blinking (this indicates the configuration is complete).

---

## ✨ Initialization & Usage

After about 30 seconds, your device should be ready. It's time to initialize it.

Assuming your host app is available in your path as `tezsign`:

1.  **Confirm Device Connection**
    ```bash
    ./tezsign list-devices
    ```

2.  **Initialize the Device**
    This prompts you for a master password.
    ```bash
    ./tezsign init
    ```
    > **Warning:** It is not currently possible to change this password. Please choose wisely!

3.  **Generate New Keys**
    Generate the keys you need, giving them descriptive aliases.
    ```bash
    ./tezsign new consensus companion
    ```
    *(You can use any aliases you like, not just "consensus" and "companion".)*

4.  **List Keys & Check Status**
    You can list all available keys on the device and check their status.
    ```bash
    ./tezsign list
    ./tezsign status
    ```

5.  **Register Keys On-Chain**
    To register your keys on the Tezos network, you will need their public key (`BLpk`) and a proof of possession. You can get these details using:
    ```bash
    ./tezsign status --full
    ```
    Use the `BLpk` and proof of possession to register the keys as a consensus or companion key. You can use a tool like [tezgov](https://gov.tez.capital/) to do this comfortably.

6.  **Unlock Keys & Run Signer**
    After the keys are registered on-chain, you must unlock them on the device to allow them to sign operations.
    ```bash
    ./tezsign unlock consensus companion
    ```
    *(Use the same aliases you created in step 3.)*

7.  **Start the Signer Server**
    Finally, start the signer server. Your baker should be configured to point to this address and port.
    ```bash
    ./tezsign run --listen 127.0.0.1:20090
    ```
    At this point, `tezsign` is ready for baking. Make sure your baker points to it when the registered keys activate, and it will sign baking operations automatically.

---

## 🔒 Security

Right now, `tezsign` implements the following security measures:

* Minimal Armbian build (a minimal kernel is a future goal).
* A custom signer capable of signing **only** consensus operations.
* Read-only `bootfs`, `rootfs`, and `app` partitions.
* A read-write, non-executable `data` partition for application data.
* All keys and respective sensitive key data are encrypted.
* All user accounts are disabled.*

> ***Warning:** "Dev" images have a `dev` account enabled. Please do not use these images in production unless you know exactly what you are doing.

---

## 🛠️ Development

TODO