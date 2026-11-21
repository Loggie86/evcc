# Home Assistant signals in the Loggie86 evcc fork

The existing native `source: homeassistant` plugin now supports **read and write**.
This is a fork extension of evcc's existing Home Assistant OAuth integration,
not a second Home Assistant data cache and not a hardware firmware replacement.

## Authentication

Connect the Home Assistant instance via evcc's existing OAuth authorisation in
its web interface. Use the Home Assistant URI reachable from the evcc container
(e.g. `http://homeassistant.local:8123` if directly published there).
Do not put long-lived access tokens in GitHub, YAML or logs. The existing
SQLite database stores evcc authorisation state.

## Read any HA entity state

```yaml
source: homeassistant
uri: http://homeassistant.local:8123
entity: sensor.solar_power
```

Getter interfaces: string, float, int and bool (with their normal HA
conversions). Power and energy readings continue to use evcc's existing unit
scaling.

To read **any state attribute** (including nested attributes), add `attribute`:

```yaml
source: homeassistant
uri: http://homeassistant.local:8123
entity: sensor.ev_charging_decision
attribute: level_gate_reasons.16
```

Scalar attribute values return as text or numbers, and object/list attributes
return JSON text. A missing/unavailable state/attribute yields an error rather
than silently supplying zero.

## Write values (only explicitly configured targets)

Use the same plugin as an evcc *setter* inside a configured custom charger,
vehicle, or other integration:

| HA target domain | evcc setter value | HA call |
| --- | --- | --- |
| `switch`, `input_boolean`, `light` | boolean | `turn_on` / `turn_off` |
| `button`, `input_button` | boolean true | `press` |
| `number`, `input_number` | float or integer | `set_value` |
| `text`, `input_text` | string | `set_value` |
| `select`, `input_select` | string | `select_option` |

Example numeric setter:

```yaml
source: homeassistant
uri: http://homeassistant.local:8123
entity: input_number.charger_requested_amps
```

Example explicit service for an entity without a standard setter:

```yaml
source: homeassistant
uri: http://homeassistant.local:8123
entity: climate.zone
service: climate.set_temperature
valueField: temperature
serviceData:
  hvac_mode: cool
```

An evcc float setter will call the specified service with the entity bound
to `climate.zone`, plus `temperature` from evcc and
`hvac_mode: cool` as static context. The configuration validates
entity/service names and rejects overrides of the target `entity_id`.
A service with no `valueField` may only be triggered by boolean
`true` (for scripts, buttons, etc.).

**This feature does not provide an unrestricted Home Assistant write endpoint.**
Only explicitly configured evcc setter calls write anything; actual HA
permissions and available services remain authoritative.

## Existing Tesla ESP32 command path (next commissioning step)

Current firmware expects a *generated Home Assistant sensor*
`sensor.alphaess_ev_command` containing a timestamped,
8-field command. That sensor is read-only to clients; directly writing
`sensor.alphaess_ev_command` is not supported by HA.

For evcc to own charging decisions without changing ESP32 firmware,
configure a dedicated HA command ingress/relay, e.g. an input_text or
custom service, to accept evcc's requests **only after explicit cutover**,
validate 0/8/16/24/32 A, vehicle identity, 15-second lease, and freshness,
then publish the existing sensor command shape. The HA policy must be
disabled as a competing command authority at cutover, but its safety
fallback must be preserved independently. No such command ingress or
write-enabled evcc charger is installed yet.

## Notes

- Home Assistant's read-only sensors stay read-only; commands invoke services.
- Connection authorization is separate from this source-code feature.
- Tests use a local mock HA API only, never the live charger or house.
- Do not add real HA tokens, IP addresses exposed to the internet, or
  operational charger commands to the public repository.
