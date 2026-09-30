package main

import "testing"

func TestSensorMeta(t *testing.T) {
	cases := []struct {
		key               string
		comp, unit, class string
	}{
		{"zone1", "sensor", "°C", "temperature"},
		{"probe3", "sensor", "°C", "temperature"},
		{"gas_percent", "sensor", "%", ""},
		{"light_threshold_percent", "sensor", "%", ""},
		{"gas_stock_g", "sensor", "g", "weight"},
		{"weight_g", "sensor", "g", "weight"},
		{"signal_dbm", "sensor", "dBm", "signal_strength"},
		{"hood_open", "binary_sensor", "", ""},
		{"light_on", "binary_sensor", "", ""},
		{"temp_warning_active", "binary_sensor", "", ""},
		{"gasbuddy_connected", "binary_sensor", "", ""},
	}
	for _, c := range cases {
		comp, unit, class := sensorMeta(c.key)
		if comp != c.comp || unit != c.unit || class != c.class {
			t.Errorf("sensorMeta(%q) = (%q,%q,%q), erwartet (%q,%q,%q)",
				c.key, comp, unit, class, c.comp, c.unit, c.class)
		}
	}
}

func TestSafeID(t *testing.T) {
	cases := map[string]string{
		"12345678":     "12345678",
		"a:b/c-d":      "a_b_c_d",
		"":             "grill",
		"OWG G32":      "OWG_G32",
		"ünïcode":      "_n_code", // Nicht-ASCII-Rune → je ein _
	}
	for in, want := range cases {
		if got := safeID(in); got != want {
			t.Errorf("safeID(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

func TestNineStateFlat(t *testing.T) {
	b := func(v bool) *bool { return &v }
	i := func(v int) *int { return &v }
	s := &nineState{
		Zones:      []int{200, 180},
		Probes:     []int{63},
		HoodOpen:   b(true),
		LightOn:    b(false),
		GasPercent: i(42),
		SignalDBm:  i(-70),
	}
	flat := s.Flat()

	if flat["zone1"] != 200 || flat["zone2"] != 180 {
		t.Errorf("Zonen falsch: %v", flat)
	}
	if flat["probe1"] != 63 {
		t.Errorf("probe1 = %v, erwartet 63", flat["probe1"])
	}
	if flat["hood_open"] != true || flat["light_on"] != false {
		t.Errorf("Haube/Licht falsch: %v", flat)
	}
	if flat["gas_percent"] != 42 || flat["signal_dbm"] != -70 {
		t.Errorf("Gas/Signal falsch: %v", flat)
	}
	// Nicht gesetzte Felder dürfen nicht auftauchen.
	for _, k := range []string{"weight_g", "temp_warning_active", "gasbuddy_connected"} {
		if _, ok := flat[k]; ok {
			t.Errorf("%q sollte fehlen (nil), ist aber gesetzt", k)
		}
	}
}

func TestStdoutSinkPublish(t *testing.T) {
	// Darf nicht panicken und keinen Fehler liefern.
	if err := (stdoutSink{}).publish("12345678", map[string]any{"zone1": 200}); err != nil {
		t.Errorf("publish: %v", err)
	}
}
