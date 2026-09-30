package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// config hält die Werte aus dem .env-File bzw. der Umgebung. Das Passwort wird
// nur zum Login benutzt und nie ausgegeben oder geloggt.
type config struct {
	user     string
	password string
	serial   string
	baseURL  string
	language string
}

// loadConfig sucht ausgehend vom aktuellen Verzeichnis nach oben nach einer
// .env-Datei (sofern envPath leer ist), liest sie ein, ohne bereits gesetzte
// Umgebungsvariablen zu überschreiben, und baut daraus die config.
func loadConfig(envPath string) (config, error) {
	if envPath == "" {
		envPath = findDotEnv()
	}
	if envPath != "" {
		if err := loadDotEnv(envPath); err != nil {
			return config{}, err
		}
	}

	cfg := config{
		user:     os.Getenv("OW_USER"),
		password: os.Getenv("OW_PASS"),
		serial:   os.Getenv("OW_SERIAL"),
		baseURL:  os.Getenv("OW_BASE_URL"),
		language: os.Getenv("OW_LANGUAGE"),
	}
	if cfg.baseURL == "" {
		cfg.baseURL = defaultBaseURL
	}
	if cfg.language == "" {
		cfg.language = "de"
	}
	return cfg, nil
}

// requireCredentials stellt sicher, dass Benutzer und Passwort vorhanden sind.
func (c config) requireCredentials() error {
	if c.user == "" || c.password == "" {
		return fmt.Errorf("OW_USER und OW_PASS fehlen — bitte in .env eintragen")
	}
	return nil
}

// findDotEnv läuft vom Arbeitsverzeichnis nach oben und liefert den Pfad der
// ersten gefundenen .env-Datei, sonst einen leeren String.
func findDotEnv() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// loadDotEnv liest ein einfaches KEY=VALUE-File. Kommentare (#) und leere
// Zeilen werden übersprungen; umgebende Anführungszeichen werden entfernt.
// Bereits gesetzte Umgebungsvariablen bleiben unangetastet.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf(".env öffnen: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}
