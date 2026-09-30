package main

import (
	"bytes"
	"strings"
	"testing"
)

// recordingSink merkt sich veröffentlichte Messwerte für Tests.
type recordingSink struct {
	published []map[string]any
	serials   []string
}

func (r *recordingSink) publish(serial string, flat map[string]any) error {
	r.serials = append(r.serials, serial)
	r.published = append(r.published, flat)
	return nil
}
func (r *recordingSink) close() {}

func TestInterpretGrillData_Frame(t *testing.T) {
	raw := mustHex(t, sampleLegacyHex)
	var log bytes.Buffer
	rec := &recordingSink{}

	interpretGrillData(&log, "grill", raw, rec)

	if len(rec.published) != 1 {
		t.Fatalf("erwartet 1 Veröffentlichung, bekam %d", len(rec.published))
	}
	if rec.published[0]["zone1"] != 181.6 {
		t.Errorf("zone1 = %v, erwartet 181.6", rec.published[0]["zone1"])
	}
	if !strings.Contains(log.String(), "Legacy-Frame") {
		t.Errorf("Log sollte 'Legacy-Frame' enthalten:\n%s", log.String())
	}
}

func TestInterpretGrillData_JSON(t *testing.T) {
	var log bytes.Buffer
	rec := &recordingSink{}

	interpretGrillData(&log, "grill", []byte(`{"popKey":"x","serialNumber":"12345678"}`), rec)

	if len(rec.published) != 0 {
		t.Errorf("JSON darf nichts an die Sink geben, bekam %d", len(rec.published))
	}
	if !strings.Contains(log.String(), "JSON") {
		t.Errorf("Log sollte 'JSON' enthalten:\n%s", log.String())
	}
}

func TestInterpretGrillData_TLS(t *testing.T) {
	var log bytes.Buffer
	rec := &recordingSink{}
	// TLS-ClientHello beginnt mit 0x16 0x03 ...
	interpretGrillData(&log, "grill", []byte{0x16, 0x03, 0x01, 0x00, 0x2f}, rec)

	if len(rec.published) != 0 {
		t.Error("TLS-Handshake darf nichts an die Sink geben")
	}
	if !strings.Contains(log.String(), "TLS") {
		t.Errorf("Log sollte TLS-Hinweis enthalten:\n%s", log.String())
	}
}

func TestInterpretGrillData_BadFrame(t *testing.T) {
	var log bytes.Buffer
	rec := &recordingSink{}
	// A3 3A Kopf, aber falsche Länge → kein Publish, Hinweis im Log.
	interpretGrillData(&log, "grill", []byte{frameHead0, frameHead1, 0x00, 0x01}, rec)

	if len(rec.published) != 0 {
		t.Error("kaputtes Frame darf nichts veröffentlichen")
	}
	if !strings.Contains(log.String(), "Decode fehlgeschlagen") {
		t.Errorf("Log sollte Decode-Fehler vermerken:\n%s", log.String())
	}
}

func TestInterpretGrillData_NilSinkNoPanic(t *testing.T) {
	var log bytes.Buffer
	raw := mustHex(t, sampleLegacyHex)
	// Darf mit nil-Sink nicht panicken (serve-socket ohne --mqtt).
	interpretGrillData(&log, "grill", raw, nil)
	if !strings.Contains(log.String(), "Legacy-Frame") {
		t.Errorf("Log fehlt:\n%s", log.String())
	}
}
