package plugin

import (
	"context"
	"errors"
	"strconv"

	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/homeassistant"
)

// HomeAssistant reads and writes Home Assistant entity states
type HomeAssistant struct {
	conn        *homeassistant.Connection
	entity      string
	attribute   string
	service     string
	valueField  string
	serviceData map[string]any
}

func init() {
	registry.AddCtx("homeassistant", NewHomeAssistantPluginFromConfig)
}

// NewHomeAssistantPluginFromConfig creates a Home Assistant plugin
func NewHomeAssistantPluginFromConfig(ctx context.Context, other map[string]any) (Plugin, error) {
	var cc struct {
		homeassistant.Config `mapstructure:",squash"`
		Entity               string
		Attribute            string
		Service              string
		ValueField           string
		ServiceData          map[string]any
	}

	if err := util.DecodeOther(other, &cc); err != nil {
		return nil, err
	}

	if cc.Entity == "" {
		return nil, errors.New("missing entity")
	}

	if err := validateHomeAssistantSignalConfig(cc.Entity, cc.Attribute, cc.Service, cc.ValueField, cc.ServiceData); err != nil {
		return nil, err
	}

	conn, err := cc.Config.NewConnection(util.NewLogger("homeassistant"))
	if err != nil {
		return nil, err
	}

	return &HomeAssistant{
		conn:        conn,
		entity:      cc.Entity,
		attribute:   cc.Attribute,
		service:     cc.Service,
		valueField:  cc.ValueField,
		serviceData: cc.ServiceData,
	}, nil
}

var _ StringGetter = (*HomeAssistant)(nil)

// StringGetter returns the entity state
func (p *HomeAssistant) StringGetter() (func() (string, error), error) {
	return p.readString, nil
}

var _ FloatGetter = (*HomeAssistant)(nil)

// FloatGetter returns the entity state as float
func (p *HomeAssistant) FloatGetter() (func() (float64, error), error) {
	return func() (float64, error) {
		if p.attribute == "" {
			return p.conn.GetFloatState(p.entity)
		}
		value, err := p.readString()
		if err != nil {
			return 0, err
		}
		return strconv.ParseFloat(value, 64)
	}, nil
}

var _ IntGetter = (*HomeAssistant)(nil)

// IntGetter returns the entity state as int
func (p *HomeAssistant) IntGetter() (func() (int64, error), error) {
	return func() (int64, error) {
		if p.attribute == "" {
			return p.conn.GetIntState(p.entity)
		}
		value, err := p.readString()
		if err != nil {
			return 0, err
		}
		return strconv.ParseInt(value, 10, 64)
	}, nil
}

var _ BoolGetter = (*HomeAssistant)(nil)

// BoolGetter returns the entity state as bool
func (p *HomeAssistant) BoolGetter() (func() (bool, error), error) {
	return func() (bool, error) {
		if p.attribute == "" {
			return p.conn.GetBoolState(p.entity)
		}
		value, err := p.readString()
		if err != nil {
			return false, err
		}
		return parseHomeAssistantSignalBool(value)
	}, nil
}

var _ BoolSetter = (*HomeAssistant)(nil)

// BoolSetter invokes the entity's turn_on/turn_off service
func (p *HomeAssistant) BoolSetter(param string) (func(bool) error, error) {
	return func(enable bool) error {
		return p.write(enable)
	}, nil
}
