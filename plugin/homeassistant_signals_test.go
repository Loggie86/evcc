package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/homeassistant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func homeAssistantTestPlugin(t *testing.T, entity, service, field string, data map[string]any) (*HomeAssistant, *string, *map[string]any) {
	t.Helper()
	path := new(string)
	body := new(map[string]any)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*path = r.URL.Path
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(body))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	conn, err := homeassistant.NewConnection(util.NewLogger("test"), srv.URL, "", false)
	require.NoError(t, err)
	// The mocked HA API does not need OAuth; real connections do.
	conn.Client.Transport = http.DefaultTransport
	return &HomeAssistant{conn: conn, entity: entity, service: service, valueField: field, serviceData: data}, path, body
}

func TestHomeAssistantSignalWrites(t *testing.T) {
	t.Run("number", func(t *testing.T) {
		p, path, data := homeAssistantTestPlugin(t, "input_number.evcc_target", "", "", nil)
		set, err := p.FloatSetter("")
		require.NoError(t, err)
		require.NoError(t, set(16))
		assert.Equal(t, "/api/services/input_number/set_value", *path)
		assert.Equal(t, "input_number.evcc_target", (*data)["entity_id"])
		assert.Equal(t, float64(16), (*data)["value"])
	})
	t.Run("text", func(t *testing.T) {
		p, path, data := homeAssistantTestPlugin(t, "input_text.evcc_command", "", "", nil)
		set, err := p.StringSetter("")
		require.NoError(t, err)
		require.NoError(t, set("1234,8,1,32,240,0,0,0"))
		assert.Equal(t, "/api/services/input_text/set_value", *path)
		assert.Equal(t, "1234,8,1,32,240,0,0,0", (*data)["value"])
	})
	t.Run("select", func(t *testing.T) {
		p, path, data := homeAssistantTestPlugin(t, "select.ev_mode", "", "", nil)
		set, err := p.StringSetter("")
		require.NoError(t, err)
		require.NoError(t, set("Automatic"))
		assert.Equal(t, "/api/services/select/select_option", *path)
		assert.Equal(t, "Automatic", (*data)["option"])
	})
	t.Run("button", func(t *testing.T) {
		p, path, _ := homeAssistantTestPlugin(t, "button.evcc_refresh", "", "", nil)
		set, err := p.BoolSetter("")
		require.NoError(t, err)
		require.NoError(t, set(true))
		assert.Equal(t, "/api/services/button/press", *path)
		require.Error(t, set(false))
	})
	t.Run("generic service", func(t *testing.T) {
		p, path, data := homeAssistantTestPlugin(t, "climate.zone", "climate.set_temperature", "temperature", map[string]any{"hvac_mode": "cool"})
		set, err := p.FloatSetter("")
		require.NoError(t, err)
		require.NoError(t, set(23))
		assert.Equal(t, "/api/services/climate/set_temperature", *path)
		assert.Equal(t, "climate.zone", (*data)["entity_id"])
		assert.Equal(t, float64(23), (*data)["temperature"])
		assert.Equal(t, "cool", (*data)["hvac_mode"])
	})
	t.Run("trigger without value", func(t *testing.T) {
		p, path, data := homeAssistantTestPlugin(t, "script.refresh_car", "script.turn_on", "", nil)
		set, err := p.BoolSetter("")
		require.NoError(t, err)
		require.Error(t, set(false))
		assert.Empty(t, *path)
		require.NoError(t, set(true))
		assert.Equal(t, "/api/services/script/turn_on", *path)
		assert.Equal(t, "script.refresh_car", (*data)["entity_id"])
	})
}

func TestHomeAssistantSignalValidation(t *testing.T) {
	for _, tc := range []struct {
		entity, attribute, service, field string
		data                              map[string]any
	}{
		{entity: "sensor.nope/../../", service: "switch.turn_on"},
		{entity: "sensor.good", service: "switch.turn_on/../bad"},
		{entity: "sensor.good", service: "notify.send_message", field: "entity_id"},
		{entity: "sensor.good", service: "climate.set_temperature", field: "temperature", data: map[string]any{"entity_id": "climate.elsewhere"}},
		{entity: "sensor.good", service: "climate.set_temperature", field: "temperature", data: map[string]any{"temperature": 30}},
		{entity: "sensor.good", field: "value"},
		{entity: "sensor.good", attribute: "status/../foo"},
	} {
		assert.Error(t, validateHomeAssistantSignalConfig(tc.entity, tc.attribute, tc.service, tc.field, tc.data), tc)
	}
	assert.NoError(t, validateHomeAssistantSignalConfig("sensor.alphaess_ev_decision", "nested.current", "", "", nil))
	assert.NoError(t, validateHomeAssistantSignalConfig("input_number.evcc_target", "", "input_number.set_value", "value", nil))
}

func TestHomeAssistantUnsupportedDefaultSetter(t *testing.T) {
	p := &HomeAssistant{entity: "sensor.alphaess_ev_command"}
	_, err := p.StringSetter("")
	assert.ErrorContains(t, err, "no default Home Assistant string setter")
	_, err = p.IntSetter("")
	assert.ErrorContains(t, err, "no default Home Assistant integer setter")
	_, err = p.FloatSetter("")
	assert.ErrorContains(t, err, "no default Home Assistant number setter")
}
