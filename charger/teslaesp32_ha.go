package charger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/api/implement"
	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/homeassistant"
)

const (
	teslaESP32MinimumAmps = int64(8)
	teslaESP32MaximumAmps = int64(32)
	teslaESP32LeasePeriod = 4 * time.Second
)

// teslaESP32HAConnection lets the transport be mocked without calling live HA.
type teslaESP32HAConnection interface {
	GetBoolState(entity string) (bool, error)
	GetFloatState(entity string) (float64, error)
	GetState(entity string) (homeassistant.StateResponse, error)
	CallService(domain, service string, payload map[string]any) error
}

// TeslaESP32HA reads Tesla Wall Connector telemetry from Home Assistant and
// optionally submits discrete charging requests to a dedicated HA *input_text*
// helper. It NEVER writes the firmware's generated sensor.alphaess_ev_command:
// a separately commissioned HA relay must translate/authorize requests.
type TeslaESP32HA struct {
	*embed
	implement.Caps
	conn teslaESP32HAConnection

	connected, contactor, current, power, energy string
	owner, ownerValue, request, armed            string
	control                                      bool

	mu       sync.Mutex
	enabled  bool
	currentA int64
	stagedAt time.Time
	seq      int64
}

func init() {
	registry.AddCtx("tesla-esp32-ha", NewTeslaESP32HAFromConfig)
}

// NewTeslaESP32HAFromConfig builds the adapter, which defaults to read-only.
// No physical charger can be controlled unless all three gates are present:
//   - control: true in the evcc charger configuration
//   - HA owner entity matches ownerValue
//   - a separate HA relay has been deliberately commissioned
func NewTeslaESP32HAFromConfig(ctx context.Context, other map[string]any) (api.Charger, error) {
	var cc struct {
		embed                `mapstructure:",squash"`
		homeassistant.Config `mapstructure:",squash"`
		Connected            string
		Contactor            string
		Current              string
		Power                string
		Energy               string
		Owner                string
		OwnerValue           string
		Request              string
		Armed                string
		Control              bool
	}
	if err := util.DecodeOther(other, &cc); err != nil {
		return nil, err
	}
	if cc.Connected == "" || cc.Contactor == "" || cc.Current == "" {
		return nil, errors.New("Tesla ESP32 HA: connected, contactor and current entities are required")
	}
	if cc.Control {
		if cc.Owner == "" || cc.OwnerValue == "" || cc.Armed == "" {
			return nil, errors.New("Tesla ESP32 HA: owner, ownerValue and armed entity are required for control")
		}
		if !strings.HasPrefix(cc.Request, "input_text.") || len(cc.Request) <= len("input_text.") {
			return nil, errors.New("Tesla ESP32 HA: control request must be an input_text entity")
		}
	}
	conn, err := cc.Config.NewConnection(util.NewLogger("tesla-esp32-ha"))
	if err != nil {
		return nil, err
	}
	c := &TeslaESP32HA{
		embed:      &cc.embed,
		Caps:       implement.New(),
		conn:       conn,
		connected:  cc.Connected,
		contactor:  cc.Contactor,
		current:    cc.Current,
		power:      cc.Power,
		energy:     cc.Energy,
		owner:      cc.Owner,
		ownerValue: cc.OwnerValue,
		request:    cc.Request,
		armed:      cc.Armed,
		control:    cc.Control,
	}
	// This Tesla Wall Connector is physically wired as single-phase.
	// Without a PhaseGetter, evcc assumes three phases and calculates
	// incorrect solar power and grid headroom for this installation.
	implement.Has(c, implement.PhaseGetter(func() (int, error) {
		return 1, nil
	}))
	if cc.Power != "" {
		implement.Has(c, implement.Meter(c.CurrentPower))
	}
	if cc.Energy != "" {
		implement.Has(c, implement.MeterEnergy(c.TotalEnergy))
	}
	if cc.Control {
		go c.renewLease(ctx)
	}
	return c, nil
}

var _ api.Charger = (*TeslaESP32HA)(nil)
var _ api.CurrentLimiter = (*TeslaESP32HA)(nil)
var _ api.PhaseDescriber = (*TeslaESP32HA)(nil)

// Phases reports physically wired supply phases, which evcc uses for
// its power model. PhaseGetter alone does not inform that calculation.
func (*TeslaESP32HA) Phases() int { return 1 }

// Status is based on independently measured connection, contactor and vehicle
// current, not on the command helper or optimistic command acceptance.
func (c *TeslaESP32HA) Status() (api.ChargeStatus, error) {
	connected, err := c.conn.GetBoolState(c.connected)
	if err != nil {
		return api.StatusNone, err
	}
	if !connected {
		return api.StatusA, nil
	}
	contactor, err := c.conn.GetBoolState(c.contactor)
	if err != nil {
		return api.StatusNone, err
	}
	current, err := c.conn.GetFloatState(c.current)
	if err != nil {
		return api.StatusNone, err
	}
	if contactor && current >= 5.5 {
		return api.StatusC, nil
	}
	return api.StatusB, nil
}

