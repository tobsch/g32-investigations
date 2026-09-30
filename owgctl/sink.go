package main

import (
	"encoding/json"
	"fmt"
)

// Ein sink nimmt einen flachen Messwert-Satz entgegen. So können der lokale
// BLE-Leser und (später) andere Quellen wahlweise nach stdout oder MQTT
// ausgeben, ohne die Lese-Logik zu kennen.
type sink interface {
	publish(serial string, flat map[string]any) error
	close()
}

// makeSink liefert die Ausgabe je nach Optionen: MQTT (wenn --mqtt gesetzt)
// oder stdout. Der Aufrufer ruft close() am Ende.
func makeSink(opt options) (sink, error) {
	if opt.mqtt != "" {
		ms, err := newMQTTSink(opt.mqtt)
		if err != nil {
			return nil, err
		}
		fmt.Printf("MQTT: veröffentliche an %s (Home-Assistant-Discovery aktiv)\n", opt.mqtt)
		return ms, nil
	}
	return stdoutSink{}, nil
}

// stdoutSink gibt jeden Messwert-Satz als eine JSON-Zeile aus.
type stdoutSink struct{}

func (stdoutSink) publish(_ string, flat map[string]any) error {
	line, err := json.Marshal(flat)
	if err != nil {
		return err
	}
	fmt.Println(string(line))
	return nil
}
func (stdoutSink) close() {}

// Flat wandelt einen dekodierten Legacy-Frame in flache Schlüssel/Wert-Paare.
// Nicht gesteckte Fühler werden weggelassen.
func (d *GrillData) Flat() map[string]any {
	m := map[string]any{
		"gas_stock_g": d.GasStock,
		"hood_open":   d.HoodOpen,
		"light_on":    d.LightOn,
	}
	for i, z := range d.Zones {
		if z != nil {
			m[fmt.Sprintf("zone%d", i+1)] = *z
		}
	}
	for i, p := range d.Probes {
		if p != nil {
			m[fmt.Sprintf("probe%d", i+1)] = *p
		}
	}
	if d.GasPercent != nil {
		m["gas_percent"] = *d.GasPercent
	}
	if d.SignalStrength != nil {
		m["signal_dbm"] = *d.SignalStrength
	}
	if d.GasBuddyOn != nil {
		m["gasbuddy_connected"] = *d.GasBuddyOn
	}
	if d.TempWarnActive != nil {
		m["temp_warning_active"] = *d.TempWarnActive
	}
	if d.LightThreshold != nil {
		m["light_threshold_percent"] = *d.LightThreshold
	}
	return m
}

// Flat wandelt den zusammengeführten 9elements-Zustand in flache Paare.
func (s *nineState) Flat() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := map[string]any{}
	for i, z := range s.Zones {
		m[fmt.Sprintf("zone%d", i+1)] = z
	}
	for i, p := range s.Probes {
		m[fmt.Sprintf("probe%d", i+1)] = p
	}
	putBool(m, "hood_open", s.HoodOpen)
	putBool(m, "light_on", s.LightOn)
	putBool(m, "gasbuddy_connected", s.GasBuddyOn)
	putBool(m, "temp_warning_active", s.TempWarnActive)
	putBool(m, "temp_warning_enabled", s.TempWarnEnabled)
	putInt(m, "gas_percent", s.GasPercent)
	putInt(m, "weight_g", s.WeightGrams)
	putInt(m, "signal_dbm", s.SignalDBm)
	putInt(m, "light_threshold_percent", s.LightThreshold)
	return m
}

func putBool(m map[string]any, k string, v *bool) {
	if v != nil {
		m[k] = *v
	}
}
func putInt(m map[string]any, k string, v *int) {
	if v != nil {
		m[k] = *v
	}
}
