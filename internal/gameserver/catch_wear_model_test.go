package gameserver

import (
	"testing"

	"rf4server/internal/config"
	"rf4server/internal/logging"
)

func TestCatchWearRespondsToFishAndIndividualPartStrength(t *testing.T) {
	s := NewServer(config.Game{}, logging.Discard(), nil)
	s.loadFightCurves()
	spec := gearPhysicsSpec{RodPowerKg: 25, LineStrengthKg: 10, HookPresent: true, HookStrengthKg: 20}
	small := s.analyzeFishFight(&RolledFish{Species: "shemaya", WeightG: 200}, spec)
	large := s.analyzeFishFight(&RolledFish{Species: "shemaya", WeightG: 1894}, spec)
	smallWear, largeWear := catchWearMultiplier(small, 10, 0), catchWearMultiplier(large, 10, 0)
	if largeWear <= smallWear {
		t.Fatalf("larger fish did not increase wear: %g <= %g", largeWear, smallWear)
	}
	strongWear := catchWearMultiplier(large, 25, 0)
	if strongWear >= largeWear {
		t.Fatalf("stronger individual part did not reduce strain: %g >= %g", strongWear, largeWear)
	}
	moreStamina := large
	moreStamina.InitialStamina *= 4
	if catchWearMultiplier(moreStamina, 10, 0) <= largeWear {
		t.Fatal("unknown-duration work estimate ignored the fish's stamina")
	}
	if catchWearMultiplier(moreStamina, 10, 60) <= catchWearMultiplier(large, 10, 60) {
		t.Fatal("measured-duration work ignored the fish's stamina")
	}
	previous := largeWear
	for _, seconds := range []float64{0.1, 1, 10, 60} {
		got := catchWearMultiplier(large, 10, seconds)
		if got <= previous {
			t.Fatalf("wear decreased or stopped growing at %gs: %g -> %g", seconds, previous, got)
		}
		if seconds == 0.1 && got/largeWear > 1.01 {
			t.Fatalf("wear jumped when measured duration started: %g -> %g", largeWear, got)
		}
		previous = got
	}
	beforeForce, beforeStamina := large.ForceN, large.InitialStamina
	_ = catchWearMultiplier(large, 1, 60)
	if large.ForceN != beforeForce || large.InitialStamina != beforeStamina {
		t.Fatal("wear changed the fish's own performance")
	}
}

func TestCatchWearUsesSharedWaterAndShockLoad(t *testing.T) {
	s := NewServer(config.Game{}, logging.Discard(), nil)
	s.loadFightCurves()
	fish := &RolledFish{Species: "shemaya", WeightG: 1894, LengthM: .44}
	spec := gearPhysicsSpec{RodPowerKg: 25, LineStrengthKg: 10, LineDiameterMM: .4,
		LineLengthM: 100, Water: fightWaterContext{Density: 1000}}
	still := s.analyzeFishFight(fish, spec)
	spec.Water.CurrentMPS = 1
	flow := s.analyzeFishFight(fish, spec)
	if catchWearMultiplier(flow, 10, 30) <= catchWearMultiplier(still, 10, 30) {
		t.Fatal("environmental load was omitted from wear")
	}
	spec.LineStretch = .8
	buffered := s.analyzeFishFight(fish, spec)
	if catchWearMultiplier(buffered, 10, 30) > catchWearMultiplier(flow, 10, 30) {
		t.Fatal("shock buffering increased wear")
	}
}

func TestCatchWearIsBoundedAndMissingDataKeepsBaseRate(t *testing.T) {
	if got := catchWearMultiplier(fishFightAnalysis{}, 10, 0); got != 1 {
		t.Fatalf("missing fish invented strain: %g", got)
	}
	a := fishFightAnalysis{ForceN: 1, GearLimitKg: 10, PotentialLoad: 1e100, PotentialPeak: 1e100, InitialStamina: 1e100}
	if got := catchWearMultiplier(a, 10, 1e100); got != 20 {
		t.Fatalf("giant fish wear escaped its bound: %g", got)
	}
	if got := catchWearMultiplier(a, 0, 10); got != 1 {
		t.Fatalf("unknown part strength should retain its base rate: %g", got)
	}
	a.PotentialLoad, a.PotentialPeak, a.InitialStamina = 0, 0, 0
	if got := catchWearMultiplier(a, 10, 0); got != .02 {
		t.Fatalf("minimum wear is not bounded: %g", got)
	}
}
