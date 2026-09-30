package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// cmdServe startet einen TCP-Server, der sich gegenüber dem eigenen Grill als
// Cloud-Socket ausgibt (Port 4502, Klartext-TCP wie das Original). Er nimmt die
// Verbindung an und PROTOKOLLIERT, was der Grill sendet — Ziel: die bisher nur
// grob bekannte Grill→Server-Richtung des Protokolls reversen, um den WLAN-Pfad
// später ohne Hersteller-Cloud nachbauen zu können.
//
// Voraussetzung im Einsatz: DNS-Umleitung von socket.ottowildeapp.com auf die
// IP dieses Rechners (Router/Loxone). Betrifft nur Tobis eigenes Gerät im
// eigenen Netz.
func cmdServe(ctx context.Context, opt options) error {
	port := 4502
	if opt.port > 0 {
		port = opt.port
	}
	addr := fmt.Sprintf(":%d", port)

	var logw io.Writer = os.Stdout
	if opt.raw != "" {
		f, err := os.OpenFile(opt.raw, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("Mitschnitt-Datei: %w", err)
		}
		defer f.Close()
		logw = io.MultiWriter(os.Stdout, f)
		fmt.Printf("Mitschnitt → %s\n", opt.raw)
	}

	// Optional: dekodierte Frames zusätzlich nach Home Assistant veröffentlichen.
	var sk sink
	if opt.mqtt != "" {
		ms, err := newMQTTSink(opt.mqtt)
		if err != nil {
			return err
		}
		defer ms.close()
		sk = ms
		fmt.Printf("MQTT: dekodierte Frames → %s (HA-Discovery aktiv)\n", opt.mqtt)
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("Listen auf %s: %w", addr, err)
	}
	defer ln.Close()
	go func() { <-ctx.Done(); ln.Close() }()

	fmt.Printf("Socket-Server auf %s — gibt sich als Cloud aus.\n", addr)
	fmt.Println("Nötig: DNS-Umleitung socket.ottowildeapp.com → diese IP.")
	fmt.Println("Warte auf den Grill (Strg-C beendet) ...")

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintln(os.Stderr, "Accept:", err)
			continue
		}
		go handleGrillConn(conn, logw, sk)
	}
}

func handleGrillConn(conn net.Conn, logw io.Writer, sk sink) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	logLine(logw, "== Verbindung von %s um %s", remote, time.Now().Format("15:04:05"))

	buf := make([]byte, 8192)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Minute))
		n, err := conn.Read(buf)
		if n > 0 {
			interpretGrillData(logw, remote, buf[:n], sk)
		}
		if err != nil {
			logLine(logw, "== %s getrennt: %v", remote, err)
			return
		}
	}
}

// interpretGrillData protokolliert einen empfangenen Chunk und versucht, ihn zu
// deuten (JSON, Legacy-Frame, TLS-Handshake oder rohe Bytes).
func interpretGrillData(logw io.Writer, remote string, data []byte, sk sink) {
	ts := time.Now().Format("15:04:05.000")
	logLine(logw, "[%s] %s  %d Byte  hex=%x", ts, remote, len(data), data)

	switch {
	case data[0] == '{':
		var v any
		if json.Unmarshal(data, &v) == nil {
			b, _ := json.MarshalIndent(v, "", "  ")
			logLine(logw, "    JSON:\n%s", b)
		} else {
			logLine(logw, "    (beginnt wie JSON, aber unvollständig/fragmentiert)")
		}
	case len(data) >= 3 && data[0] == frameHead0 && data[1] == frameHead1:
		if d, err := DecodeLegacyFrame(data); err == nil {
			b, _ := json.Marshal(d)
			logLine(logw, "    Legacy-Frame: %s", b)
			if sk != nil {
				emit(sk, d.Serial, d.Flat()) // dekodierten Frame nach HA
			}
		} else {
			logLine(logw, "    (Frame-Kopf A3 3A, aber Decode fehlgeschlagen: %v)", err)
		}
	case data[0] == 0x16 && len(data) > 2 && data[1] == 0x03:
		logLine(logw, "    ⚠ sieht nach TLS-Handshake aus — der Grill nutzt hier doch TLS.")
	default:
		logLine(logw, "    ASCII: %q", printableASCII(data))
	}
}

func logLine(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, format+"\n", a...)
}

// printableASCII ersetzt nicht-druckbare Bytes durch '.'.
func printableASCII(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		} else {
			sb.WriteByte('.')
		}
	}
	return sb.String()
}
