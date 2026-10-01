# g32-investigations — toward "Open Otto"

Making the **Otto Wilde G32 Connected** gas grill fully usable **locally, without the manufacturer
cloud**. Research notes, a reverse-engineered protocol reference, and a small tool.

## Why

Otto Wilde Grillers wound down at the end of 2025; the brand moved to Miele, and there is **no
guarantee that the app/cloud keeps running**. When the cloud goes dark, connected G32 grills lose
their smart features. This project is about making sure they don't — for your own grill, purely
locally.

## The plan (Open Otto)

Three layers, from "works today" to "fully open":

1. **Read locally** — get temperatures, core-probes, gas level, hood, light into **Home Assistant**
   over Bluetooth LE, no cloud login. The community already solved this well (see credits); this repo
   corroborates it and documents the protocol.
2. **Control locally** — set grill mode, hood-light threshold, high-temp warning, and calibrate the
   gas scale over BLE (no cloud). Documented here and supported by the `owgctl` tool.
3. **Fully open** — replace the vendor cloud entirely:
   - a **local replacement for the WiFi socket** the grill phones home to, and ultimately
   - **custom firmware (ESPHome) on the grill's own ESP32**, so no vendor code runs at all.
   This is the frontier — nobody has opened the grill yet. See `notes on hardware` below.

The point of "Open Otto" is not to compete with the existing community tools, but to **combine**
them and push the two layers that are still open (local control, and a truly cloud-free / open-firmware
grill), with a clean, shared protocol reference as the common base.

## What's in here

- **[`PROTOCOL.md`](./PROTOCOL.md)** — the reverse-engineered local protocol: BLE GATT characteristics
  (9elements) and the legacy 28-byte frame, read + write formats, gas-scale calibration, the WiFi
  socket, and how the pieces fit. Derived from the decompiled app (Flutter) and ESP32 firmware
  (Ghidra/Xtensa), cross-checked against community work.
- **[`HARDWARE.md`](./HARDWARE.md)** — the controller's GPIO / peripheral map (SPI pins + chip-selects,
  thermocouple converters, GasBuddy UART, hood, front panel), for the open-firmware / ESPHome path.
- **[`esphome/`](./esphome)** — a starter ESPHome config (`g32.yaml`) using the real pins: 4× MAX6675
  probes, GasBuddy UART, gas LEDs, hood, buttons, status LEDs. Untested draft — read its warning
  before flashing.
- **[`owgctl/`](./owgctl)** — a small Go tool:
  - **local, cloud-free:** `ble scan` / `ble read` (→ MQTT / Home Assistant), `ble set` (control),
    `decode` / `replay` (offline), `serve-socket` (experimental local cloud-socket capture/replacement).
  - **backup while the cloud lives:** `export` (your grills + firmware + session history, own account).
  - Home Assistant alarm automations in `owgctl/homeassistant/`.

## Status

- Protocol (BLE read + write, gas calibration, legacy frame, socket registration): **documented**,
  decompiled from app **and** firmware, and independently corroborated by the community.
- `owgctl`: builds, unit-tested for the decode/pipeline logic. The BLE/MQTT/serve paths compile and
  are logically complete but **not yet verified against a physical grill**.
- Firmware version matters: community BLE tools need **1.4.5** (9elements); factory version code 13
  behaves differently. Check with `owgctl ble scan` on your grill.

## Hardware (for the open-firmware frontier)

From firmware decompilation (to be confirmed by opening a grill):

- SoC: **Ai-Thinker `ESP32-S` module — a classic ESP32** (not S3). Confirmed from a community board
  photo (module marking `ESP32-S / FCC ID:2AHMR-ESP32S`), matching the firmware analysis (chip_id 0).
  Board silk: `OTTO WILDE`, `Material: KB-6160`, `Version: OTTOG32C20…`; external antenna via IPEX.
  The electronics sit in a metal-cased **control panel (OPS panel)** behind the front.
- Temperatures: **bit-banged SPI** thermocouple front-end (CLK GPIO 18, MOSI GPIO 23), 8 channels,
  register-configured chip (likely MAX31856 / ADS1118), polynomial linearization with cold-junction
  compensation.
- Gas scale (**GasBuddy**): external **UART** peripheral (4-pin GND/+5V/RX+TX) with its own
  **STM32F030** + ADCs; the grill polls it for a finished weight.
- The electronics live in the **control panel (OPS panel)**.

The full GPIO / peripheral map (SPI pins, per-channel chip-selects, GasBuddy UART, hood) is in
**[`HARDWARE.md`](./HARDWARE.md)**, reverse-engineered from the firmware. Headline: temperature is a
bit-banged SPI bus (CLK 18 / MOSI 23 / MISO 19), core probes are **4× MAX6675**, zones are two
register-configured converters; GasBuddy is **UART1 (TX 2 / RX 15, 9600)**.

The one thing that still needs a physical board: the ESP32's **Secure Boot / flash-encryption**
status (the go/no-go for flashing custom firmware) — the vendor OTA image is unsigned, which hints
Secure Boot is off, but only the eFuses are definitive. A spare/donor controller board would let this
be settled without opening a working grill.

**Serial access** (for flashing ESPHome / reading eFuses): solder to the ESP32-S pads `TXD0` (GPIO1),
`RXD0` (GPIO3), `GND`; for download mode also `IO0` (pull low at reset) and `EN`. Use a **3.3 V**
USB-UART adapter (TX↔RX crossed). First read the eFuse / Secure-Boot status and take a full
`esptool` flash dump **before** changing anything — there is no known recovery path otherwise.

## Credits

Independent community projects this builds on / cross-checks with:

- [sagdusmir/G32-Grill-Display-480x320-BTpref](https://github.com/sagdusmir/G32-Grill-Display-480x320-BTpref)
  and 320x172 variant — cloud-free BLE → ESPHome display, tested on firmware 1.4.5.
- [zaubii/owg-g32-ha-integration](https://github.com/zaubii/owg-g32-ha-integration) — Home Assistant
  integration via the (cloud) socket.
- [JBecker32/G32-Display-480x320-HACS](https://github.com/JBecker32/G32-Display-480x320-HACS).
- Discussion in the Grillsportverein "Otto Wilde G32 | Smarthome" thread.

## Disclaimer & license

For interoperability with **your own** device. Not affiliated with or endorsed by Otto Wilde / Miele.
No manufacturer code or credentials are included. Licensed under the **MIT License** (see `LICENSE`).
