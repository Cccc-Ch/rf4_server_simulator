package gameserver

import (
	"math"
	"testing"

	"rf4server/internal/proto"
)

func TestFishingLoadComparisonMatrix(t *testing.T) {
	s, db := newBalanceTestServer(t)
	s.loadFightCurves()
	const rodGUID = "11111111111111111111111111111111"
	count := 0
	previousRodLoad := map[int64]float64{}
	for _, rodID := range []string{"spin_10001_S60L", "spin_10158_M60UL", "spin_10158_M88HH"} {
		def, err := db.GetItemDefByItemID(t.Context(), rodID)
		if err != nil || def == nil {
			t.Fatalf("rod %s: %v", rodID, err)
		}
		spec := gearPhysicsSpec{RigType: 5, RodPowerKg: rodPowerKg(def),
			LinePresent: true, LineStrengthKg: 300, LineLengthM: 100,
			ReelPresent: true, ReelDragKg: 300, ReelBaseDragKg: 10}
		previousMassLoad, previousStamina := 0.0, 0.0
		for _, grams := range []int64{100, 1894, 10000} {
			fish := &RolledFish{FishGUID: "22222222222222222222222222222222", Species: "shemaya", WeightG: grams}
			baseline := s.analyzeFishFight(fish, spec)
			if baseline.PotentialLoad <= previousMassLoad || baseline.InitialStamina <= previousStamina {
				t.Fatalf("fish mass did not increase load and stamina: %s %dg %+v", rodID, grams, baseline)
			}
			if old := previousRodLoad[grams]; old > 0 && baseline.PotentialLoad >= old {
				t.Fatalf("stronger rod did not reduce rod-limited load: %s %dg %+v", rodID, grams, baseline)
			}
			previousRodLoad[grams] = baseline.PotentialLoad
			previousMassLoad, previousStamina = baseline.PotentialLoad, baseline.InitialStamina
			for _, curve := range []reelDragCurve{reelDragLocalLinear, reelDragVanilla25034} {
				spec.ReelDragCurve = curve
				for _, factor := range []float64{1, 0.5} {
					for _, lineOut := range []float64{30, 100} {
						lastLoad := 0.0
						for _, step := range []uint32{0, 14, 28, 29} {
							request := nativeReportRequest(rodGUID, 20, factor, step, step<<16|step)
							findObj(request, "cdhlhfejmai").Fields[20].Num = ptrU64(proto.F32(float32(lineOut)).Num)
							report, ok := decodeNativeFishingReport(request)
							if !ok {
								t.Fatal("valid synthetic report rejected")
							}
							report.FishGUID = fish.FishGUID
							got := s.analyzeFishFight(fish, spec, &report)
							if !got.NativeReportUsed || !got.LineOutKnown || got.LineOutM != lineOut || got.GearLimitKg != spec.RodPowerKg {
								t.Fatalf("report or rod strength was not applied: %+v", got)
							}
							if got.InitialStamina != baseline.InitialStamina || math.Abs(got.ForceN-baseline.ForceN*factor) > 1e-9 {
								t.Fatal("report changed intrinsic stamina or ignored fatigue", got)
							}
							if !isFinitePositive(got.PotentialLoad) || got.PotentialLoad+1e-12 < lastLoad || got.PotentialLoad > baseline.PotentialLoad*factor+1e-12 {
								t.Fatalf("drag, fatigue or force budget diverged: rod=%s mass=%d step=%d line=%g factor=%g %+v", rodID, grams, step, lineOut, factor, got)
							}
							if (lineOut == 100 || step == 29) && math.Abs(got.PotentialLoad-baseline.PotentialLoad*factor) > 1e-12 {
								t.Fatalf("lock or exhausted line still gave drag protection: %+v", got)
							}
							lastLoad = got.PotentialLoad
							count++
						}
					}
				}
			}
			t.Logf("rod=%s rated_kgf=%.3f fish_g=%d force_n=%.3f load=%.5f stamina=%.1f", rodID, spec.RodPowerKg, grams, baseline.ForceN, baseline.PotentialLoad, baseline.InitialStamina)
		}
	}
	if count != 288 {
		t.Fatalf("comparison states=%d want=288", count)
	}
	t.Logf("validated %d synthetic model/protocol states; client HUD and fight time were not measured", count)
}
