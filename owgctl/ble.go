package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// Dieser Teil liest den Grill LOKAL über Bluetooth LE — ohne Hersteller-Cloud
// und ohne Konto. UUIDs und Frame-Format stammen aus der App-Dekompilierung
// (notizen/2026-09-27_*). Der Lesepfad schreibt nichts an den Grill.

// Bekannte UUIDs. Legacy: der 28-Byte-Frame kommt als Notification auf getData.
var (
	uuidLegacyService = mustUUID("dc0f41ea-b6ae-46a8-a19e-1a3bf4342bcb")
	uuidLegacyNotify  = mustUUID("dc0f41e2-b6ae-46a8-a19e-1a3bf4342bcb") // getData (TX)

	// 9elements: einzelne Characteristics statt einem Sammel-Frame.
	// Services
	uuidTempService   = bluetooth.New16BitUUID(0x181A)
	uuidBinaryService = bluetooth.New16BitUUID(0x183B)
	uuidGasService    = bluetooth.New16BitUUID(0x181D)
	uuidInfoService   = bluetooth.New16BitUUID(0x180A)
	uuidMgmtService   = mustUUID("830e02f4-843b-49c3-a4fa-613587247e6c")
	uuidGrillMode     = mustUUID("830e02f7-843b-49c3-a4fa-613587247e6c")
	uuidPop           = mustUUID("830e02f6-843b-49c3-a4fa-613587247e6c")

	// Gas-Kalibrierung (lesbar), für 'ble info'
	uuidGasBottleWeight = mustUUID("64ebc8a3-e9a4-11ed-a05b-0242ac120003")
	uuidGasFullWeight   = mustUUID("64ebc8a4-e9a4-11ed-a05b-0242ac120003")
	uuidGasCriticalPct  = mustUUID("64ebc8a6-e9a4-11ed-a05b-0242ac120003")
	uuidGasTare         = mustUUID("64ebc8a7-e9a4-11ed-a05b-0242ac120003")
	// Characteristics
	uuidTempMeasure     = bluetooth.New16BitUUID(0x2A1C)
	uuidHoodStatus      = mustUUID("e1255ec1-6199-44ab-b78e-08ec262cea4b")
	uuidLightStatus     = mustUUID("e1255ec0-6199-44ab-b78e-08ec262cea4b")
	uuidLightThreshold  = mustUUID("e1255ec2-6199-44ab-b78e-08ec262cea4b")
	uuidGasPercent      = mustUUID("64ebc8a5-e9a4-11ed-a05b-0242ac120003")
	uuidGasConnected    = mustUUID("64ebc8a9-e9a4-11ed-a05b-0242ac120003")
	uuidWeightMeasure   = bluetooth.New16BitUUID(0x2A9D)
	uuidSignalStrength  = mustUUID("5f8b9bdc-86f7-4c68-9dce-3d1d4cb21b95")
	uuidTempWarnActive  = mustUUID("1f168db3-72ba-4806-84cc-e1c310898566")
	uuidTempWarnEnabled = mustUUID("1f168db4-72ba-4806-84cc-e1c310898566")
	uuidSerialNumber    = bluetooth.New16BitUUID(0x2A25)
	uuidFirmwareVersion = bluetooth.New16BitUUID(0x2A26)
)

// name9Elements erkennt die neue Generation am Bluetooth-Namen.
var name9Elements = regexp.MustCompile(`^OWG-G32C-[A-F0-9]{8}$`)

func mustUUID(s string) bluetooth.UUID {
	u, err := bluetooth.ParseUUID(s)
	if err != nil {
		panic("ungültige UUID " + s + ": " + err.Error())
	}
	return u
}

