package core

import (
	"testing"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/core/types"
	"github.com/evcc-io/evcc/util"
	"github.com/evcc-io/evcc/util/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func solarTargetTestRates(now time.Time, watts float64, hours int) api.Rates {
	r := make(api.Rates, 0, hours*4+2)
	for i := 0; i <= hours*4+1; i++ {
		ts := now.Add(time.Duration(i) * 15 * time.Minute)
		value := watts
		if i >= hours*4 {
			value = 0
		}
		r = append(r, api.Rate{Start: ts, End: ts.Add(15 * time.Minute), Value: value})
	}
	return r
}

func TestBatterySolarTargetForecast(t *testing.T) {
	now := time.Date(2026, 11, 19, 11, 0, 0, 0, time.FixedZone("AWST", 8*3600))
	solar := solarTargetTestRates(now, 8500, 6)
	plan := planBatterySolarTarget(now, 100, 50, 27.9, 1500, solar)
	assert.Equal(t, "forecast", plan.Status)
	assert.InDelta(t, 13950, plan.EnergyNeededWh, 0.01)
	require.Greater(t, plan.ReserveW, 1000.0)
	require.Less(t, plan.ReserveW, 8500.0)
	assert.True(t, plan.SolarEnd.After(now.Add(5*time.Hour)))
}
func TestBatterySolarTargetShortfall(t *testing.T) {
	now := time.Date(2026, 11, 19, 15, 0, 0, 0, time.Local)
	plan := planBatterySolarTarget(now, 100, 25, 27.9, 1600, solarTargetTestRates(now, 1800, 2))
	assert.Equal(t, "shortfall", plan.Status)
	assert.Equal(t, 12000.0, plan.ReserveW)
}
func TestBatterySolarTargetNoForecast(t *testing.T) {
	p := planBatterySolarTarget(time.Now(), 100, 40, 27.9, 1000, nil)
	assert.Equal(t, "no-forecast", p.Status)
	assert.Equal(t, 12000.0, p.ReserveW)
}
func TestBatterySolarTargetReached(t *testing.T) {
	p := planBatterySolarTarget(time.Now(), 90, 91, 27.9, 1200, nil)
	assert.Equal(t, "reached", p.Status)
}
func TestBatterySolarTargetNextDayNotUsed(t *testing.T) {
	now := time.Date(2026, 11, 19, 19, 0, 0, 0, time.FixedZone("AWST", 8*3600))
	nextDay := now.Add(12 * time.Hour)
	p := planBatterySolarTarget(now, 100, 50, 27.9, 1000, solarTargetTestRates(nextDay, 8000, 5))
	assert.Equal(t, "solar-ended", p.Status)
}
func TestBatterySolarTargetDisabled(t *testing.T) {
	for _, target := range []float64{0, 30, 120} {
		p := planBatterySolarTarget(time.Now(), target, 40, 27.9, 1000, nil)
		assert.Equal(t, "disabled", p.Status)
	}
}

func TestBatterySolarTargetSitePowerProtectsBattery(t *testing.T) {
	// When forecast is absent, target mode must protect all home-battery
	// charging power. In Off mode, original site behaviour is unchanged.
	site := &Site{
		log: util.NewLogger("battery-solar-test"),
		batteryMeters: []config.Device[api.Meter]{
			config.NewStaticDevice(config.Named{}, api.Meter(api.NewMockMeter(gomock.NewController(t)))),
		},
		batterySolarTargetSoc: 100,
	}
	state := siteState{battery: types.BatteryState{Soc: 50, Capacity: 27.9, Power: -3000}}
	protected := site.sitePower(state, 0, 0)
	assert.InDelta(t, 100, protected.power, 0.001)
	assert.True(t, protected.batterySolarTargetActive)
	site.batterySolarTargetSoc = 0
	ordinary := site.sitePower(state, 0, 0)
	assert.InDelta(t, -3000, ordinary.power, 0.001)
	assert.False(t, ordinary.batterySolarTargetActive)
	site.batterySolarTargetSoc = 100
	state.battery.Soc = 100
	reached := site.sitePower(state, 0, 0)
	assert.InDelta(t, -3000, reached.power, 0.001)
}
