# owgctl

Lokaler, cloud-freier Zugriff auf den **Otto Wilde G32 Connected**. Ein Werkzeug für zwei Zwecke:

1. **Sichern**, solange die Hersteller-Cloud noch läuft (eigenes Konto): Firmware und
   Session-Historie herunterladen.
2. **Lokal betreiben**: den Grill per Bluetooth auslesen und nach Home Assistant (MQTT)
   veröffentlichen — ohne App, ohne Cloud, ohne Konto.

Grundlage ist die dekompilierte App und Firmware (Protokoll siehe `../PROTOCOL.md`). Es wird ausschließlich Tobis eigenes Gerät
angesprochen; das Tool umgeht keine Schutzmaßnahmen.

## Einrichtung

Zugangsdaten (nur für die Cloud-Befehle) in `../.env` (Rechte 600, in `.gitignore`):

```
OW_USER=deine-email
OW_PASS=dein-passwort
OW_SERIAL=12345678
```

Bauen:

```
go build -o owgctl .
```

## Befehle

### Cloud (Konto nötig, solange der Server lebt)

| Befehl | Zweck |
|---|---|
| `owgctl login` | Anmeldung testen |
| `owgctl grills` | eigene Grills auflisten (Seriennummer, Firmware) |
| `owgctl firmware [serial]` | Firmware-Metadaten anzeigen |
| `owgctl download [serial]` | Firmware-`.bin` sichern → `firmware/` |
| `owgctl sessions [serial]` | Session-Historie; `--dump DIR` sichert je Session eine JSON |
| `owgctl export [--out DIR]` | **alles auf einmal sichern**: Grills + Firmware + alle Sessions |

`export` ist der schnellste Weg, vor einer Cloud-Abschaltung alles zu retten: es schreibt
`grills.json`, lädt je Grill die Firmware und legt jede Session als JSON ab. GET-Anfragen werden
bei Netzfehlern/429/5xx bis zu dreimal mit Backoff wiederholt.

### Offline (kein Grill, kein Konto)

| Befehl | Zweck |
|---|---|
| `owgctl decode <hex>` | ein Live-Frame zu JSON dekodieren |
| `owgctl replay <datei\|->` | gespeicherte Hex-Frames einspeisen (Test des ganzen Pfads) |

### Lokal per Bluetooth

| Befehl | Zweck |
|---|---|
| `owgctl ble scan` | nach Grills in Reichweite suchen |
| `owgctl ble read [namefilter]` | Grill auslesen, Live-Daten als JSON-Zeilen |
| `owgctl ble read --mqtt tcp://host:1883` | stattdessen nach Home Assistant veröffentlichen |
| `owgctl ble set mode <0-3>` | Grillmodus setzen (direkt/indirekt 1-3) |
| `owgctl ble set hoodlight <0-100>` | Haubenlicht-Schwelle in % |
| `owgctl ble set warning <on\|off>` | Übertemperatur-Warnung schalten |

`ble read` reconnectet automatisch, wenn die Verbindung abreißt. `ble set` schreibt nur
dokumentierte, ungefährliche Einstellungen (write-with-response, 1 Byte) und verweigert die Arbeit,
wenn der Grill nach Legacy-Firmware aussieht (die Setter sind nur für 9elements belegt). Firmware
und OTA schreibt das Tool bewusst **nicht**.

`ble read` erkennt die Firmware-Generation am Bluetooth-Namen und wählt automatisch den Legacy-
(28-Byte-Frame) oder den 9elements-Pfad (einzelne Characteristics).

## Home Assistant

Mit `--mqtt` meldet `owgctl` den Grill über MQTT-Discovery selbst an: Temperaturen, Gas, Haube,
Licht und Warnungen erscheinen als Geräte-Entitäten. Ein gemeinsames State-Topic
`owgctl/<serial>/state` trägt alle Werte; die Discovery-Configs liegen unter
`homeassistant/<component>/owg_<serial>/<key>/config`.

