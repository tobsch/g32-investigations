package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// cmdExport sichert in einem Rutsch alles, was die Cloud noch hergibt:
// die Grill-Liste, je Grill die Firmware-Datei und die komplette
// Session-Historie. Gedacht als "einmal alles retten", solange der Server lebt.
func cmdExport(ctx context.Context, c *client, outDir string) error {
	if outDir == "" {
		outDir = "backup-" + time.Now().Format("2006-01-02")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	grills, err := c.grills(ctx)
	if err != nil {
		return fmt.Errorf("Grills laden: %w", err)
	}
	if err := writeJSON(filepath.Join(outDir, "grills.json"), grills); err != nil {
		return err
	}
	fmt.Printf("%d Grill(s) → %s/grills.json\n", len(grills), outDir)

	for _, g := range grills {
		serial := g.SerialNumber
		if serial == "" {
			continue
		}
		fmt.Printf("== Grill %s (%s)\n", serial, g.Nickname)
		if err := exportFirmware(ctx, c, outDir, serial); err != nil {
			fmt.Fprintf(os.Stderr, "  Firmware %s: %v\n", serial, err)
		}
		if err := exportSessions(ctx, c, outDir, serial); err != nil {
			fmt.Fprintf(os.Stderr, "  Sessions %s: %v\n", serial, err)
		}
	}
	fmt.Printf("Fertig. Sicherung in %s/\n", outDir)
	return nil
}

func exportFirmware(ctx context.Context, c *client, outDir, serial string) error {
	fw, err := c.firmwareFor(ctx, serial)
	if err != nil {
		return err
	}
	if fw.ID == "" {
		return fmt.Errorf("keine Firmware-ID")
	}
	dir := filepath.Join(outDir, "firmware")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Metadaten mitsichern.
	_ = writeJSON(filepath.Join(dir, serial+"-firmware.json"), fw)

	path := filepath.Join(dir, fmt.Sprintf("%s-update-%s.bin", serial, sanitize(fw.version())))
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
	fmt.Printf("  Firmware %s → %s (%d Bytes)\n", fw.version(), filepath.Base(path), n)
	return nil
}

func exportSessions(ctx context.Context, c *client, outDir, serial string) error {
	const size = 20
	dir := filepath.Join(outDir, "sessions", sanitize(serial))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
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
	if err := writeJSON(filepath.Join(dir, "_index.json"), all); err != nil {
		return err
	}
	saved := 0
	for _, s := range all {
		raw, err := c.sessionDetail(ctx, s.SessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Session %s: %v\n", s.SessionID, err)
			continue
		}
		path := filepath.Join(dir, "session-"+sanitize(s.SessionID)+".json")
		if err := os.WriteFile(path, prettyJSON(raw), 0o644); err != nil {
			return err
		}
		saved++
	}
	fmt.Printf("  %d/%d Sessions gesichert → %s/\n", saved, len(all), dir)
	return nil
}

// writeJSON schreibt v hübsch formatiert als JSON-Datei.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
