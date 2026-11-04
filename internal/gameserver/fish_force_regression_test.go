package gameserver

import (
	"math"
	"testing"

	"rf4server/internal/config"
	"rf4server/internal/logging"
)

func TestLargeFishForceReachesProtocolCurvesAndAnalysis(t *testing.T) {
	s := NewServer(config.Game{}, logging.Discard(), nil)
	s.loadFightCurves()
	g := verifiedForcePerGram * 1000
	for _, species := range []string{"wfish", "c.carp", "a_salmon", "w_catfish", "thynnus_t", "cetorhinus_m"} {
		for _, kg := range []float64{0.1, 1, 10, 100, 110.458, 300, 2000} {
			fish := &RolledFish{FishGUID: "fish", Species: species, WeightG: int64(math.Round(kg * 1000))}
			want := float64(fish.WeightG) / 1000 * performanceForFish(species).pull * g
			model := s.fishFightModel(fish)
			wire := s.buildFish27(fish, "", "")
			analysis := s.analyzeFishFight(fish, gearPhysicsSpec{})
			if math.Abs(model.ForceN-want) > want*1e-12 || analysis.ForceN != model.ForceN || math.Abs(f32At(wire, 8)-want) > want*1e-6 || math.Abs(f32At(wire.Field(21).Val.Items[0], 8)-want) > want*1e-6 {
				t.Fatalf("%s %.3fkg: 拉力在模型/协议/动作曲线之间被压缩", species, kg)
			}
		}
	}
	fish := &RolledFish{Species: "wfish", WeightG: 110458}
	model := s.fishFightModel(fish)
	t.Logf("110.458kg: baseline=%.3fkgf, curve peak=%.3fkgf", model.ForceN/g, model.ForceN*model.CurvePeakRatio/g)
}

func TestLargeFishDragLockAndFatigueRemainSeparate(t *testing.T) {
	s := NewServer(config.Game{}, logging.Discard(), nil)
	fish := &RolledFish{FishGUID: "fish", Species: "wfish", WeightG: 100000}
	spec := gearPhysicsSpec{RigType: 5, RodPowerKg: 200, LinePresent: true, LineStrengthKg: 200, LineLengthM: 100,
		ReelPresent: true, ReelDragKg: 40, ReelBaseDragKg: 30}
	report := &nativeFishingReport{FishGUID: "fish", ForceFactor: 1, DragMean: 28, DragMin: 28, DragMax: 28, RigLengthM: 20}
	ordinary := s.analyzeFishFight(fish, spec, report)
	report.DragMean, report.DragMin, report.DragMax = 29, 29, 29
	locked := s.analyzeFishFight(fish, spec, report)
	if ordinary.PotentialLoad >= 1 || locked.PotentialLoad <= 1 || locked.ForceN != ordinary.ForceN || locked.InitialStamina != ordinary.InitialStamina {
		t.Fatal("泄力保护、锁轮传力或鱼固有参数关联错误", ordinary, locked)
	}
	report.ForceFactor = 0.1
	fatigued := s.analyzeFishFight(fish, spec, report)
	if math.Abs(fatigued.ForceN-locked.ForceN*0.1) > 1e-9 || fatigued.InitialStamina != locked.InitialStamina {
		t.Fatal("客户端疲劳系数未沿用或体力重复扣减")
	}
	if fish.Force != 0 || fish.Stamina != 0 || spec.ReelDragKg != 40 {
		t.Fatal("分析修改了鱼或装备原值")
	}
}

func TestLargeFishWeightAndPowerSettingsReachProtocolOnce(t *testing.T) {
	s, db := newBalanceTestServer(t)
	for key, value := range map[string]string{"fish_weight_scale": "4", "fight_power_scale": "2"} {
		if err := db.TunableSet(t.Context(), key, value, "test"); err != nil {
			t.Fatal(err)
		}
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	weight := s.sampleMatchedWeight("wfish", func() float64 { return 25000 }, 0)
	fish := &RolledFish{Species: "wfish", WeightG: weight}
	wire := s.buildFish27(fish, "", "")
	weightField := wire.Field(4)
	want := 200 * verifiedForcePerGram * 1000
	if weight != 100000 || weightField.U32() != 100000 || math.Abs(f32At(wire, 8)-want) > want*1e-6 {
		t.Fatal("四倍鱼重没有进入力量或力量倍率被重复计算", weight, f32At(wire, 8))
	}
	fish.Force = 42
	if got := s.fishFightModel(fish).ForceN; got != 84 {
		t.Fatal("显式鱼力没有优先于重量估值或倍率重复", got)
	}
}
