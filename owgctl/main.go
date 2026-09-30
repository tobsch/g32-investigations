// owgctl ist ein kleines CLI, um über das EIGENE Otto-Wilde-Konto die eigenen
// Grills zu listen, die Firmware fürs eigene Gerät zu sichern und die eigene
// Session-Historie zu exportieren — solange die Hersteller-Cloud noch läuft.
//
// Zugangsdaten kommen aus einer .env-Datei (OW_USER, OW_PASS, OW_SERIAL).
// Das Tool läuft auf dem eigenen Rechner; das Passwort geht nur an den
// Otto-Wilde-Server und wird nirgends ausgegeben.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cmd, rest := args[0], args[1:]

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		usage()
		return
	}

	if err := run(ctx, cmd, rest); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`owgctl — lokaler Zugriff auf das eigene Otto-Wilde-Konto

Aufruf:
  owgctl <befehl> [optionen]

Befehle:
  login                    Anmeldung testen (gibt nur Erfolg/Fehler aus)
  grills                   eigene Grills auflisten (Seriennummer, Firmware)
  firmware [serial]        Firmware-Metadaten zur Seriennummer anzeigen
  download [serial]        Firmware-Binärdatei sichern (--out DIR)
  sessions [serial]        Session-Historie auflisten (--dump DIR sichert Details)
  export                   ALLES sichern: Grills + Firmware + Sessions (--out DIR)
  decode <hex>             Live-Frame offline dekodieren (ohne Grill, ohne Konto)
  ble scan                 lokal per Bluetooth nach Grills suchen
  ble read [namefilter]    Grill lokal auslesen, Live-Daten als JSON-Zeilen
                           (--mqtt tcp://host:1883 → Home Assistant statt stdout)
                           (--raw datei.hex → Rohframes mitschneiden, Legacy)
  ble set <ding> <wert>    Einstellung schreiben (9elements): mode 0-3,
                           hoodlight 0-100, warning on/off
  replay <datei|->         gespeicherte Hex-Frames einspeisen (Test ohne Grill;
                           mit --mqtt/--delay/--loop bis Home Assistant prüfbar)
  serve-socket             als Cloud-Socket ausgeben und mitschneiden, was der
                           Grill im WLAN sendet (--port 4502, --raw log.txt)

Optionen:
  --env PFAD    andere .env-Datei benutzen (Standard: .env im Projektbaum)
  --out DIR     Zielverzeichnis für download (Standard: firmware/)
  --dump DIR    Zielverzeichnis für sessions (JE Session eine JSON-Datei)

Zugangsdaten (aus .env oder Umgebung):
  OW_USER, OW_PASS   Otto-Wilde-Konto
  OW_SERIAL          Seriennummer des Grills (optional, sonst als Argument)
  OW_BASE_URL        API-Host (optional)
`)
}

// options bündelt die von mehreren Befehlen genutzten Flags.
type options struct {
	env     string
	out     string
	dump    string
	mqtt    string
	raw     string
	port    int
	delayMs int
	loop    bool
	rest    []string // verbleibende Positionsargumente
}

