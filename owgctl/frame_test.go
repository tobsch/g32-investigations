package main

import (
	"encoding/hex"
	"testing"
)

// Beispiel-Frame aus dem Simulator der App (notizen/2026-09-27_ble-lesen-detail.md,
// i=100, Seriennummer-Bytes als 00 angenommen). Zone 1 = 0x12*10 + 0x10/10 = 181,6 °C.
const sampleLegacyHex = "a33a0000000012100e0c0a080604021412100e0c0a08000001010cc3"

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	clean := make([]byte, 0, len(s))
	for _, r := range s {
		if r != ' ' {
			clean = append(clean, byte(r))
		}
	}
	b, err := hex.DecodeString(string(clean))
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	return b
}

func TestDecodeLegacyFrameSample(t *testing.T) {
	raw := mustHex(t, sampleLegacyHex)
	if len(raw) != 28 {
		t.Fatalf("Beispiel-Frame hat %d Byte, erwartet 28", len(raw))
	}
	d, err := DecodeLegacyFrame(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.Zones[0] == nil {
		t.Fatal("Zone 1 ist nil, erwartet 181.6")
	}
	if got := *d.Zones[0]; got != 181.6 {
		t.Errorf("Zone 1 = %.1f, erwartet 181.6", got)
	}
	if !d.HoodOpen {
		t.Error("HoodOpen sollte true sein (Byte 22 = 01)")
	}
	if !d.LightOn {
		t.Error("LightOn sollte true sein (Byte 23 = 01)")
	}
	if d.FwCode != 12 {
		t.Errorf("FwCode = %d, erwartet 12", d.FwCode)
	}
}

func TestDecodeLegacyRejectsBadFrame(t *testing.T) {
	// falsche Länge
	if _, err := DecodeLegacyFrame([]byte{0xA3, 0x3A, 0xC3}); err == nil {
		t.Error("kurzer Frame sollte Fehler ergeben")
	}
	// falscher Header
	bad := make([]byte, 28)
	bad[27] = frameTail
	if _, err := DecodeLegacyFrame(bad); err == nil {
		t.Error("Frame ohne A3 3A sollte Fehler ergeben")
	}
}

func TestUnpluggedProbeIsNil(t *testing.T) {
	raw := make([]byte, 28)
	raw[0], raw[1], raw[27] = frameHead0, frameHead1, frameTail
	// Nutzlast beginnt bei raw[2]; Probe 0 liegt bei Nutzlast-Offset 12 → raw[14..15].
	// 1500.0 = b0=150 (0x96), b1=0 → nicht gesteckt.
	raw[2+12], raw[2+13] = 150, 0
	d, err := DecodeLegacyFrame(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.Probes[0] != nil {
		t.Errorf("Probe 0 sollte nil sein (1500 = nicht gesteckt), ist %v", *d.Probes[0])
	}
}

func TestDecode9ElementsTemps(t *testing.T) {
	// 2 Blöcke: Zone (typ=1) 220 °C, Probe (typ=0) 63 °C.
	v := []byte{0x01, 0x00, 0xDC, 0x00, 0x00, 0x3F}
	got, err := Decode9ElementsTemps(v)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("erwartet 2 Sensoren, bekam %d", len(got))
	}
	if got[0].Type != "zone" || got[0].TempC != 220 {
		t.Errorf("Block 0 = %+v, erwartet zone/220", got[0])
	}
	if got[1].Type != "probe" || got[1].TempC != 63 {
		t.Errorf("Block 1 = %+v, erwartet probe/63", got[1])
	}
}

func TestDecode9ElementsIndexWraps(t *testing.T) {
	// 8 Zonen-Blöcke: index soll bei 4 wieder von 0 zählen (n & 3).
	v := make([]byte, 8*3)
	for n := 0; n < 8; n++ {
		v[3*n] = 1 // Typ zone
	}
	got, err := Decode9ElementsTemps(v)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []int{0, 1, 2, 3, 0, 1, 2, 3}
	for i, w := range want {
		if got[i].Index != w {
			t.Errorf("Block %d Index = %d, erwartet %d", i, got[i].Index, w)
		}
	}
}

func TestDecode9ElementsRejectsBadLength(t *testing.T) {
	if _, err := Decode9ElementsTemps([]byte{0x01, 0x00}); err == nil {
		t.Error("Länge 2 (kein Vielfaches von 3) sollte Fehler ergeben")
	}
}

func TestGrillDataFlat(t *testing.T) {
	raw := mustHex(t, sampleLegacyHex)
	d, err := DecodeLegacyFrame(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	flat := d.Flat()
	if flat["zone1"] != 181.6 {
		t.Errorf("flat[zone1] = %v, erwartet 181.6", flat["zone1"])
	}
	if flat["hood_open"] != true {
		t.Errorf("flat[hood_open] = %v, erwartet true", flat["hood_open"])
	}
	if _, ok := flat["gas_stock_g"]; !ok {
		t.Error("gas_stock_g fehlt in Flat()")
	}
}

func TestParseHexFrameIgnoresSeparators(t *testing.T) {
	a, err := parseHexFrame("a3:3a-c3")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b, _ := parseHexFrame("0xA33AC3")
	if len(a) != 3 || a[0] != 0xA3 || a[2] != 0xC3 {
		t.Errorf("Trenner nicht ignoriert: %x", a)
	}
	if string(a) != string(b) {
		t.Errorf("Groß/Klein bzw. 0x nicht gleich behandelt: %x vs %x", a, b)
	}
}
