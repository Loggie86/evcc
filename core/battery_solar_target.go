package core

import (
	"math"
	"time"

	"github.com/evcc-io/evcc/api"
)

// batterySolarPlan is a best-effort forecast-driven home-battery allocation.
// This does not command the home battery or guarantee a charge level:
// weather, home loads, charge acceptance and solar generation may vary.
type batterySolarPlan struct {
	Status         string
	SolarEnd       time.Time
	ReserveW       float64
	EnergyNeededWh float64
	ForecastNetWh  float64
}

// planBatterySolarTarget reserves sufficient charging power for the home battery
// to approach the selected SoC by the end of useful forecast sunlight. The
// surplus above the reserve remains available for evcc Smart EV charging.
// Missing/unusable forecast is conservative: no charging battery power is
// offered to the EV, but true grid-export surplus is still usable.
func planBatterySolarTarget(now time.Time, targetSoc, soc, capacityKWh, homeLoadW float64, solar api.Rates) batterySolarPlan {
	if targetSoc < 50 || targetSoc > 100 || math.IsNaN(soc) || soc < 0 ||
		soc > 100 || math.IsNaN(capacityKWh) || capacityKWh <= 0 {
		return batterySolarPlan{Status: "disabled"}
	}
	if soc >= targetSoc-0.1 {
		return batterySolarPlan{Status: "reached"}
	}

	missingWh := capacityKWh * 1000 * (targetSoc - soc) / 100
	out := batterySolarPlan{Status: "no-forecast", EnergyNeededWh: missingWh}
	if len(solar) < 2 {
		out.ReserveW = 12000
		return out
	}

	// Above 300 W forecast marks useful PV generation. Exclude the next
	// morning's production; the target is *today's* solar finish.
	eod := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	for _, rate := range solar {
		if rate.Start.After(eod) || !rate.Start.Before(eod) {
			break
		}
		if rate.End.After(now) && rate.Value >= 300 && !math.IsNaN(rate.Value) &&
			!math.IsInf(rate.Value, 0) {
			if rate.End.Before(eod) {
				out.SolarEnd = rate.End
			} else {
				out.SolarEnd = eod
			}
		}
	}
	if out.SolarEnd.IsZero() || !out.SolarEnd.After(now.Add(5*time.Minute)) {
		out.Status = "solar-ended"
		out.ReserveW = 12000
		return out
	}

	hours := out.SolarEnd.Sub(now).Hours()
	projectedWh := solarEnergy(solar, now, out.SolarEnd)
	if math.IsNaN(projectedWh) || math.IsInf(projectedWh, 0) || projectedWh < 0 {
		out.ReserveW = 12000
		return out
	}

	// Conservative 10% solar forecast uncertainty allowance. Account for the
	// house's recent measured load, excluding the EV and battery.
	homeLoadW = math.Max(500, math.Min(12000, homeLoadW))
	out.ForecastNetWh = math.Max(0, projectedWh*0.9-homeLoadW*hours)

	// ~90% effective battery charging efficiency; keep a small time margin.
	requiredSolarWh := missingWh / 0.90 * 1.05
	if out.ForecastNetWh < requiredSolarWh {
		out.Status = "shortfall"
		out.ReserveW = 12000 // no battery charging current may be diverted
		return out
	}

	forecastNowW := 0.0
	if rate, err := solar.At(now); err == nil {
		forecastNowW = math.Max(0, rate.Value)
	}
	ratio := requiredSolarWh / math.Max(1, out.ForecastNetWh)
	shareW := math.Max(0, forecastNowW-homeLoadW) * ratio
	averageW := requiredSolarWh / hours

	// Prefer charging the battery while solar is strong; still preserve an
	// average-rate floor if forecast resolution is coarse.
	out.ReserveW = math.Min(12000, math.Max(averageW*0.8, shareW))
	out.Status = "forecast"
	return out
}
