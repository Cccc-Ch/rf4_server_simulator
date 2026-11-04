package gameserver

import (
	"context"
	"math"
	"testing"
)

func TestFightEquipmentAndFishWeightReachProtocolAndLoadModel(t *testing.T) {
	srv, _ := newBalanceTestServer(t)
	ctx := context.Background()
	sess := newProbeSess(srv)
	const uid = int64(1000001)
	var lastLimit float64
	for _, rodID := range []string{"tele_10012_tl13", "tele_10167_700"} {
		rod, err := srv.CreateItem(ctx, uid, rodID, "backpack", 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"fluoro_15807", "c_hook_20839"} {
			part, err := srv.CreateItem(ctx, uid, id, "backpack", 1)
			if err != nil {
				t.Fatal(err)
			}
			mountGearForTest(t, srv, sess, rod.GUID, part.GUID)
		}
		ids := srv.rigGearIDs(ctx, uid, rod.GUID)
		spec := srv.loadGearSpecForRig(ctx, ids, rod.GUID)
		limit := gearBreakLimitKg(spec)
		if limit <= lastLimit {
			t.Fatalf("stronger rod did not raise load limit: %g -> %g", lastLimit, limit)
		}
		lastLimit = limit
		rodTree, err := srv.ItemToTree(ctx, rod)
		if err != nil {
			t.Fatal(err)
		}
		if rodTree.Field(0).Val == nil {
			t.Fatal("rod blank missing from transmitted tree")
		}
		strengthIndex := fieldIdxByCS(rodTree.Class, csStrength)
		if strengthIndex < 0 {
			t.Fatal("rod strength missing from transmitted tree")
		}
		if got := f32At(rodTree, strengthIndex); math.Abs(got-spec.RodPowerKg) > spec.RodPowerKg*1e-5 {
			t.Fatalf("rod %s transmitted strength %g disagrees with model %g", rodID, got, spec.RodPowerKg)
		}
		previousLoad, previousStamina := 0.0, 0.0
		for _, grams := range []int64{100, 405, 1894, 10000, 100000} {
			fish := &RolledFish{FishGUID: "11111111111111111111111111111111", Species: "shemaya", Grade: 2, WeightG: grams}
			analysis := srv.analyzeFishFight(fish, spec)
			tree := srv.buildFish27(fish, "", rod.GUID)
			if analysis.PotentialLoad <= previousLoad || analysis.InitialStamina <= previousStamina {
				t.Fatalf("%s %dg did not increase load and stamina: %+v", rodID, grams, analysis)
			}
			if got := f32At(tree, 8); math.Abs(got-analysis.ForceN) > analysis.ForceN*1e-6 {
				t.Fatal("transmitted force and gear analysis diverged")
			}
			t.Logf("rod=%s weakest=%s limit_kgf=%.3f fish_g=%d force_n=%.3f base_load=%.4f peak_load=%.4f initial_stamina=%.1f",
				rodID, gearWeakestPart(spec), limit, grams, analysis.ForceN, analysis.PotentialLoad, analysis.PotentialPeak, analysis.InitialStamina)
			previousLoad, previousStamina = analysis.PotentialLoad, analysis.InitialStamina
		}
	}
}

func TestStrongerRodKeepsWeakLineBoundaryAndFishPerformance(t *testing.T) {
	srv, _ := newBalanceTestServer(t)
	fish := &RolledFish{Species: "shemaya", WeightG: 1894}
	weak := gearPhysicsSpec{RodPowerKg: 25, LineStrengthKg: 5}
	strong := weak
	strong.RodPowerKg = 100
	a, b := srv.analyzeFishFight(fish, weak), srv.analyzeFishFight(fish, strong)
	if a.ForceN != b.ForceN || a.InitialStamina != b.InitialStamina || a.PotentialLoad != b.PotentialLoad || a.GearLimitKg != 5 || b.GearLimitKg != 5 {
		t.Fatalf("strong rod bypassed weak line or changed fish performance: %+v %+v", a, b)
	}
}
