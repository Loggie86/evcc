package plugin

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// These checks keep an entity-bound write from becoming an arbitrary URL or
// unscoped service call. The target must be selected in evcc configuration.
var haEntityID = regexp.MustCompile(`^[a-z_][a-z0-9_]*\.[a-z0-9_]+$`)
var haServiceID = regexp.MustCompile(`^[a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*$`)
var haFieldID = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func validateHomeAssistantSignalConfig(entity, attribute, service, valueField string, data map[string]any) error {
	if !haEntityID.MatchString(entity) {
		return fmt.Errorf("invalid Home Assistant entity id: %s", entity)
	}
	for _, part := range strings.Split(attribute, ".") {
		if part != "" && !haFieldID.MatchString(part) {
			return fmt.Errorf("invalid attribute path: %s", attribute)
		}
	}
	if service == "" {
		if valueField != "" || len(data) != 0 {
			return errors.New("valueField and serviceData require an explicit service")
		}
		return nil
	}
	if !haServiceID.MatchString(service) {
		return fmt.Errorf("invalid Home Assistant service: %s", service)
	}
	if valueField != "" && (!haFieldID.MatchString(valueField) || valueField == "entity_id") {
		return fmt.Errorf("invalid service value field: %s", valueField)
	}
	if _, ok := data["entity_id"]; ok {
		return errors.New("serviceData must not override the configured entity_id")
	}
	if _, ok := data[valueField]; ok && valueField != "" {
		return errors.New("serviceData must not override the dynamic valueField")
	}
	return nil
}

func (p *HomeAssistant) readString() (string, error) {
	if p.attribute != "" {
		return p.conn.GetAttribute(p.entity, p.attribute)
	}
	res, err := p.conn.GetState(p.entity)
	return res.State, err
}

func parseHomeAssistantSignalBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "true", "1", "active", "yes":
		return true, nil
	case "off", "false", "0", "inactive", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid Home Assistant boolean: %q", v)
	}
}

func (p *HomeAssistant) domain() string {
	domain, _, _ := strings.Cut(p.entity, ".")
	return domain
}

func (p *HomeAssistant) write(value any) error {
	if p.service != "" {
		domain, service, _ := strings.Cut(p.service, ".")
		payload := make(map[string]any, len(p.serviceData)+2)
		for key, value := range p.serviceData {
			payload[key] = value
		}
		payload["entity_id"] = p.entity
		if p.valueField != "" {
			payload[p.valueField] = value
		} else if trigger, ok := value.(bool); !ok || !trigger {
			// A command without a data field is a trigger, not a switch.
			return errors.New("trigger service requires true")
		}
		return p.conn.CallService(domain, service, payload)
	}
	domain := p.domain()
	switch v := value.(type) {
	case string:
		switch domain {
		case "select", "input_select":
			return p.conn.CallSelectService(p.entity, v)
		case "text", "input_text":
			return p.conn.CallService(domain, "set_value", map[string]any{
				"entity_id": p.entity, "value": v,
			})
		}
	case int64:
		switch domain {
		case "number", "input_number":
			return p.conn.CallNumberService(p.entity, v)
		}
	case float64:
		switch domain {
		case "number", "input_number":
			return p.conn.CallNumberService(p.entity, v)
		}
	case bool:
		return p.conn.CallSwitchService(p.entity, v)
	}
	return fmt.Errorf("no default Home Assistant setter for %s and value type %T; configure service and valueField", p.entity, value)
}

var (
	_ StringSetter = (*HomeAssistant)(nil)
	_ FloatSetter  = (*HomeAssistant)(nil)
	_ IntSetter    = (*HomeAssistant)(nil)
)

// StringSetter sets text and select entities, or calls an explicitly
// configured HA service with the string supplied as valueField.
func (p *HomeAssistant) StringSetter(param string) (func(string) error, error) {
	if p.service == "" {
		switch p.domain() {
		case "select", "input_select", "text", "input_text":
		default:
			return nil, fmt.Errorf("no default Home Assistant string setter for %s", p.entity)
		}
	}
	if p.service != "" && p.valueField == "" {
		return nil, errors.New("string service setter requires valueField")
	}
	return func(value string) error { return p.write(value) }, nil
}

// FloatSetter sets number/input_number or an explicitly configured service.
func (p *HomeAssistant) FloatSetter(param string) (func(float64) error, error) {
	if p.service == "" && p.domain() != "number" && p.domain() != "input_number" {
		return nil, fmt.Errorf("no default Home Assistant number setter for %s", p.entity)
	}
	if p.service != "" && p.valueField == "" {
		return nil, errors.New("numeric service setter requires valueField")
	}
	return func(value float64) error { return p.write(value) }, nil
}

// IntSetter sets number/input_number or an explicitly configured service.
func (p *HomeAssistant) IntSetter(param string) (func(int64) error, error) {
	if p.service == "" && p.domain() != "number" && p.domain() != "input_number" {
		return nil, fmt.Errorf("no default Home Assistant integer setter for %s", p.entity)
	}
	if p.service != "" && p.valueField == "" {
		return nil, errors.New("numeric service setter requires valueField")
	}
	return func(value int64) error { return p.write(value) }, nil
}
