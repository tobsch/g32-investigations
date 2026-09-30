package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://mobile-api.ottowildeapp.com"

// client spricht die Otto-Wilde-Mobile-API. Er meldet sich mit dem eigenen
// Konto an und hält das Access-Token für Folgeanfragen im Speicher.
type client struct {
	http     *http.Client
	baseURL  string
	language string
	token    string // Access-Token, nur im Speicher, wird nie ausgegeben
}

func newClient(cfg config) *client {
	return &client{
		http:     &http.Client{Timeout: 30 * time.Second},
		baseURL:  strings.TrimRight(cfg.baseURL, "/"),
		language: cfg.language,
	}
}

// apiError bildet die Fehler-Hülle der API ab ({message, errorCode}).
type apiError struct {
	Status  int
	Message string `json:"message"`
	Code    int    `json:"errorCode"`
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s (Code %d)", e.Status, e.Message, e.Code)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// setHeaders setzt die Standard-Header, die auch die App sendet, plus das
// Authorization-Token (roh, ohne "Bearer"), sofern vorhanden.
func (c *client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", c.language)
	req.Header.Set("UA-Platform", "Android")
	req.Header.Set("UA-Platform-Version", "14")
	req.Header.Set("UA-Model", "owgctl")
	if c.token != "" {
		req.Header.Set("Authorization", c.token)
	}
}

// login meldet sich mit E-Mail und Passwort an und merkt sich das Token.
// Das Passwort verlässt diese Funktion nur im Request-Body an den eigenen Server.
func (c *client) login(ctx context.Context, email, password string) error {
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)

	raw, err := c.send(req)
	if err != nil {
		return err
	}
	var out struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("Login-Antwort lesen: %w", err)
	}
	if out.Data.AccessToken == "" {
		return fmt.Errorf("Login: kein Token in der Antwort")
	}
	c.token = out.Data.AccessToken
	return nil
}

// get holt einen JSON-Endpunkt und liefert den rohen Body. GET ist idempotent,
// daher wird bei Netzfehlern und 429/5xx mehrmals mit Backoff wiederholt.
func (c *client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	const attempts = 3
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(i) * 750 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		c.setHeaders(req)
		raw, err := c.send(req)
		if err == nil {
			return raw, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// retryable erkennt vorübergehende Fehler (Netz, 429, 5xx).
func retryable(err error) bool {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.Status == 429 || apiErr.Status >= 500
	}
	return true // Netzwerk-/Transportfehler
}

// send führt die Anfrage aus und übersetzt Nicht-2xx-Antworten in apiError.
func (c *client) send(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &apiError{Status: resp.StatusCode}
		_ = json.Unmarshal(raw, apiErr) // Body ist evtl. kein JSON
		return nil, apiErr
	}
	return raw, nil
}

// grill spiegelt die für uns relevanten Felder von GrillModel. popKey wird
// bewusst nicht ausgelesen (gerätespezifisches Geheimnis).
type grill struct {
	SerialNumber            string `json:"serialNumber"`
	Nickname                string `json:"nickname"`
	FirmwareSemanticVersion string `json:"firmwareSemanticVersion"`
	FirmwareVersionCode     int    `json:"firmwareVersionCode"`
	IsWifiConnected         bool   `json:"isWifiConnected"`
	Bluetooth               struct {
		Name string `json:"bluetoothName"`
	} `json:"bluetoothConnectionInfo"`
}

// grills listet die Grills des angemeldeten Kontos.
func (c *client) grills(ctx context.Context) ([]grill, error) {
	raw, err := c.get(ctx, "/v2/grills", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []grill `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("Grill-Liste lesen: %w", err)
	}
	return out.Data, nil
}

// flexString nimmt sowohl JSON-Strings als auch Zahlen (und null) an. Die API
// liefert z. B. die Firmware-ID mal als Zahl, mal als Text.
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		*s = ""
		return nil
	}
	if len(b) >= 2 && b[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*s = flexString(str)
		return nil
	}
	*s = flexString(b) // Zahl o. Ä. wörtlich übernehmen
	return nil
}

// firmware beschreibt einen Firmware-Eintrag zur eigenen Seriennummer.
type firmware struct {
	ID              flexString      `json:"id"`
	Filename        string          `json:"filename"`
	VersionNumber   json.RawMessage `json:"versionNumber"`
	SemanticVersion string          `json:"semanticVersion"`
	ReleaseNotes    string          `json:"releaseNotes"`
}

// version gibt die Versionsnummer als String, egal ob die API Zahl oder Text liefert.
func (f firmware) version() string {
	s := strings.Trim(string(f.VersionNumber), `"`)
	if s == "" || s == "null" {
		return f.SemanticVersion
	}
	return s
}

// firmwareFor holt die Firmware-Metadaten zur Seriennummer des eigenen Grills.
func (c *client) firmwareFor(ctx context.Context, serial string) (firmware, error) {
	q := url.Values{"serialNumber": {serial}}
	raw, err := c.get(ctx, "/firmware", q)
	if err != nil {
		return firmware{}, err
	}
	var out struct {
		Data firmware `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return firmware{}, fmt.Errorf("Firmware-Metadaten lesen: %w", err)
	}
	return out.Data, nil
}

// downloadFirmware lädt die Firmware-Binärdatei zur gegebenen ID und schreibt
// sie in w. Zurück kommt die Anzahl geschriebener Bytes.
func (c *client) downloadFirmware(ctx context.Context, id string, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/firmware/%s/download", c.baseURL, url.PathEscape(id)), nil)
	if err != nil {
		return 0, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		apiErr := &apiError{Status: resp.StatusCode}
		_ = json.Unmarshal(raw, apiErr)
		return 0, apiErr
	}
	return io.Copy(w, resp.Body)
}

// sessionSummary ist ein Eintrag der Session-Liste.
type sessionSummary struct {
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	CreatedAt   string `json:"createdAt"`
	RecipeID    string `json:"recipeId"`
}

// sessionsPage holt eine Seite der Session-Liste zur Seriennummer.
func (c *client) sessionsPage(ctx context.Context, serial string, page, size int) ([]sessionSummary, error) {
	q := url.Values{
		"serialNumber": {serial},
		"paging":       {"true"},
		"page":         {fmt.Sprint(page)},
		"size":         {fmt.Sprint(size)},
	}
	raw, err := c.get(ctx, "/grill-analytics/sessions", q)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []sessionSummary `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("Session-Liste lesen: %w", err)
	}
	return out.Data, nil
}

// sessionDetail liefert den rohen JSON-Body einer einzelnen Session (inkl. Messreihe).
func (c *client) sessionDetail(ctx context.Context, sessionID string) ([]byte, error) {
	return c.get(ctx, "/grill-analytics/sessions/"+url.PathEscape(sessionID), nil)
}