// Enabled returns evcc's in-process request state. It is not an assertion
// that the physical charger is energized; Status() supplies that truth.
func (c *TeslaESP32HA) Enabled() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled && c.control, nil
}

// MaxCurrent supports each amp from 16 through 24 plus 8 A and 32 A.
// Unsupported intermediate requests round down to a commissioned level.
func (c *TeslaESP32HA) MaxCurrent(amps int64) error {
	tier, err := teslaESP32Tier(amps)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.control {
		return errors.New("Tesla ESP32 HA: read-only adapter; cannot set current")
	}
	if c.enabled {
		if err := c.publishLocked(tier); err != nil {
			return err
		}
	}
	c.currentA = tier
	c.stagedAt = time.Now()
	return nil
}

func teslaESP32Tier(amps int64) (int64, error) {
	if amps < teslaESP32MinimumAmps || amps > teslaESP32MaximumAmps {
		return 0, fmt.Errorf("Tesla ESP32 HA: current %d A outside supported 8–32 A range", amps)
	}
	switch {
	case amps < 16:
		return 8, nil
	case amps <= 24:
		return amps, nil
	case amps < 32:
		return 24, nil
	default:
		return 32, nil
	}
}

// Enable(true) cannot revive an old charging target: it publishes an explicit
// 0 A request before a fresh target. Enable(false) also publishes 0 A.
func (c *TeslaESP32HA) Enable(enable bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.control {
		return errors.New("Tesla ESP32 HA: read-only adapter; cannot enable charger")
	}
	if !enable {
		c.enabled = false
		c.currentA = 0
		c.stagedAt = time.Time{}
		return c.publishLocked(0)
	}
	if c.enabled {
		return nil
	}
	if err := c.publishLocked(0); err != nil {
		return err
	}
	c.enabled = true
	// evcc may set current before enabling. Accept only a tier staged
	// within this session, never a stale target from a prior session.
	if c.currentA >= teslaESP32MinimumAmps && !c.stagedAt.IsZero() &&
		time.Since(c.stagedAt) < 10*time.Second {
		if err := c.publishLocked(c.currentA); err != nil {
			c.enabled = false
			return err
		}
	} else {
		c.currentA = 0
	}
	return nil
}

// GetMinMaxCurrent tells evcc that its normal 6 A minimum is incompatible
// with this site's 8/16-24/32 A controller.
func (c *TeslaESP32HA) GetMinMaxCurrent() (float64, float64, error) {
	return float64(teslaESP32MinimumAmps), float64(teslaESP32MaximumAmps), nil
}

func (c *TeslaESP32HA) CurrentPower() (float64, error) {
	if c.power == "" {
		return 0, errors.New("Tesla ESP32 HA: no charger power sensor configured")
	}
	return c.conn.GetFloatState(c.power)
}

func (c *TeslaESP32HA) TotalEnergy() (float64, error) {
	if c.energy == "" {
		return 0, errors.New("Tesla ESP32 HA: no charger energy sensor configured")
	}
	return c.conn.GetFloatState(c.energy)
}

// publishLocked sends a compact timestamped lease request to a scoped HA
// input_text helper. A helper is NEVER the original ESP32 command sensor.
// HA is responsible for validating the owner and physical safety before
// translating this request into its existing 8-field ESP32 command.
func (c *TeslaESP32HA) publishLocked(amps int64) error {
	if !c.control {
		return errors.New("Tesla ESP32 HA: read-only")
	}
	owner, err := c.conn.GetState(c.owner)
	if err != nil {
		return fmt.Errorf("Tesla ESP32 HA: owner unavailable: %w", err)
	}
	if owner.State != c.ownerValue {
		return fmt.Errorf("Tesla ESP32 HA: owner %q is not %q", owner.State, c.ownerValue)
	}
	armed, err := c.conn.GetBoolState(c.armed)
	if err != nil || !armed {
		return errors.New("Tesla ESP32 HA: handover not armed")
	}
	now := time.Now()
	seq := now.UnixNano()
	if seq <= c.seq {
		seq = c.seq + 1
	}
	c.seq = seq
	payload := fmt.Sprintf(`{"seq":%d,"timestamp":%d,"amps":%d}`, seq, now.Unix(), amps)
	return c.conn.CallService("input_text", "set_value", map[string]any{
		"entity_id": c.request,
		"value":     payload,
	})
}

// renewLease sends the same requested tier every 4 seconds while enabled.
// A missing HA connection or owner gate stops renewal; a properly configured
// HA relay must stop physical charging after the 15-second lease expires.
func (c *TeslaESP32HA) renewLease(ctx context.Context) {
	ticker := time.NewTicker(teslaESP32LeasePeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			if c.enabled && c.currentA >= teslaESP32MinimumAmps {
				_ = c.publishLocked(c.currentA)
			}
			c.mu.Unlock()
		}
	}
}
