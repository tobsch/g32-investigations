# Otto Wilde G32 Connected — Local Protocol Reference

Cloud-free protocol for the Otto Wilde G32 Connected gas grill, for **interoperability with your
own device**. Reverse-engineered from the official app (Flutter, decompiled) and the ESP32 firmware
(Ghidra/Xtensa), and cross-checked against independent community work. No vendor code or credentials
are reproduced here — this is protocol data only.

Contributions welcome. If you find differences on your firmware, please open an issue with the raw
BLE frames / GATT dump.

## Firmware generations

The grill exists in two BLE flavours; the app picks by **BLE advertising name**:

| Generation | Detection | Data path |
|---|---|---|
| **Legacy** | name does *not* match `OWG-G32C-[A-F0-9]{8}` | one 28-byte notification frame |
| **9elements** | name matches `OWG-G32C-[A-F0-9]{8}` | individual GATT characteristics |

Notes:
- Community ESPHome tools target the 9elements path and are tested with firmware **1.4.5**; the
  factory firmware (version code 13) is reported as **not** compatible with those tools.
- Reading needs no pairing/bonding and no prior write in the app's code path (verify on-device).

---

## 9elements — GATT characteristics

Access: **N** = subscribe/notify, **R** = read, **W** = write-with-response.

### Temperature — service `181A`
| Characteristic | UUID | Access | Format |
|---|---|---|---|
| temperatureMeasurement | `2a1c` | N | N×3 bytes: `[type][uint16 BE °C]`; `type>0`=zone, `type==0`=probe; index = block# & 3 |
| highTemperatureWarningActive | `1f168db3-72ba-4806-84cc-e1c310898566` | N | bool (byte>0); firmware ≥1.2.0 |
| highTemperatureWarningEnabled | `1f168db4-…` | R/W | bool; firmware ≥1.4.0 |
| toggleCookingSessionRecording | `1f168db0-…` | R/W | 1 byte (start/stop recording) |
| fetchSensorData / …Count | `1f168db1-…` / `1f168db2-…` | R/W / R | chunked session-data readout |

### Binary sensors — service `183B`
| Characteristic | UUID | Access | Format |
|---|---|---|---|
| lightStatus | `e1255ec0-6199-44ab-b78e-08ec262cea4b` | N | bool |
| hoodStatus | `e1255ec1-…` | N | bool (hood open) |
| hoodlightThresholdPercentage | `e1255ec2-…` | R/W | uint8 %; firmware ≥1.1.0 |

### Gas scale (GasBuddy) — service `181D`
| Characteristic | UUID | Access | Format |
|---|---|---|---|
| weightMeasurement | `2a9d` | N | uint16 BE, grams (raw weight) |
| gasbuddyPercent | `64ebc8a5-e9a4-11ed-a05b-0242ac120003` | N | uint8 % |
| gasbuddyConnected | `64ebc8a9-…` | N | bool; firmware ≥1.2.0 |
| gasbuddyBottleWeight (empty-tank weight) | `64ebc8a3-…` | R/W | uint16 BE, grams |
| gasbuddyFullWeight (capacity) | `64ebc8a4-…` | R/W | uint16 BE, grams |
| gasbuddyCriticalPercent | `64ebc8a6-…` | R/W | uint8 % |
| gasbuddyTare | `64ebc8a7-…` | R | app reads the current raw value and uses it as the tare reference |

### Device info — service `180A`
| Characteristic | UUID | Access | Format |
|---|---|---|---|
| serialNumber | `2a25` | R | UTF-8 |
| firmwareSemanticVersion | `2a26` | R | UTF-8 (e.g. `1.4.5`) |
| signalStrength | `5f8b9bdc-86f7-4c68-9dce-3d1d4cb21b95` | N | int8, dBm |
| createErrorLog / errorChunksAvailable / errorCodesReadChunk | `5f8b9bd0` / `5f8b9bda` / `5f8b9bdb` | — | MCU error-log readout |

### Device management — service `830e02f4-843b-49c3-a4fa-613587247e6c`
| Characteristic | UUID | Access | Format |
|---|---|---|---|
| grillMode | `830e02f7-…` | W | uint8: `0`=direct, `1..3`=indirect |
| resetWifi | `830e02f5-…` | W | 1 byte `0x02` (triggers WiFi reprovisioning) |
| pop | `830e02f6-…` | R | UTF-8 onboarding/proof-of-possession key |

