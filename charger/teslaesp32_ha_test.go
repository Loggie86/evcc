package charger

import (
	"errors"
	"fmt"
	"testing"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/util/homeassistant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type teslaESP32MockCall struct {
	domain, service string
	data            map[string]any
}
type teslaESP32MockHA struct {
	states  map[string]string
	numbers map[string]float64
	calls   []teslaESP32MockCall
	err     error
}

func (m *teslaESP32MockHA) GetBoolState(entity string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	switch m.states[entity] {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, errors.New("unavailable boolean")
	}
}
func (m *teslaESP32MockHA) GetFloatState(entity string) (float64, error) {
	if m.err != nil {
		return 0, m.err
	}
	value, ok := m.numbers[entity]
	if !ok {
		return 0, errors.New("unavailable numeric")
	}
	return value, nil
}
func (m *teslaESP32MockHA) GetState(entity string) (homeassistant.StateResponse, error) {
	if m.err != nil {
		return homeassistant.StateResponse{}, m.err
	}
	value, ok := m.states[entity]
	if !ok {
		return homeassistant.StateResponse{}, errors.New("unavailable state")
	}
	return homeassistant.StateResponse{State: value}, nil
}
func (m *teslaESP32MockHA) CallService(domain, service string, data map[string]any) error {
	if m.err != nil {
		return m.err
	}
	m.calls = append(m.calls, teslaESP32MockCall{domain, service, data})
	return nil
}

func newMockTeslaESP32HA() (*TeslaESP32HA, *teslaESP32MockHA) {
	m := &teslaESP32MockHA{
		states: map[string]string{
			"binary_sensor.connected": "off",
			"binary_sensor.contactor": "off",
			"input_select.owner":      "evcc",
			"input_boolean.armed":     "on",
		},
		numbers: map[string]float64{"sensor.current": 0, "sensor.power": 0, "sensor.energy": 20},
	}
	c := &TeslaESP32HA{
		conn: m, connected: "binary_sensor.connected",
		contactor: "binary_sensor.contactor", current: "sensor.current",
		power: "sensor.power", energy: "sensor.energy",
		owner: "input_select.owner", ownerValue: "evcc",
		request: "input_text.lease", armed: "input_boolean.armed", control: true,
	}
	return c, m
}

func TestTeslaESP32Tiers(t *testing.T) {
	for _, tc := range []struct {
		in, want int64
		valid    bool
	}{
		{0, 0, false}, {6, 0, false}, {7, 0, false},
		{8, 8, true}, {9, 8, true}, {15, 8, true},
		{16, 16, true}, {17, 17, true}, {18, 18, true},
		{19, 19, true}, {20, 20, true}, {21, 21, true},
		{22, 22, true}, {23, 23, true}, {24, 24, true},
		{25, 24, true}, {31, 24, true}, {32, 32, true},
		{33, 0, false}, {45, 0, false},
	} {
		actual, err := teslaESP32Tier(tc.in)
		if tc.valid {
			require.NoError(t, err)
			assert.Equal(t, tc.want, actual)
		} else {
			require.Error(t, err)
			assert.Equal(t, int64(0), actual)
		}
	}
}

func TestTeslaESP32States(t *testing.T) {
	c, m := newMockTeslaESP32HA()
	status, err := c.Status()
	require.NoError(t, err)
	assert.Equal(t, api.StatusA, status)
	m.states[c.connected] = "on"
	status, err = c.Status()
	require.NoError(t, err)
	assert.Equal(t, api.StatusB, status)
	m.states[c.contactor] = "on"
	m.numbers[c.current] = 5.4
	status, err = c.Status()
	require.NoError(t, err)
	assert.Equal(t, api.StatusB, status)
	m.numbers[c.current] = 5.5
	status, err = c.Status()
	require.NoError(t, err)
	assert.Equal(t, api.StatusC, status)
	m.err = errors.New("HA disconnected")
	_, err = c.Status()
	assert.Error(t, err)
	m.err = nil
	power, err := c.CurrentPower()
	require.NoError(t, err)
	assert.Equal(t, float64(0), power)
	energy, err := c.TotalEnergy()
	require.NoError(t, err)
	assert.Equal(t, float64(20), energy)
	min, max, err := c.GetMinMaxCurrent()
	require.NoError(t, err)
	assert.Equal(t, float64(8), min)
	assert.Equal(t, float64(32), max)
}

func TestTeslaESP32CommandsAndOff(t *testing.T) {
	c, m := newMockTeslaESP32HA()
	// No physical control exists by merely constructing the adapter.
	assert.Empty(t, m.calls)
	enabled, err := c.Enabled()
	require.NoError(t, err)
	assert.False(t, enabled)

	require.NoError(t, c.Enable(true))
	require.Len(t, m.calls, 1)
	assert.Equal(t, "input_text", m.calls[0].domain)
	assert.Equal(t, "set_value", m.calls[0].service)
	assert.Equal(t, "input_text.lease", m.calls[0].data["entity_id"])
	assert.Contains(t, m.calls[0].data["value"], `"amps":0`)

	require.NoError(t, c.MaxCurrent(15))
	require.Len(t, m.calls, 2)
	assert.Contains(t, m.calls[1].data["value"], `"amps":8`)
	enabled, err = c.Enabled()
	require.NoError(t, err)
	assert.True(t, enabled)

	require.NoError(t, c.MaxCurrent(32))
	assert.Contains(t, m.calls[2].data["value"], `"amps":32`)
	require.Error(t, c.MaxCurrent(6))
	require.Len(t, m.calls, 3, "invalid current must not be forwarded")

	require.NoError(t, c.Enable(false))
	assert.Contains(t, m.calls[3].data["value"], `"amps":0`)
	enabled, err = c.Enabled()
	require.NoError(t, err)
	assert.False(t, enabled)

	// A restart may not reuse a previous charging current.
	require.NoError(t, c.Enable(true))
	require.Len(t, m.calls, 5)
	assert.Contains(t, m.calls[4].data["value"], `"amps":0`)
}

func TestTeslaESP32ReadOnlyAndOwnerGate(t *testing.T) {
	c, m := newMockTeslaESP32HA()
	c.control = false
	require.ErrorContains(t, c.Enable(true), "read-only")
	require.ErrorContains(t, c.MaxCurrent(16), "read-only")
	assert.Empty(t, m.calls)
	enabled, err := c.Enabled()
	require.NoError(t, err)
	assert.False(t, enabled)

	c.control = true
	m.states[c.owner] = "homeassistant"
	require.ErrorContains(t, c.Enable(true), "not")
	assert.Empty(t, m.calls, "no write without explicit evcc authority")
	enabled, _ = c.Enabled()
	assert.False(t, enabled)
}

func TestTeslaESP32NoRenewalWithNoTarget(t *testing.T) {
	c, m := newMockTeslaESP32HA()
	require.NoError(t, c.Enable(true))
	require.Len(t, m.calls, 1)
	c.mu.Lock()
	assert.Equal(t, int64(0), c.currentA)
	c.mu.Unlock()
}

func TestTeslaESP32ControlConfigMustHaveOwnerAndHelper(t *testing.T) {
	_, err := NewTeslaESP32HAFromConfig(t.Context(), map[string]any{
		"uri":       "http://127.0.0.1:8123",
		"connected": "binary_sensor.connected",
		"contactor": "binary_sensor.contactor",
		"current":   "sensor.current",
		"control":   true,
	})
	assert.ErrorContains(t, err, "owner")
	_, err = NewTeslaESP32HAFromConfig(t.Context(), map[string]any{
		"uri":        "http://127.0.0.1:8123",
		"connected":  "binary_sensor.connected",
		"contactor":  "binary_sensor.contactor",
		"current":    "sensor.current",
		"control":    true,
		"owner":      "input_select.owner",
		"ownerValue": "evcc",
		"armed":      "input_boolean.armed",
		"request":    "sensor.alphaess_ev_command",
	})
	assert.ErrorContains(t, err, "input_text")
}

func TestTeslaESP32RequiresIndependentArming(t *testing.T) {
	c, m := newMockTeslaESP32HA()
	m.states["input_boolean.armed"] = "off"
	require.ErrorContains(t, c.Enable(true), "not armed")
	assert.Empty(t, m.calls)
	m.states["input_boolean.armed"] = "on"
	require.NoError(t, c.Enable(true))
	require.Len(t, m.calls, 1)
	m.states["input_boolean.armed"] = "off"
	require.ErrorContains(t, c.MaxCurrent(24), "not armed")
	require.Len(t, m.calls, 1)
}

// Every newly supported amp must reach HA unchanged.
func TestTeslaESP32OneAmpResolution16To24(t *testing.T) {
	c, m := newMockTeslaESP32HA()
	require.NoError(t, c.Enable(true))
	for amps := int64(16); amps <= 24; amps++ {
		require.NoError(t, c.MaxCurrent(amps))
		payload := m.calls[len(m.calls)-1].data["value"]
		assert.Contains(t, payload, fmt.Sprintf(`"amps":%d`, amps))
	}
}

func TestTeslaESP32StagedCurrentBeforeEnable(t *testing.T) {
	c, m := newMockTeslaESP32HA()
	require.NoError(t, c.MaxCurrent(17))
	assert.Empty(t, m.calls, "staging while disabled must not command HA")
	require.NoError(t, c.Enable(true))
	require.Len(t, m.calls, 2)
	assert.Contains(t, m.calls[0].data["value"], `"amps":0`)
	assert.Contains(t, m.calls[1].data["value"], `"amps":17`)
}

func TestTeslaESP32SinglePhase(t *testing.T) {
	charger, err := NewTeslaESP32HAFromConfig(t.Context(), map[string]any{
		"uri":       "http://127.0.0.1:8123",
		"connected": "binary_sensor.connected",
		"contactor": "binary_sensor.contactor",
		"current":   "sensor.current",
		"control":   false,
	})
	require.NoError(t, err)
	phase, ok := api.Cap[api.PhaseGetter](charger)
	require.True(t, ok)
	n, err := phase.GetPhases()
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	desc, ok := api.Cap[api.PhaseDescriber](charger)
	require.True(t, ok)
	assert.Equal(t, 1, desc.Phases())
}
