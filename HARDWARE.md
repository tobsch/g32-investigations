# G32 Connected — Controller hardware map

For the **open-firmware frontier** (running ESPHome on the grill's own ESP32 instead of the vendor
app). This is the GPIO / peripheral map of the controller board, needed to write a firmware
replacement.

**Source & status:** derived from **static analysis of the vendor firmware on my own unit** (ESP32
image, Ghidra/Xtensa) and cross-checked against the one public board photo. It is **not yet verified
against a live board** — pin continuity and the exact zone-converter part have not been measured.
Corrections and measurements very welcome (open an issue). No vendor code or secrets are reproduced
here — this is a hardware-interoperability description only.

## SoC

**Ai-Thinker `ESP32-S`** module — a **classic ESP32** (not S3), external antenna via IPEX. Firmware
built with ESP-IDF v5.0.x, NimBLE, protocomm provisioning. See [`README.md`](./README.md).

## GPIO map

| GPIO | Function |
|---|---|
| 18 | Temperature SPI **CLK** (bit-banged) |
| 23 | Temperature SPI **MOSI** |
| 19 | Temperature SPI **MISO** |
| 17 | **CS** zone converter A (channels 0 & 1) |
| 4  | **CS** zone converter B (channels 2 & 3) |
| 22 | **CS** core probe 4 |
| 32 | **CS** core probe 5 |
| 5  | **CS** core probe 6 |
| 16 | **CS** core probe 7 |
| 2  | GasBuddy **UART1 TX** |
| 15 | GasBuddy **UART1 RX** |
| 27 | Gas-level indicator LED |
| 14 | Gas-level indicator LED |
| 34 | Hood sensor (input) |
| 21 | Hood light (LEDC PWM output) |
| 13, 33 | Front-panel buttons (input, interrupt) |
| 26, 25 | Status LEDs (WiFi/BT, output) |
| 35, 39 | Hardware-variant straps (input, read once at boot) |

## Temperature front-end

Eight channels over **one bit-banged SPI bus** (CLK 18 / MOSI 23 / MISO 19), **two different
converter types**:

- **Core probes (channels 4–7): four `MAX6675` chips**, one per probe, each on its own CS
  (GPIO 22/32/5/16). Confirmed by the read format: 16-bit read, **bit 2 = open-thermocouple flag**,
  **temperature = value >> 3** (0.25 °C/count) — the exact MAX6675 layout. A value of **1500** marks
  "probe not plugged in".
  → In ESPHome: the stock **`max6675`** platform, four instances.
- **Grill zones (channels 0–3): two register-configured 2-channel converters** (CS GPIO 17 and 4).
  Each is set up with a 3-register init, then read as three 16-bit words per channel after a ~50 ms
  conversion, with a busy-flag poll. The MCU does **Type-K linearization + cold-junction compensation
  in software** (so the converter supplies a raw value, not °C). The **exact part is still open**
  (needs the chip marking from an open board).
  → In ESPHome: a small **custom component** that reproduces the same bit-bang sequence — the full
  sequence (clock/command/read framing) is known, so the part number isn't strictly required.

## GasBuddy (gas scale) — UART1

External 4-pin peripheral (`GND, +5V, RX, TX`) with its own STM32F030; the grill polls it:

- **UART1, TX = GPIO2, RX = GPIO15, 9600 baud, 8N1**, no RTS/CTS.
- Poll ~1/s: send the fixed 4-byte command **`AA 14 C3 3C`**, read the reply, **weight = reply
  bytes [2:3], big-endian (grams)**.
- Calibration (empty/full/critical/tare) is set over BLE; see [`PROTOCOL.md`](./PROTOCOL.md).

## Hood

- **Hood sensor: GPIO34** (input; reed/hall — open/close).
- **Hood light: GPIO21** (LEDC PWM); switched against ambient brightness vs. the
  `hoodlightThresholdPercentage` setting.

## Front panel — no graphical display

The front panel is **status LEDs + buttons only** — there is **no graphical/LCD display** on this
board. A `comhub` task runs a connection-state machine (disconnected / pairing / connecting /
connected) and drives the status LEDs; a button task reads the buttons (short/long press). What looked
at first like a display-driver init turned out to be the WiFi / `esp_netif` / `esp_event` plumbing
(it registers WiFi/IP event handlers that feed the LED state machine).

- **Buttons: GPIO13, GPIO33** (interrupt, debounced, short/long press).
- **Status LEDs: GPIO25, GPIO26** (WiFi/BT, blink patterns for connection state).

So an ESPHome replacement doesn't need to reproduce a screen: buttons → `binary_sensor`, status LEDs
→ `output`/`light`, connection logic → Home Assistant automations.

## Still open (needs a physical board)

- **eFuse / Secure Boot / Flash Encryption status** — the go/no-go for flashing custom firmware.
  Read with `esptool` over serial (`SECURE_BOOT_EN`, `FLASH_CRYPT_CNT`). The vendor OTA image is
  **unsigned**, which strongly suggests Secure Boot is **off**, but only the eFuses are definitive.
- Exact **zone-converter part number** (chip marking) and continuity confirmation of the pin map.
- A clean macro photo / continuity map of the unpopulated 6-pin header near `R39` (likely the
  factory serial/programming header: `3V3 · EN · IO0 · RXD0 · TXD0 · GND`).

*This would all go much faster with a spare/donor controller board to probe — see the project
discussion.*