### OTA — service `64cd0d4e-2f66-4e6f-b1af-fac03a604018`
`otaControl` `64cd0d41-…` (1-byte control codes) and `otaData` `64cd0d42-…` (chunked image).
MTU 247, 244-byte chunks. Control codes: 1=requestBle start, 3/4=ack/nak, 5=done, 6/7=doneAck/Nak.
**No checksum/signature check on the app side.** Do not flash unverified images without a recovery path.

---

## Legacy — 28-byte notification frame

- Service `dc0f41ea-b6ae-46a8-a19e-1a3bf4342bcb`, notify on **`dc0f41e2-…`** (TX).
  Write path (rare, gas-weight only) on `dc0f41e1-…` (RX).
- `dc0f41e3-…` in advertising marks a device the app treats as *not* 9elements (scan filter).

Frame: header `A3 3A`, trailer `C3`. **Payload = `frame[2 : len-1]`** (offsets below are into the payload).
Known total lengths: 28 / 34 / 48 / 49 / 50 bytes (older→newer firmware; longer frames add fields).

| Payload offset | Field | Encoding |
|---|---|---|
| 0–3 | serial (partial) | 4 bytes, hex string |
| 4–19 | 8 temperatures | 2 bytes each: **`byte0*10 + byte1/10` °C**; `1500.0` = probe not plugged in |
| | | first 4 = zones, last 4 = probes |
| 20–21 | gas stock | uint16 Big-Endian, grams |
| 22 | hood open | `== 1` |
| 23 | light on | `== 1` |
| 24 | firmware version code | uint8 |
| 25–49 | (longer frames) | semver, signal, gas %, auto-update, light threshold, gasbuddy-connected, grill mode, connection mode, temp-warning flags |

No checksum in the frame; validation is header/trailer + exact length.

---

## Writing / control (calibration & settings)

All write-with-response. Weight is encoded as `[grams >> 8, grams & 0xFF]` (uint16 BE, 0…65535 g).

| Action | Characteristic | Payload |
|---|---|---|
| Grill mode | `830e02f7` | 1 byte, 0=direct / 1–3=indirect |
| Hood-light threshold | `e1255ec2` | uint8 % |
| High-temp warning on/off | `1f168db4` | 1 byte 0/1 |
| GasBuddy empty-tank weight | `64ebc8a3` | uint16 BE grams |
| GasBuddy capacity | `64ebc8a4` | uint16 BE grams |
| GasBuddy critical percent | `64ebc8a6` | uint8 % |
| GasBuddy tare | `64ebc8a7` | read current raw value → use as tare reference |

The gas **percentage** is computed on the grill from these calibration values (stored in NVS as
`WEIGHT`, `TAREWGT`, `FULLWGT`, `GASBDNWGT`, `CRIT_PRCT`); for a local client you only need the four
characteristics above to configure it — no cloud login required.

---

## GasBuddy (gas scale) hardware

The GasBuddy is an **external UART peripheral**, not a directly wired load cell:
- 4-pin connector: **GND, +5V, RX** (and TX). Confirmed on-device by the community (thanks @sagdusmir).
- It contains an **STM32F030** with its own ADCs + regulator; it measures the load cell and reports a
  finished weight.
- The grill **polls it over UART ~1×/s** with a fixed 4-byte command and reads a 16-bit weight from
  the response (bytes 2–3, big-endian).

---

## WiFi / cloud socket (reference only — not local)

In WiFi mode the grill talks **only to the vendor cloud** (`socket.ottowildeapp.com:4502`, plaintext
TCP), not to your LAN. A local WiFi setup therefore requires replacing that socket server.

- The grill registers with `{"popKey":"…","serialNumber":"…","firmwareSemanticVersion":"…"}`
  (or the query-string form). `popKey` is a 32-byte value hex-encoded to 64 chars; `serialNumber`
  derives from the device MAC (`MAC_UID`).
- Live data comes back as the same binary frame family as BLE (`GrillSocketDataframe`, 28…50 bytes).

For local use, **BLE is the simpler path** and needs none of the above.

---

## Method & credits

- App: Flutter AOT snapshot decompiled with `blutter`.
- Firmware: ESP32 image analysed with Ghidra + an Xtensa SLEIGH module.
- Independently corroborated by community projects (BLE decode: sagdusmir, JBecker; socket/HA: zaubii).

This document describes protocol facts for interoperating with your own grill. It is not affiliated
with or endorsed by Otto Wilde / Miele.
