package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// mqttSink veröffentlicht Messwerte per MQTT und meldet die Sensoren einmalig
// über Home Assistants MQTT-Discovery an. Damit erscheint der Grill in HA
// automatisch als Gerät, ganz ohne Hersteller-Cloud.
type mqttSink struct {
	client       mqtt.Client
	discoveryPfx string          // HA-Discovery-Präfix, i. d. R. "homeassistant"
	announced    map[string]bool // je Sensor-Key einmal Discovery senden
}

func newMQTTSink(broker string) (*mqttSink, error) {
	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(fmt.Sprintf("owgctl-%d", time.Now().Unix())).
		SetConnectTimeout(10 * time.Second)
	c := mqtt.NewClient(opts)
	tok := c.Connect()
	if !tok.WaitTimeout(10 * time.Second) {
		return nil, fmt.Errorf("MQTT-Verbindung zu %s: Timeout", broker)
	}
	if err := tok.Error(); err != nil {
		return nil, fmt.Errorf("MQTT-Verbindung zu %s: %w", broker, err)
	}
	return &mqttSink{client: c, discoveryPfx: "homeassistant", announced: map[string]bool{}}, nil
}

func (m *mqttSink) close() {
	m.client.Disconnect(500)
}

// stateTopic ist das gemeinsame Zustands-Topic für einen Grill.
func (m *mqttSink) stateTopic(serial string) string {
	return fmt.Sprintf("owgctl/%s/state", safeID(serial))
}

func (m *mqttSink) publish(serial string, flat map[string]any) error {
	if serial == "" {
		serial = "grill"
	}
	// Discovery je neuem Schlüssel einmal senden.
	for key := range flat {
		if m.announced[key] {
			continue
		}
		if err := m.announceSensor(serial, key); err != nil {
			return err
		}
		m.announced[key] = true
	}
	payload, err := json.Marshal(flat)
	if err != nil {
		return err
	}
	tok := m.client.Publish(m.stateTopic(serial), 0, true, payload)
	tok.WaitTimeout(5 * time.Second)
	return tok.Error()
}

// announceSensor schreibt die HA-Discovery-Konfiguration für einen Messwert.
func (m *mqttSink) announceSensor(serial, key string) error {
	comp, unit, devClass := sensorMeta(key)
	uid := fmt.Sprintf("owg_%s_%s", safeID(serial), key)
	cfg := map[string]any{
		"name":        key,
		"unique_id":   uid,
		"object_id":   uid,
		"state_topic": m.stateTopic(serial),
		"device": map[string]any{
			"identifiers":  []string{"owg_" + safeID(serial)},
			"name":         "Otto Wilde G32 " + serial,
			"manufacturer": "Otto Wilde (lokal via owgctl)",
			"model":        "G32 Connected",
		},
	}
	if comp == "binary_sensor" {
		cfg["value_template"] = fmt.Sprintf("{{ 'ON' if value_json.%s else 'OFF' }}", key)
		cfg["payload_on"] = "ON"
		cfg["payload_off"] = "OFF"
	} else {
		cfg["value_template"] = fmt.Sprintf("{{ value_json.%s }}", key)
	}
	if unit != "" {
		cfg["unit_of_measurement"] = unit
	}
	if devClass != "" {
		cfg["device_class"] = devClass
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	topic := fmt.Sprintf("%s/%s/owg_%s/%s/config", m.discoveryPfx, comp, safeID(serial), key)
	tok := m.client.Publish(topic, 0, true, payload)
	tok.WaitTimeout(5 * time.Second)
	return tok.Error()
}

// sensorMeta leitet aus dem Schlüsselnamen HA-Komponente, Einheit und
// device_class ab.
func sensorMeta(key string) (component, unit, deviceClass string) {
	switch {
	case strings.HasPrefix(key, "zone"), strings.HasPrefix(key, "probe"):
		return "sensor", "°C", "temperature"
	case key == "gas_percent", key == "light_threshold_percent":
		return "sensor", "%", ""
	case key == "gas_stock_g", key == "weight_g":
		return "sensor", "g", "weight"
	case key == "signal_dbm":
		return "sensor", "dBm", "signal_strength"
	case strings.HasSuffix(key, "_open"), strings.HasSuffix(key, "_on"),
		strings.HasSuffix(key, "_active"), strings.HasSuffix(key, "_enabled"),
		strings.HasSuffix(key, "_connected"):
		return "binary_sensor", "", ""
	default:
		return "sensor", "", ""
	}
}

// safeID macht aus einem String einen MQTT-/HA-tauglichen Bezeichner.
func safeID(s string) string {
	if s == "" {
		return "grill"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}
