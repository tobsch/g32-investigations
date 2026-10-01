# ESPHome firmware for the G32 controller (draft)

A **starting skeleton** for running [ESPHome](https://esphome.io) on the grill's own ESP32, so the
G32 works entirely in Home Assistant with no vendor cloud. Pins and peripherals come from the
firmware reverse-engineering in [`../HARDWARE.md`](../HARDWARE.md).

## ⚠️ Read before flashing

This is **untested** and flashing is **one-way without a backup**. Do **not** flash until you have,
over a serial connection (`esptool`):

1. **Confirmed the chip is unlocked** — `SECURE_BOOT_EN` and `FLASH_CRYPT_CNT` / `SPI_BOOT_CRYPT_CNT`
   all `0`. (The vendor OTA image is unsigned, which strongly suggests Secure Boot is off — but only
   the eFuses are definitive.)
2. **Taken a full flash dump** (`esptool read_flash 0 <size> backup.bin`) — bootloader, partition
   table, app **and NVS** (NVS holds the GasBuddy calibration, serial, WiFi and pop key). This is your
   only way back to stock.

Serial access: the ESP32 UART0 (`TXD0`/`RXD0`/`GND`, plus `IO0` low + `EN` for download mode) — the
unpopulated 6-pin header near `R39` is the likely factory programming header (confirm pinout by
continuity). Use a **3.3 V** adapter.

## What works in this draft

| Part | Status |
|---|---|
| Core probes (4× MAX6675) | stock `max6675` component, real CS pins |
| GasBuddy weight (UART1, poll `AA 14 C3 3C`) | polled template sensor |
| Gas LEDs, hood sensor/light, buttons, status LEDs | stock components, real pins |
| **Grill zones (ch 0–3)** | **TODO — custom component** (non-stock converter) |

The grill zones use two register-configured 2-channel converters on the shared SPI bus; the full
bit-bang sequence is documented in `../HARDWARE.md` and must be reproduced in a small custom
component. Everything else uses stock ESPHome components.

## Use

Put your WiFi into a `secrets.yaml` next to `g32.yaml`, then build with the ESPHome CLI. Flash the
**first** image over serial (see the warning above); subsequent updates can go OTA.