// cmdBLE ist der Einstieg für die lokalen Bluetooth-Befehle (offline).
func cmdBLE(ctx context.Context, opt options) error {
	sub := ""
	if len(opt.rest) > 0 {
		sub = opt.rest[0]
	}
	filter := ""
	if len(opt.rest) > 1 {
		filter = opt.rest[1]
	}

	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("Bluetooth-Adapter aktivieren: %w", err)
	}

	switch sub {
	case "scan":
		return bleScan(ctx, adapter)
	case "read":
		sk, err := makeSink(opt)
		if err != nil {
			return err
		}
		defer sk.close()
		var rawW *os.File
		if opt.raw != "" {
			rawW, err = os.OpenFile(opt.raw, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return fmt.Errorf("Rohmitschnitt-Datei: %w", err)
			}
			defer rawW.Close()
			fmt.Printf("Rohframes → %s (später mit 'replay' erneut analysierbar)\n", opt.raw)
		}
		return bleRead(ctx, adapter, filter, sk, rawW)
	case "info":
		return bleInfo(ctx, adapter, filter)
	case "dump":
		return bleDump(ctx, adapter, filter)
	case "set":
		return bleSet(ctx, adapter, opt)
	default:
		return fmt.Errorf("Aufruf: owgctl ble <scan|read|info|set> [...]")
	}
}

// bleDump entdeckt ALLE Services und Characteristics (auch undokumentierte),
// liest jede lesbare einmal und gibt UUID + Rohbytes + Länge aus. Rein lesend.
// Das POP (830e02f6…) wird redigiert (Geräte-Secret).
func bleDump(ctx context.Context, a *bluetooth.Adapter, filter string) error {
	hit, err := findGrill(ctx, a, filter)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Verbinde mit %s (%s) ...\n", hit.name, hit.address.String())
	dev, err := a.Connect(hit.address, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("Verbindung: %w", err)
	}
	defer dev.Disconnect()

	svcs, err := dev.DiscoverServices(nil)
	if err != nil {
		return fmt.Errorf("Service-Discovery: %w", err)
	}
	fmt.Printf("# %s  (%s, %d Services)\n", hit.name, hit.address.String(), len(svcs))
	for _, svc := range svcs {
		fmt.Printf("\nService %s\n", svc.UUID().String())
		chars, err := svc.DiscoverCharacteristics(nil)
		if err != nil {
			fmt.Printf("  (Characteristics-Discovery fehlgeschlagen: %v)\n", err)
			continue
		}
		for _, c := range chars {
			u := c.UUID().String()
			if strings.HasPrefix(u, "830e02f6") {
				fmt.Printf("  char %s  = (POP, redigiert)\n", u)
				continue
			}
			buf := make([]byte, 256)
			n, err := c.Read(buf)
			if err != nil {
				fmt.Printf("  char %s  = (nicht lesbar: notify/write-only)\n", u)
				continue
			}
			b := buf[:n]
			ascii := ""
			if isAllPrintable(b) {
				ascii = "  \"" + string(b) + "\""
			}
			fmt.Printf("  char %s  (%2d B) = %x%s\n", u, n, b, ascii)
		}
	}
	return nil
}