## Alarme in Home Assistant

`homeassistant/alarms.yaml` enthält fertige Automationen als Ersatz für die Push-Alarme der
Hersteller-App: Kerntemperatur erreicht, Gas fast leer, Haube zu lange offen, Übertemperatur.
Seriennummer und Ziel-Kerntemperatur darin anpassen. Gültiges YAML (geprüft).

## Ohne Grill testen (Broker-Pfad prüfen)

`replay` speist Beispiel-Frames durch denselben Code wie der Live-Betrieb. Gegen einen lokalen
Broker:

```
# Terminal 1: Broker
mosquitto

# Terminal 2: Frames abspielen, an HA/Broker senden
owgctl replay testdata/frames-legacy.hex --mqtt tcp://localhost:1883 --delay 1000 --loop

# Terminal 3: mitlesen
mosquitto_sub -t 'owgctl/#' -t 'homeassistant/#' -v
```

Ohne `--mqtt` gibt `replay` JSON-Zeilen auf stdout aus — praktisch, um den Decoder zu prüfen.

## WLAN-Pfad erkunden (fortgeschritten)

Der Grill spricht im WLAN nur mit der Cloud (`socket.ottowildeapp.com:4502`, Klartext-TCP). Für
einen cloud-freien WLAN-Betrieb müsste man diesen Socket lokal nachbauen. Der erste Schritt ist,
zu erfassen, was der Grill sendet:

```
owgctl serve-socket --port 4502 --raw grill-wifi.log
```

Dann im Router/Loxone `socket.ottowildeapp.com` auf die IP dieses Rechners umleiten. Der Server gibt
sich als Cloud aus und protokolliert jede Nachricht des Grills (JSON hübsch, Legacy-Frames dekodiert,
TLS wird erkannt). Aus dem Mitschnitt lässt sich die Grill→Server-Richtung reversen.

Mit `--mqtt tcp://host:1883` veröffentlicht `serve-socket` erkannte Frames zusätzlich nach Home
Assistant — derselbe Weg wie `ble read`. Damit liefert der WLAN-Pfad HA-Daten, sobald das Framing
des Grills bestätigt ist. Was der Grill sendet, steht in `../PROTOCOL.md`.

Hinweis: Das ist Erkundung für das **eigene** Gerät im eigenen Netz. Für den normalen lokalen
Betrieb ist Bluetooth (`ble read`) der einfachere und empfohlene Weg — WLAN braucht man nur, wenn
der Grill bewusst weiter WLAN nutzen soll.

## Dauerbetrieb (Raspberry Pi / HA-Host)

Für linux/arm64 (Pi 3/4/5, 64-bit) cross-bauen. BLE braucht CGO nur auf macOS; auf Linux nutzt
`tinygo.org/x/bluetooth` D-Bus (BlueZ) ohne CGO:

```
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o owgctl-arm64 .
```

Als Dienst, der dauerhaft liest und nach Home Assistant meldet (`/etc/systemd/system/owgctl.service`):

```ini
[Unit]
Description=owgctl – Otto Wilde G32 nach MQTT
After=network-online.target bluetooth.target

[Service]
ExecStart=/opt/owgctl/owgctl ble read --mqtt tcp://127.0.0.1:1883
Restart=always
RestartSec=10
# --loop nicht nötig; ble read bleibt verbunden. Reconnect via Restart.

[Install]
WantedBy=multi-user.target
```

Hinweis: BLE-Reichweite an der Südterrasse prüfen. Falls der Pi zu weit weg ist, ist ein
ESP32-Bluetooth-Proxy (ESPHome) die Alternative — siehe dem Repo-README.

## Stand

Der Decoder ist per Unit-Test belegt (`go test`). BLE- und MQTT-Pfad kompilieren und sind logisch
vollständig, aber noch nicht gegen echten Grill bzw. Broker verifiziert. Details und offene Punkte:
dem Repo-README.
