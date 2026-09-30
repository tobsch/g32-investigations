package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// parseHexFrame liest einen Hex-String und ignoriert übliche Trenner
// (Leerzeichen, Doppelpunkt, Bindestrich, 0x-Präfix).
func parseHexFrame(s string) ([]byte, error) {
	s = strings.NewReplacer(" ", "", ":", "", "-", "", "0x", "", "0X", "", "\t", "").Replace(s)
	return hex.DecodeString(s)
}

// Dieses Modul dekodiert die BLE-/Socket-Live-Frames des Otto Wilde G32
// Connected. Die Byte-Belegung ist aus der App dekompiliert (siehe
// notizen/2026-09-27_ble-lesen-detail.md). Es spricht NICHT mit dem Grill —
// es parst nur Bytes und ist damit ohne Gerät testbar.

const (
	frameHead0 = 0xA3
	frameHead1 = 0x3A
	frameTail  = 0xC3

	// probeUnplugged ist der Rohwert, den die App als "Fühler nicht gesteckt"
	// interpretiert (1500.0 °C).
	probeUnplugged = 1500.0
)

// frameLengths sind die bekannten Gesamtlängen des rohen Frames je Firmware.
var frameLengths = map[int]string{
	28: "v12 (0.0.0)",
	34: "v1_0_0",
	48: "v1_1_0",
	49: "v1_2_0",
	50: "v1_4_0",
}

// GrillData ist der dekodierte Zustand aus einem Live-Frame. Nicht in jeder
// Firmware-Variante sind alle Felder belegt; nicht gesetzte Felder bleiben nil.
type GrillData struct {
	Serial   string     `json:"serial"`
	Zones    []*float64 `json:"zones"`  // 4 Zonen, nil = nicht gesteckt/aus
	Probes   []*float64 `json:"probes"` // 4 Kernfühler, nil = nicht gesteckt
	GasStock int        `json:"gasStockGrams"`
	HoodOpen bool       `json:"hoodOpen"`
	LightOn  bool       `json:"lightOn"`
	FwCode   int        `json:"firmwareVersionCode"`

	// Ab Firmware 1.0.0 (längere Frames):
	SemanticVersion string   `json:"semanticVersion,omitempty"`
	SignalStrength  *int     `json:"signalStrength,omitempty"` // dBm (int8)
	GasPercent      *int     `json:"gasPercent,omitempty"`
	AutoUpdate      *bool    `json:"autoUpdate,omitempty"`
	LightThreshold  *int     `json:"lightThresholdPercent,omitempty"` // ab 1.1.0
	GasBuddyOn      *bool    `json:"gasBuddyConnected,omitempty"`     // ab 1.1.0
	GrillMode       *int     `json:"grillMode,omitempty"`             // ab 1.1.0
	ConnectionMode  *int     `json:"connectionMode,omitempty"`        // ab 1.1.0
	TempWarnActive  *bool    `json:"tempWarningActive,omitempty"`     // ab 1.2.0
	TempWarnEnabled *bool    `json:"tempWarningEnabled,omitempty"`    // ab 1.4.0

	Variant string `json:"variant"` // erkannte Frame-Variante
}

// DecodeLegacyFrame parst einen rohen Legacy-Frame (A3 3A ... C3). Die
// Offset-Angaben beziehen sich auf die Nutzlast p = frame[2 : len-1].
func DecodeLegacyFrame(raw []byte) (*GrillData, error) {
	variant, ok := frameLengths[len(raw)]
	if !ok {
		return nil, fmt.Errorf("unbekannte Frame-Länge %d (erwartet 28/34/48/49/50)", len(raw))
	}
	if raw[0] != frameHead0 || raw[1] != frameHead1 {
		return nil, fmt.Errorf("Header falsch: %#x %#x (erwartet A3 3A)", raw[0], raw[1])
	}
	if raw[len(raw)-1] != frameTail {
		return nil, fmt.Errorf("Endbyte falsch: %#x (erwartet C3)", raw[len(raw)-1])
	}
	p := raw[2 : len(raw)-1]

	d := &GrillData{Variant: variant}
	d.Serial = fmt.Sprintf("%02x%02x%02x%02x", p[0], p[1], p[2], p[3])

	temps := make([]*float64, 8)
	for i := 0; i < 8; i++ {
		t := legacyTemp(p[4+2*i], p[5+2*i])
		if t != probeUnplugged {
			v := t
			temps[i] = &v
		}
	}
	d.Zones = temps[:4]
	d.Probes = temps[4:]

	d.GasStock = int(binary.BigEndian.Uint16(p[20:22]))
	d.HoodOpen = p[22] == 1
	d.LightOn = p[23] == 1
	d.FwCode = int(p[24])

	// Längere Varianten: Zusatzfelder ab Nutzlast-Offset 25.
	if len(p) >= 29 { // ab 1.0.0
		d.SemanticVersion = fmt.Sprintf("%d.%d.%d", p[25], p[26], p[27])
		ss := int(int8(p[28]))
		d.SignalStrength = &ss
	}
	if len(p) >= 31 {
		gp := int(p[29])
		d.GasPercent = &gp
		au := p[30] != 0
		d.AutoUpdate = &au
	}
	if len(p) >= 35 { // ab 1.1.0
		lt := int(p[31])
		d.LightThreshold = &lt
		gb := p[32] != 0
		d.GasBuddyOn = &gb
		gm := int(p[33])
		d.GrillMode = &gm
		cm := int(p[34])
		d.ConnectionMode = &cm
	}
	if len(p) >= 46 { // ab 1.2.0
		tw := p[45] != 0
		d.TempWarnActive = &tw
	}
	if len(p) >= 47 { // ab 1.4.0
		te := p[46] != 0
		d.TempWarnEnabled = &te
	}
	return d, nil
}

// legacyTemp rechnet zwei Bytes in °C um: b0 zählt Zehner, b1 Zehntel.
// Beleg: GrillBinaryTranslator.grillDataFromBinary (App-Dekompilat).
func legacyTemp(b0, b1 byte) float64 {
	return float64(b0)*10 + float64(b1)/10
}

// SensorReading ist ein Sensor aus dem 9elements-Temperatur-Characteristic.
type SensorReading struct {
	Type  string `json:"type"`  // "zone" oder "probe"
	Index int    `json:"index"` // 0..3 je Typ
	TempC int    `json:"tempC"`
}

// Decode9ElementsTemps parst den Wert des temperatureMeasurement-Characteristics
// (2a1c) der 9elements-Firmware: 3-Byte-Blöcke [typ][uint16 BE °C].
// typ > 0 = Zone, typ == 0 = Probe. index = Blocknummer & 3.
func Decode9ElementsTemps(v []byte) ([]SensorReading, error) {
	if len(v)%3 != 0 {
		return nil, fmt.Errorf("Länge %d ist kein Vielfaches von 3", len(v))
	}
	out := make([]SensorReading, 0, len(v)/3)
	for n := 0; n < len(v)/3; n++ {
		block := v[3*n : 3*n+3]
		typ := "probe"
		if block[0] > 0 {
			typ = "zone"
		}
		out = append(out, SensorReading{
			Type:  typ,
			Index: n & 3,
			TempC: int(binary.BigEndian.Uint16(block[1:3])),
		})
	}
	return out, nil
}
