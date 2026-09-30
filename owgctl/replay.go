package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// cmdReplay speist gespeicherte Hex-Frames (eine Zeile je Frame) durch denselben
// Decoder und dieselbe Sink wie der Live-Betrieb. So lässt sich der komplette
// Pfad bis Home Assistant OHNE Grill testen — z. B. gegen einen lokalen
// mosquitto-Broker mit --mqtt.
//
// Aufruf: owgctl replay <datei|-> [--mqtt tcp://host:1883] [--delay ms] [--loop]
func cmdReplay(ctx context.Context, opt options) error {
	if len(opt.rest) == 0 {
		return fmt.Errorf("Aufruf: owgctl replay <datei|-> [--mqtt …] [--delay ms] [--loop]")
	}
	frames, err := loadFrames(opt.rest[0])
	if err != nil {
		return err
	}
	if len(frames) == 0 {
		return fmt.Errorf("keine Frames in der Eingabe")
	}

	sk, err := makeSink(opt)
	if err != nil {
		return err
	}
	defer sk.close()

	delay := 500 * time.Millisecond
	if opt.delayMs > 0 {
		delay = time.Duration(opt.delayMs) * time.Millisecond
	}

	for {
		for _, raw := range frames {
			d, err := DecodeLegacyFrame(raw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Frame verworfen (%v): %x\n", err, raw)
				continue
			}
			emit(sk, d.Serial, d.Flat())
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
			}
		}
		if !opt.loop {
			return nil
		}
	}
}

// loadFrames liest Hex-Frames aus einer Datei oder von stdin ("-").
// Leere Zeilen und Kommentare (#) werden übersprungen.
func loadFrames(path string) ([][]byte, error) {
	var r *bufio.Scanner
	if path == "-" {
		r = bufio.NewScanner(os.Stdin)
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = bufio.NewScanner(f)
	}
	var out [][]byte
	line := 0
	for r.Scan() {
		line++
		s := strings.TrimSpace(r.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		raw, err := parseHexFrame(s)
		if err != nil {
			return nil, fmt.Errorf("Zeile %d: kein gültiges Hex: %w", line, err)
		}
		out = append(out, raw)
	}
	return out, r.Err()
}
