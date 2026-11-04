package gameserver

import (
	"context"
	"testing"
	"time"
)

// 旧式派鱼承重限制仍可显式开启；默认允许轻装备遇到大鱼。
func enableLegacyGearCap(t *testing.T, s *Server) {
	t.Helper()
	if s.DB != nil {
		if err := s.DB.TunableSet(t.Context(), "fish_weight_cap", "1", "test"); err != nil {
			t.Fatal(err)
		}
		s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
		return
	}
	s.tunMu.Lock()
	s.tunCache, s.tunAt = map[string]float64{"fish_weight_cap": 1}, time.Now()
	s.tunMu.Unlock()
}

// 未配置时不按装备承力排除大鱼。
func TestFishWeightCapDefaultAllowsLargeFish(t *testing.T) {
	s, _ := newDetachTestServer(t)
	spec := gearPhysicsSpec{RodPowerKg: 1, LineStrengthKg: 1, LinePresent: true, LineLengthM: 100}
	if got := s.sampleMatchedWeight("thynnus_t", func() float64 { return 300000 }, 0.01, spec); got != 300000 {
		t.Fatalf("默认应允许大鱼咬轻装备，得到 %d", got)
	}
}

// 非法配置回退到默认玩法。
func TestFishWeightCapInvalidValueFallsBack(t *testing.T) {
	s, db := newDetachTestServer(t)
	if err := db.TunableSet(context.Background(), "fish_weight_cap", "abc", "test"); err != nil {
		t.Fatal(err)
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if got := s.matchedWeightCapG("c.roach", 0.25); got != 0 {
		t.Fatalf("非法值应回退到不限制，得到 %v", got)
	}
}

// TestFishWeightCapExplicitOffAndOn 管理员显式 0 关闭截断、改回 1 恢复，
// 走管理指令 reload 的真实热更路径。
func TestFishWeightCapExplicitOffAndOn(t *testing.T) {
	s, db := newDetachTestServer(t)
	if err := db.TunableSet(context.Background(), "fish_weight_cap", "0", "test"); err != nil {
		t.Fatal(err)
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if got := s.tunableF("fish_weight_cap", 1); got != 0 {
		t.Fatalf("显式 0 后 fish_weight_cap = %v, want 0（截断关闭）", got)
	}
	if err := db.TunableSet(context.Background(), "fish_weight_cap", "1", "test"); err != nil {
		t.Fatal(err)
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if got := s.tunableF("fish_weight_cap", 1); got != 1 {
		t.Fatalf("显式 1 后 fish_weight_cap = %v, want 1（截断恢复）", got)
	}
}

func TestFishWeightCapControlsBothSamplingPaths(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name, location := "database", "level_900_rules"
		if fallback {
			name, location = "fallback", "weight-cap-fallback"
		}
		t.Run(name, func(t *testing.T) {
			s, db := seedBaitWeightTest(t)
			if _, err := db.SQL.Exec(`INSERT INTO fish_weight_override(loc,fish_key,weight,enabled,updated_by,updated_ts)
VALUES('rules-test','c.roach',1,1,'test',0),('rules-test','frog',0,0,'test',0)`); err != nil {
				t.Fatal(err)
			}
			if err := db.TunableSet(t.Context(), "fish_weight_scale", "4", "test"); err != nil {
				t.Fatal(err)
			}
			setBaitRuleSwitch(t, s, "0")
			for _, value := range []string{"1", "0", "1"} {
				if err := db.TunableSet(t.Context(), "fish_weight_cap", value, "test"); err != nil {
					t.Fatal(err)
				}
				s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
				matched, above := 0, 0
				for attempt := 0; matched < 40 && attempt < 1000; attempt++ {
					fish := s.rollFishWithRules(location, "rule_neutral", 0.25, neutralFishingSkillBonus())
					if fish == nil {
						continue
					}
					matched++
					if fish.WeightG > 250 {
						above++
					}
					if fish.WeightG < 1 || fish.WeightG > 5000 {
						t.Fatalf("scaled sample outside natural support: %+v", fish)
					}
				}
				if matched != 40 || value == "1" && above != 0 || value == "0" && above == 0 {
					t.Fatalf("switch=%s matched=%d above=%d", value, matched, above)
				}
			}
		})
	}
}

func TestFishWeightCapOffPreservesRigEligibility(t *testing.T) {
	s, db := newBalanceTestServer(t)
	for key, value := range map[string]string{"fish_weight_cap": "0", "fish_weight_scale": "4"} {
		if err := db.TunableSet(t.Context(), key, value, "test"); err != nil {
			t.Fatal(err)
		}
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	spec := gearPhysicsSpec{
		RodPowerKg: 1, LineStrengthKg: 1, HookPresent: true, HookLengthMM: 1,
		LinePresent: true, LineLengthM: 100, Water: defaultFightWater("rapid"),
	}
	if got := s.sampleMatchedWeight("c.roach", func() float64 { return 1000 }, 0.01, spec); got != 4000 {
		t.Fatalf("disabled cap changed scaled random sample: %d", got)
	}
	for _, invalid := range []gearPhysicsSpec{
		{BrokenPart: "hook"},
		{InvalidLeader: true},
		{LinePresent: true, LineLengthM: 0},
		{LinePresent: true, LineLengthM: 5, Water: fightWaterContext{FishDepthM: 10}},
		{Water: fightWaterContext{FishDepthM: 10, WaterDepthM: 5}},
	} {
		if got := s.sampleMatchedWeight("c.roach", func() float64 { return 1000 }, 0, invalid); got != 0 {
			t.Fatalf("disabled cap accepted unavailable tackle or depth: %+v weight=%d", invalid, got)
		}
	}
}
