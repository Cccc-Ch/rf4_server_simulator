package gameserver

import (
	"math"
	"testing"

	"rf4server/internal/config"
	"rf4server/internal/logging"
)

func TestFallbackFishStaticsPreserveCalibrationAndGrowBeyondIt(t *testing.T) {
	for i, weight := range fallbackCalWeights {
		got := fallbackFightStatics(weight)
		for field, values := range fallbackCalFields {
			if math.Abs(got[field]-values[i]) > 1e-12 {
				t.Fatalf("%gg field %d changed calibration: %g != %g", weight, field, got[field], values[i])
			}
		}
	}
	anchor := fallbackCalWeights[len(fallbackCalWeights)-1]
	base := fallbackFightStatics(anchor)
	last := base
	for _, weight := range []float64{anchor + 0.001, 1000, 1894, 10000, 100000, 3125000} {
		got := fallbackFightStatics(weight)
		for field := range fallbackGrowthPowers {
			if !isFinitePositive(got[field]) || got[field] <= last[field] {
				t.Fatalf("%gg field %d stopped growing: %g -> %g", weight, field, last[field], got[field])
			}
			if weight == anchor+0.001 && math.Abs(got[field]-base[field])/base[field] > 0.00001 {
				t.Fatalf("field %d jumped at calibration boundary: %g -> %g", field, base[field], got[field])
			}
		}
		for field, value := range base {
			if _, grows := fallbackGrowthPowers[field]; !grows && got[field] != value {
				t.Fatalf("%gg dimensionless field %d changed: %g != %g", weight, field, got[field], value)
			}
		}
		last = got
	}
}

func TestFallbackFishWeightReachesTransmittedDynamics(t *testing.T) {
	srv := NewServer(config.Game{}, logging.Discard(), nil)
	srv.loadFightCurves()
	previous := map[int]float64{}
	for _, grams := range []int64{405, 1894, 10000, 100000, 3125000} {
		fish := &RolledFish{FishGUID: "11111111111111111111111111111111", Species: "shemaya", Grade: 2,
			WeightG: grams, LengthM: fishBodyLength("shemaya", float64(grams), 0.0355, 1.0/3)}
		tree := srv.buildFish27(fish, "", "")
		weightField := tree.Field(4)
		if got := weightField.U32(); got != uint32(grams) {
			t.Fatalf("transmitted weight=%d want=%d", got, grams)
		}
		for _, field := range []int{3, 5, 8, 9, 10, 11, 19} {
			got := f32At(tree, field)
			if !isFinitePositive(got) || got <= previous[field] {
				t.Fatalf("%dg transmitted field %d stopped growing: %g -> %g", grams, field, previous[field], got)
			}
			previous[field] = got
		}
		curves := tree.Field(21).Val
		if curves == nil || len(curves.Items) == 0 || math.Abs(f32At(curves.Items[0], 8)-f32At(tree, 8)) > f32At(tree, 8)*1e-6 {
			t.Fatal("transmitted fish force disagrees with curve tension")
		}
	}
}

func TestFishFightModelFeedsProtocolAnalysisAndGeometry(t *testing.T) {
	srv := NewServer(config.Game{}, logging.Discard(), nil)
	srv.loadFightCurves()
	fish := &RolledFish{FishGUID: "11111111111111111111111111111111", Species: "shemaya", Grade: 2, WeightG: 1894}
	model := srv.fishFightModel(fish)
	if model.WeightG != fish.WeightG || model.LengthM <= 0 || fish.LengthM != 0 {
		t.Fatalf("model did not derive geometry without changing the catch: %+v", model)
	}
	tree := srv.buildFish27(fish, "", "")
	for field, want := range map[int]float64{3: model.LengthM, 5: model.Stamina, 6: model.FatigueResistance, 8: model.ForceN} {
		if got := f32At(tree, field); math.Abs(got-want) > want*1e-6 {
			t.Fatalf("field %d disagrees with fish model: %g != %g", field, got, want)
		}
	}
	for field, want := range model.Statics {
		if got := f32At(tree, field); math.Abs(got-want) > math.Max(1, want)*1e-6 {
			t.Fatalf("dynamic field %d disagrees with fish model: %g != %g", field, got, want)
		}
	}
	spec := gearPhysicsSpec{RodPowerKg: 10, LineStrengthKg: 10,
		Water: fightWaterContext{Kind: "river", Density: 1000, CurrentMPS: 0.35}}
	analysis := srv.analyzeFishFight(fish, spec)
	if analysis.ForceN != model.ForceN || analysis.PeakForceN != model.ForceN*model.CurvePeakRatio || analysis.InitialStamina != model.Stamina {
		t.Fatalf("analysis disagrees with transmitted fish model: %+v", analysis)
	}
	fish.LengthM = model.LengthM * 2
	longer := srv.fishFightModel(fish)
	longerAnalysis := srv.analyzeFishFight(fish, spec)
	if longer.LengthM != fish.LengthM || longer.ForceN != model.ForceN || longer.Stamina != model.Stamina {
		t.Fatal("body length changed mass-based intrinsic performance")
	}
	if longerAnalysis.WaterDragN >= analysis.WaterDragN || longerAnalysis.PotentialLoad >= analysis.PotentialLoad {
		t.Fatal("body length did not reach the water-load calculation")
	}
}

func TestFishFightModelPreservesExplicitPerformanceAndNil(t *testing.T) {
	srv := NewServer(config.Game{}, logging.Discard(), nil)
	fish := &RolledFish{Species: "a_salmon", WeightG: 5000, LengthM: 0.75, Force: 42, Stamina: 1234}
	model := srv.fishFightModel(fish)
	if model.ForceN != 42 || model.Stamina != 1234*defaultFishStaminaScale || model.LengthM != 0.75 {
		t.Fatalf("explicit fish performance changed: %+v", model)
	}
	missing := srv.fishFightModel(nil)
	if missing.WeightG != 0 || missing.LengthM != 0 || missing.ForceN != 0 || missing.Stamina != 0 || missing.FatigueResistance != 0 || missing.CurvePeakRatio != 0 || missing.Statics != nil {
		t.Fatalf("missing fish invented model values: %+v", missing)
	}
}
