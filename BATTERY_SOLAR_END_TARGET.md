# Forecast-driven home battery target by solar end

Adds a **Battery target by solar end** selector at the bottom of evcc's Battery usage card.
Off retains existing behaviour; a selected SoC uses the remaining solar forecast
to prioritise home battery charging. This is a best-effort target, not a guarantee.

In **Smart** solar charging mode, evcc uses home battery SoC and capacity,
remaining solar forecast, household demand, and the estimated end of useful
sunlight to reserve battery charging power. Remaining surplus can charge the EV.
No AlphaESS inverter commands or Tesla ESP32 safety logic are changed.

If the forecast is insufficient or unavailable, the feature reserves ongoing
home-battery charging power (real grid-export surplus remains usable).
After target SoC is reached, existing priority and battery buffer controls apply.
Now/Always Charge intentionally ignores Smart solar budgets.

API: POST /api/batterysolartarget/{soc}. Setting 0 disables the goal.
Selection persists in the evcc settings database; prioritySoc and bufferSoc
are not changed. Live state exposes target, reserve watts, forecast end and
feasibility status.