// parseFlags zieht die bekannten --flags aus args heraus; alles andere bleibt
// als Positionsargument stehen. Bewusst simpel gehalten.
func parseFlags(args []string) (options, error) {
	var o options
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s erwartet einen Wert", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--env":
			o.env, err = next()
		case "--out":
			o.out, err = next()
		case "--dump":
			o.dump, err = next()
		case "--mqtt":
			o.mqtt, err = next()
		case "--raw":
			o.raw, err = next()
		case "--port":
			var v string
			if v, err = next(); err == nil {
				_, err = fmt.Sscanf(v, "%d", &o.port)
			}
		case "--delay":
			var v string
			if v, err = next(); err == nil {
				_, err = fmt.Sscanf(v, "%d", &o.delayMs)
			}
		case "--loop":
			o.loop = true
		default:
			if strings.HasPrefix(a, "--") {
				return o, fmt.Errorf("unbekannte Option: %s", a)
			}
			o.rest = append(o.rest, a)
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

func run(ctx context.Context, cmd string, args []string) error {
	opt, err := parseFlags(args)
	if err != nil {
		return err
	}

	// decode und ble arbeiten offline — kein Konto, kein Server, keine .env nötig.
	if cmd == "decode" {
		return cmdDecode(opt.rest)
	}
	if cmd == "ble" {
		return cmdBLE(ctx, opt)
	}
	if cmd == "replay" {
		return cmdReplay(ctx, opt)
	}
	if cmd == "serve-socket" {
		return cmdServe(ctx, opt)
	}

	cfg, err := loadConfig(opt.env)
	if err != nil {
		return err
	}
	if err := cfg.requireCredentials(); err != nil {
		return err
	}

	// resolveSerial nimmt das erste Positionsargument, sonst OW_SERIAL.
	resolveSerial := func() (string, error) {
		if len(opt.rest) > 0 {
			return opt.rest[0], nil
		}
		if cfg.serial != "" {
			return cfg.serial, nil
		}
		return "", fmt.Errorf("keine Seriennummer — als Argument angeben oder OW_SERIAL in .env setzen")
	}

	c := newClient(cfg)
	if err := c.login(ctx, cfg.user, cfg.password); err != nil {
		return err
	}

	switch cmd {
	case "login":
		fmt.Println("Anmeldung erfolgreich.")
		return nil

	case "grills":
		return cmdGrills(ctx, c)

	case "firmware":
		serial, err := resolveSerial()
		if err != nil {
			return err
		}
		return cmdFirmware(ctx, c, serial)

	case "download":
		serial, err := resolveSerial()
		if err != nil {
			return err
		}
		return cmdDownload(ctx, c, serial, opt.out)

	case "sessions":
		serial, err := resolveSerial()
		if err != nil {
			return err
		}
		return cmdSessions(ctx, c, serial, opt.dump)

	case "export":
		return cmdExport(ctx, c, opt.out)

	default:
		usage()
		return fmt.Errorf("unbekannter Befehl: %s", cmd)
	}
}

func cmdGrills(ctx context.Context, c *client) error {
	grills, err := c.grills(ctx)
	if err != nil {
		return err
	}
	if len(grills) == 0 {
		fmt.Println("Keine Grills im Konto.")
		return nil
	}
	for _, g := range grills {
		name := g.Nickname
		if name == "" {
			name = "(ohne Namen)"
		}
		fmt.Printf("%-16s  %s  FW %s (Code %d)  BLE %q  WLAN %v\n",
			g.SerialNumber, name, g.FirmwareSemanticVersion, g.FirmwareVersionCode,
			g.Bluetooth.Name, g.IsWifiConnected)
	}
	return nil
}

func cmdFirmware(ctx context.Context, c *client, serial string) error {
	fw, err := c.firmwareFor(ctx, serial)
	if err != nil {
		return err
	}
	fmt.Printf("Seriennummer:     %s\n", serial)
	fmt.Printf("Firmware-ID:      %s\n", fw.ID)
	fmt.Printf("Version:          %s (semver %s)\n", fw.version(), fw.SemanticVersion)
	fmt.Printf("Dateiname:        %s\n", fw.Filename)
	if fw.ReleaseNotes != "" {
		fmt.Printf("Release Notes:    %s\n", fw.ReleaseNotes)
	}
	return nil
}

func cmdDownload(ctx context.Context, c *client, serial, outDir string) error {
	if outDir == "" {
		outDir = "firmware"
	}
	fw, err := c.firmwareFor(ctx, serial)
	if err != nil {
		return err
	}
	if fw.ID == "" {
		return fmt.Errorf("keine Firmware-ID für %s erhalten", serial)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("update-%s.bin", sanitize(fw.version()))
	path := filepath.Join(outDir, name)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	n, err := c.downloadFirmware(ctx, string(fw.ID), f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return err
	}
	fmt.Printf("Gesichert: %s (%d Bytes, Version %s)\n", path, n, fw.version())
	return nil
}

func cmdSessions(ctx context.Context, c *client, serial, dumpDir string) error {
	const size = 20
	var all []sessionSummary
	for page := 0; ; page++ {
		batch, err := c.sessionsPage(ctx, serial, page, size)
		if err != nil {
			return err
		}
		all = append(all, batch...)
		if len(batch) < size {
			break
		}
	}
	fmt.Printf("%d Sessions für %s\n", len(all), serial)
	for _, s := range all {
		name := s.SessionName
		if name == "" {
			name = "(ohne Namen)"
		}
		fmt.Printf("  %s  %s  %s\n", s.CreatedAt, s.SessionID, name)
	}

	if dumpDir == "" {
		return nil
	}
	if err := os.MkdirAll(dumpDir, 0o755); err != nil {
		return err
	}
	for _, s := range all {
		raw, err := c.sessionDetail(ctx, s.SessionID)
		if err != nil {
			return fmt.Errorf("Session %s: %w", s.SessionID, err)
		}
		raw = prettyJSON(raw)
		path := filepath.Join(dumpDir, "session-"+sanitize(s.SessionID)+".json")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("Details gesichert in %s/\n", dumpDir)
	return nil
}

func cmdDecode(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("Aufruf: owgctl decode <hex> — z. B. a33a...c3")
	}
	// Hex-String einsammeln, Leerzeichen und übliche Trenner ignorieren.
	raw, err := parseHexFrame(strings.Join(args, ""))
	if err != nil {
		return fmt.Errorf("kein gültiges Hex: %w", err)
	}

	d, err := DecodeLegacyFrame(raw)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// sanitize entfernt Pfad-gefährliche Zeichen aus einem Datei-Namensteil.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}

// prettyJSON versucht, den Body einzurücken; klappt das nicht, bleibt er roh.
func prettyJSON(raw []byte) []byte {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return raw
	}
	return buf.Bytes()
}