func isAllPrintable(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// bleInfo verbindet einmal und liest alle lesbaren Characteristics (Geräteinfo,
// POP, aktuelle Sensor-/Gaswerte, Gas-Kalibrierung) direkt aus — ohne auf
// Notifications zu warten. Praktisch für idle Boards (Testgeräte außerhalb des
// Grills), die keine Live-Daten pushen. Rein lesend.
func bleInfo(ctx context.Context, a *bluetooth.Adapter, filter string) error {
	hit, err := findGrill(ctx, a, filter)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Verbinde mit %s (%s) ...\n", hit.name, hit.address.String())
	dev, err := a.Connect(hit.address, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("Verbindung: %w", err)
	}
	defer dev.Disconnect()

	out := map[string]any{
		"name":    hit.name,
		"address": hit.address.String(),
		"rssi":    hit.rssi,
	}
	if name9Elements.MatchString(hit.name) {
		out["generation"] = "9elements"
	} else {
		out["generation"] = "legacy"
		out["hinweis"] = "Legacy liefert Daten nur als Notification-Frame — 'ble read' nutzen."
	}

	// readRaw liest eine Characteristic einmal; ok=false wenn nicht vorhanden/lesbar.
	readRaw := func(service, char bluetooth.UUID) ([]byte, bool) {
		chars, err := discover(dev, service, char)
		if err != nil {
			return nil, false
		}
		c, ok := chars[char.String()]
		if !ok {
			return nil, false
		}
		buf := make([]byte, 64)
		n, err := c.Read(buf)
		if err != nil {
			return nil, false
		}
		return buf[:n], true
	}
	str := func(key string, service, char bluetooth.UUID) {
		if b, ok := readRaw(service, char); ok {
			out[key] = strings.TrimRight(string(b), "\x00")
		}
	}
	u8 := func(key string, service, char bluetooth.UUID) {
		if b, ok := readRaw(service, char); ok && len(b) >= 1 {
			out[key] = int(b[0])
		}
	}
	boolean := func(key string, service, char bluetooth.UUID) {
		if b, ok := readRaw(service, char); ok && len(b) >= 1 {
			out[key] = b[0] > 0
		}
	}
	u16be := func(key string, service, char bluetooth.UUID) {
		if b, ok := readRaw(service, char); ok && len(b) >= 2 {
			out[key] = int(binary.BigEndian.Uint16(b[:2]))
		}
	}

	// Geräteinfo
	str("serial", uuidInfoService, uuidSerialNumber)
	str("firmware", uuidInfoService, uuidFirmwareVersion)
	if b, ok := readRaw(uuidInfoService, uuidSignalStrength); ok && len(b) >= 1 {
		out["signal_dbm"] = int(int8(b[0]))
	}
	// Verwaltung. POP ist ein Geräte-Secret → nur Vorhandensein/Länge zeigen, nie den Wert.
	if b, ok := readRaw(uuidMgmtService, uuidPop); ok {
		out["pop_present"] = true
		out["pop_len"] = len(strings.TrimRight(string(b), "\x00"))
	}
	u8("grill_mode", uuidMgmtService, uuidGrillMode)
	// Temperaturen (einmaliger Read des Measurement-Chars, falls lesbar)
	if b, ok := readRaw(uuidTempService, uuidTempMeasure); ok {
		out["temp_raw_hex"] = fmt.Sprintf("%x", b)
		if readings, err := Decode9ElementsTemps(b); err == nil {
			var zones, probes []int
			for _, r := range readings {
				if r.Type == "zone" {
					zones = append(zones, r.TempC)
				} else {
					probes = append(probes, r.TempC)
				}
			}
			out["zones_c"] = zones
			out["probes_c"] = probes
		}
	}
	boolean("temp_warning_enabled", uuidTempService, uuidTempWarnEnabled)
	// Binärsensoren
	boolean("hood_open", uuidBinaryService, uuidHoodStatus)
	boolean("light_on", uuidBinaryService, uuidLightStatus)
	u8("light_threshold_pct", uuidBinaryService, uuidLightThreshold)
	// Gas
	u8("gas_percent", uuidGasService, uuidGasPercent)
	u16be("gas_weight_g", uuidGasService, uuidWeightMeasure)
	boolean("gasbuddy_connected", uuidGasService, uuidGasConnected)
	u16be("gas_bottle_weight_g", uuidGasService, uuidGasBottleWeight)
	u16be("gas_full_weight_g", uuidGasService, uuidGasFullWeight)
	u8("gas_critical_pct", uuidGasService, uuidGasCriticalPct)
	u16be("gas_tare_g", uuidGasService, uuidGasTare)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// bleSet schreibt eine Einstellung an den Grill (nur 9elements-Firmware).
// Aufruf: owgctl ble set <mode|hoodlight|warning> <wert> [namefilter]
//
// Achtung: schreibt auf das Gerät. Nur dokumentierte, ungefährliche
// Einstellungen; keine Firmware, kein OTA.
func bleSet(ctx context.Context, a *bluetooth.Adapter, opt options) error {
	if len(opt.rest) < 3 {
		return fmt.Errorf("Aufruf: owgctl ble set <mode 0-3|hoodlight 0-100|warning on/off> <wert> [namefilter]")
	}
	what, value := opt.rest[1], opt.rest[2]
	filter := ""
	if len(opt.rest) > 3 {
		filter = opt.rest[3]
	}

	var service, char bluetooth.UUID
	var payload []byte
	switch what {
	case "mode":
		n, err := parseRange(value, 0, 3)
		if err != nil {
			return fmt.Errorf("mode: %w", err)
		}
		service, char, payload = uuidMgmtService, uuidGrillMode, []byte{byte(n)}
	case "hoodlight":
		n, err := parseRange(value, 0, 100)
		if err != nil {
			return fmt.Errorf("hoodlight: %w", err)
		}
		service, char, payload = uuidBinaryService, uuidLightThreshold, []byte{byte(n)}
	case "warning":
		on, err := parseOnOff(value)
		if err != nil {
			return err
		}
		b := byte(0)
		if on {
			b = 1
		}
		service, char, payload = uuidTempService, uuidTempWarnEnabled, []byte{b}
	default:
		return fmt.Errorf("unbekannt: %q (mode|hoodlight|warning)", what)
	}

	hit, err := findGrill(ctx, a, filter)
	if err != nil {
		return err
	}
	if !name9Elements.MatchString(hit.name) {
		return fmt.Errorf("Schreiben ist nur für 9elements-Firmware belegt; %q sieht nach Legacy aus", hit.name)
	}
	fmt.Printf("Verbinde mit %s ...\n", hit.name)
	dev, err := a.Connect(hit.address, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("Verbindung: %w", err)
	}
	defer dev.Disconnect()

	chars, err := discover(dev, service, char)
	if err != nil {
		return err
	}
	c, ok := chars[char.String()]
	if !ok {
		return fmt.Errorf("Characteristic %s nicht gefunden", char.String())
	}
	if _, err := c.Write(payload); err != nil {
		return fmt.Errorf("Schreiben: %w", err)
	}
	fmt.Printf("OK: %s = %s geschrieben (% x an %s)\n", what, value, payload, char.String())
	return nil
}

// parseRange parst eine Ganzzahl und prüft die Grenzen.
func parseRange(s string, lo, hi int) (int, error) {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("keine Zahl: %q", s)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%d außerhalb %d..%d", n, lo, hi)
	}
	return n, nil
}

// parseOnOff akzeptiert on/off, an/aus, true/false, 1/0.
func parseOnOff(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "on", "an", "true", "1", "ein":
		return true, nil
	case "off", "aus", "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("warning: %q nicht verstanden (on/off)", s)
}

// grillHit ist ein gefundenes Gerät (Adresse + Name, aus dem Scan kopiert).
type grillHit struct {
	address bluetooth.Address
	name    string
	rssi    int16
}

// isOWG prüft, ob ein Advertising-Name zu einem Otto-Wilde-Grill gehört.
func isOWG(name string) bool {
	return strings.HasPrefix(name, "OWG") || strings.HasPrefix(name, "OTTO")
}

// bleScan sucht 10 s lang nach Grills und listet sie.
func bleScan(ctx context.Context, a *bluetooth.Adapter) error {
	fmt.Println("Scanne 10 s nach Otto-Wilde-Grills ...")
	seen := map[string]bool{}
	stopScanAfter(ctx, a, 10*time.Second)
	err := a.Scan(func(a *bluetooth.Adapter, res bluetooth.ScanResult) {
		name := res.LocalName()
		if !isOWG(name) {
			return
		}
		addr := res.Address.String()
		if seen[addr] {
			return
		}
		seen[addr] = true
		gen := "legacy"
		if name9Elements.MatchString(name) {
			gen = "9elements"
		}
		fmt.Printf("  %s  RSSI %4d dBm  %-20s  [%s]\n", addr, res.RSSI, name, gen)
	})
	if err != nil {
		return err
	}
	if len(seen) == 0 {
		fmt.Println("Kein Grill gefunden. Grill an? In Reichweite? Nicht mit der App verbunden?")
	}
	return nil
}

// bleRead verbindet mit dem ersten passenden Grill und gibt Live-Daten als
// JSON-Zeilen aus, bis der Nutzer abbricht (Strg-C).
func bleRead(ctx context.Context, a *bluetooth.Adapter, filter string, sk sink, rawW io.Writer) error {
	// Reconnect-Schleife: bei Verbindungsabbruch neu scannen und verbinden,
	// bis der Nutzer abbricht (Strg-C / ctx).
	for ctx.Err() == nil {
		hit, err := findGrill(ctx, a, filter)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Suche: %v — neuer Versuch in 5 s\n", err)
			sleepCtx(ctx, 5*time.Second)
			continue
		}
		if err := readOnce(ctx, a, hit, sk, rawW); err != nil {
			fmt.Fprintf(os.Stderr, "Sitzung: %v\n", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		fmt.Println("Verbindung verloren — neuer Versuch in 5 s ...")
		sleepCtx(ctx, 5*time.Second)
	}
	return nil
}

// readOnce verbindet einmal, abonniert und wartet, bis der Grill die Verbindung
// trennt oder der Nutzer abbricht.
func readOnce(ctx context.Context, a *bluetooth.Adapter, hit grillHit, sk sink, rawW io.Writer) error {
	fmt.Printf("Verbinde mit %s (%s) ...\n", hit.name, hit.address.String())

	disconnected := make(chan struct{})
	var once sync.Once
	a.SetConnectHandler(func(d bluetooth.Device, connected bool) {
		if !connected {
			once.Do(func() { close(disconnected) })
		}
	})

	dev, err := a.Connect(hit.address, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("Verbindung: %w", err)
	}
	defer dev.Disconnect()

	if name9Elements.MatchString(hit.name) {
		err = subscribe9Elements(dev, sk)
	} else {
		err = subscribeLegacy(dev, sk, rawW)
	}
	if err != nil {
		return err
	}
	fmt.Println("Live-Daten abonniert (Strg-C zum Beenden) ...")

	select {
	case <-ctx.Done():
	case <-disconnected:
	}
	return nil
}

// sleepCtx wartet d oder bis ctx abgebrochen wird.
func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// emit reicht einen Messwert-Satz an die Sink; Fehler landen auf stderr,
// ohne den Lauf abzubrechen.
func emit(sk sink, serial string, flat map[string]any) {
	if err := sk.publish(serial, flat); err != nil {
		fmt.Fprintln(os.Stderr, "Ausgabe:", err)
	}
}

// findGrill scannt, bis ein passender Grill auftaucht, und stoppt dann den Scan.
func findGrill(ctx context.Context, a *bluetooth.Adapter, filter string) (grillHit, error) {
	var found grillHit
	var got bool
	stopScanAfter(ctx, a, 15*time.Second)
	err := a.Scan(func(a *bluetooth.Adapter, res bluetooth.ScanResult) {
		name := res.LocalName()
		if !isOWG(name) {
			return
		}
		if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
			return
		}
		found = grillHit{address: res.Address, name: name, rssi: res.RSSI}
		got = true
		a.StopScan()
	})
	if err != nil {
		return grillHit{}, err
	}
	if !got {
		return grillHit{}, fmt.Errorf("kein passender Grill gefunden (Filter %q)", filter)
	}
	return found, nil
}

// readLegacy abonniert den 28-Byte-Frame und dekodiert jede Notification.
func subscribeLegacy(dev bluetooth.Device, sk sink, rawW io.Writer) error {
	svcs, err := dev.DiscoverServices([]bluetooth.UUID{uuidLegacyService})
	if err != nil || len(svcs) == 0 {
		return fmt.Errorf("Legacy-Service nicht gefunden: %w", err)
	}
	chars, err := svcs[0].DiscoverCharacteristics([]bluetooth.UUID{uuidLegacyNotify})
	if err != nil || len(chars) == 0 {
		return fmt.Errorf("Notify-Characteristic nicht gefunden: %w", err)
	}
	err = chars[0].EnableNotifications(func(buf []byte) {
		if rawW != nil {
			fmt.Fprintf(rawW, "%x\n", buf) // Rohframe für spätere Analyse
		}
		d, err := DecodeLegacyFrame(buf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Frame verworfen (%v): %x\n", err, buf)
			return
		}
		emit(sk, d.Serial, d.Flat())
	})
	if err != nil {
		return fmt.Errorf("Notifications aktivieren: %w", err)
	}
	return nil
}

// nineState hält den zusammengeführten Zustand der 9elements-Generation.
// Mehrere Characteristics liefern unabhängig; jede Notification aktualisiert ein
// Feld und gibt den Gesamtzustand als JSON-Zeile aus (wie combineLatest der App).
type nineState struct {
	mu   sync.Mutex
	sink sink

	Zones           []int  `json:"zonesC,omitempty"`
	Probes          []int  `json:"probesC,omitempty"`
	HoodOpen        *bool  `json:"hoodOpen,omitempty"`
	LightOn         *bool  `json:"lightOn,omitempty"`
	LightThreshold  *int   `json:"lightThresholdPercent,omitempty"`
	GasPercent      *int   `json:"gasPercent,omitempty"`
	GasBuddyOn      *bool  `json:"gasBuddyConnected,omitempty"`
	WeightGrams     *int   `json:"weightGrams,omitempty"`
	SignalDBm       *int   `json:"signalDbm,omitempty"`
	TempWarnActive  *bool  `json:"tempWarningActive,omitempty"`
	TempWarnEnabled *bool  `json:"tempWarningEnabled,omitempty"`
	Serial          string `json:"serial,omitempty"`
	Firmware        string `json:"firmware,omitempty"`
}

func (s *nineState) emit() {
	flat := s.Flat() // sperrt s.mu selbst
	s.mu.Lock()
	serial := s.Serial
	s.mu.Unlock()
	emit(s.sink, serial, flat)
}

// read9Elements verbindet die einzelnen Characteristics der neuen Generation zu
// einem gemeinsamen Zustand. Statische Werte (Serial, Firmware) werden einmal
// gelesen, die Live-Werte abonniert.
func subscribe9Elements(dev bluetooth.Device, sk sink) error {
	st := &nineState{sink: sk}

	// Geräteinfo einmalig lesen (nicht kritisch, wenn es fehlschlägt).
	if chars, err := discover(dev, uuidInfoService, uuidSerialNumber, uuidFirmwareVersion, uuidSignalStrength); err == nil {
		if c, ok := chars[uuidSerialNumber.String()]; ok {
			st.Serial = readString(c)
		}
		if c, ok := chars[uuidFirmwareVersion.String()]; ok {
			st.Firmware = readString(c)
		}
		if c, ok := chars[uuidSignalStrength.String()]; ok {
			subscribe(c, func(b []byte) {
				if len(b) >= 1 {
					v := int(int8(b[0]))
					st.mu.Lock()
					st.SignalDBm = &v
					st.mu.Unlock()
					st.emit()
				}
			})
		}
	}

	// Temperaturen (Service 181A) + Übertemperatur-Warnung.
	if chars, err := discover(dev, uuidTempService, uuidTempMeasure, uuidTempWarnActive, uuidTempWarnEnabled); err == nil {
		if c, ok := chars[uuidTempMeasure.String()]; ok {
			subscribe(c, func(b []byte) {
				readings, err := Decode9ElementsTemps(b)
				if err != nil {
					return
				}
				var zones, probes []int
				for _, r := range readings {
					if r.Type == "zone" {
						zones = append(zones, r.TempC)
					} else {
						probes = append(probes, r.TempC)
					}
				}
				st.mu.Lock()
				st.Zones, st.Probes = zones, probes
				st.mu.Unlock()
				st.emit()
			})
		}
		subscribeBool(chars, uuidTempWarnActive, st, func(s *nineState, v *bool) { s.TempWarnActive = v })
		subscribeBool(chars, uuidTempWarnEnabled, st, func(s *nineState, v *bool) { s.TempWarnEnabled = v })
	}

	// Binärsensoren (Service 183B): Haube, Licht, Licht-Schwelle.
	if chars, err := discover(dev, uuidBinaryService, uuidHoodStatus, uuidLightStatus, uuidLightThreshold); err == nil {
		subscribeBool(chars, uuidHoodStatus, st, func(s *nineState, v *bool) { s.HoodOpen = v })
		subscribeBool(chars, uuidLightStatus, st, func(s *nineState, v *bool) { s.LightOn = v })
		subscribeU8(chars, uuidLightThreshold, st, func(s *nineState, v *int) { s.LightThreshold = v })
	}

	// Gaswaage (Service 181D): Prozent, Gewicht, Verbunden-Status.
	if chars, err := discover(dev, uuidGasService, uuidGasPercent, uuidWeightMeasure, uuidGasConnected); err == nil {
		subscribeU8(chars, uuidGasPercent, st, func(s *nineState, v *int) { s.GasPercent = v })
		subscribeBool(chars, uuidGasConnected, st, func(s *nineState, v *bool) { s.GasBuddyOn = v })
		if c, ok := chars[uuidWeightMeasure.String()]; ok {
			subscribe(c, func(b []byte) {
				if len(b) >= 2 {
					v := int(binary.BigEndian.Uint16(b[:2]))
					st.mu.Lock()
					st.WeightGrams = &v
					st.mu.Unlock()
					st.emit()
				}
			})
		}
	}

	return nil
}

// discover findet einen Service und die genannten Characteristics darin und
// liefert sie als Map UUID-String → Characteristic.
func discover(dev bluetooth.Device, service bluetooth.UUID, chars ...bluetooth.UUID) (map[string]bluetooth.DeviceCharacteristic, error) {
	svcs, err := dev.DiscoverServices([]bluetooth.UUID{service})
	if err != nil || len(svcs) == 0 {
		return nil, fmt.Errorf("Service %s nicht gefunden: %w", service.String(), err)
	}
	found, err := svcs[0].DiscoverCharacteristics(chars)
	if err != nil {
		return nil, err
	}
	m := make(map[string]bluetooth.DeviceCharacteristic, len(found))
	for _, c := range found {
		m[c.UUID().String()] = c
	}
	return m, nil
}

func subscribe(c bluetooth.DeviceCharacteristic, fn func([]byte)) {
	_ = c.EnableNotifications(fn)
}

// subscribeBool abonniert eine Bool-Characteristic (erstes Byte > 0 = true).
func subscribeBool(chars map[string]bluetooth.DeviceCharacteristic, u bluetooth.UUID, st *nineState, set func(*nineState, *bool)) {
	c, ok := chars[u.String()]
	if !ok {
		return
	}
	subscribe(c, func(b []byte) {
		if len(b) < 1 {
			return
		}
		v := b[0] > 0
		st.mu.Lock()
		set(st, &v)
		st.mu.Unlock()
		st.emit()
	})
}

// subscribeU8 abonniert eine uint8-Characteristic.
func subscribeU8(chars map[string]bluetooth.DeviceCharacteristic, u bluetooth.UUID, st *nineState, set func(*nineState, *int)) {
	c, ok := chars[u.String()]
	if !ok {
		return
	}
	subscribe(c, func(b []byte) {
		if len(b) < 1 {
			return
		}
		v := int(b[0])
		st.mu.Lock()
		set(st, &v)
		st.mu.Unlock()
		st.emit()
	})
}

// readString liest eine Characteristic als UTF-8-String (für Serial/Firmware).
func readString(c bluetooth.DeviceCharacteristic) string {
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(buf[:n]), "\x00")
}

// stopScanAfter stoppt den Scan nach d oder bei Abbruch, je nachdem was zuerst kommt.
func stopScanAfter(ctx context.Context, a *bluetooth.Adapter, d time.Duration) {
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
		a.StopScan()
	}()
}
