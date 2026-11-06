package gameserver

import (
	"context"
	"math"
	"testing"
)

// 派鱼重量上限策略：
//
//	鱼咬不咬钩取决于饵、水域、深度和鱼种分布，**不取决于钓具强度**。
//	所以 fish_weight_cap 默认 0（承力不参与派鱼），只有管理员显式写 1
//	才恢复“按最弱环截断”的上游行为。
//
//	钩入口限制是另一回事：钩体太小，大鱼即使咬中也挂不住。这条是几何
//	关系，单独由 hook_entry_cap 控制，默认 1（开启）。
//
// 这个默认值与上游相反，是有意为之，见 gear_physics.go 的 fishWeightCapKg。

// TestFishWeightCapDefaultIsOff 未配置时承力截断必须关闭：
// 弱线组照样会遇到大鱼，后果交给搏鱼断线，而不是“大鱼不来”。
func TestFishWeightCapDefaultIsOff(t *testing.T) {
	s, _ := newDetachTestServer(t)
	if got := s.tunableF("fish_weight_cap", 0); got != 0 {
		t.Fatalf("未配置时 fish_weight_cap = %v, want 0（承力不参与派鱼）", got)
	}
	if got := s.tunableF("hook_entry_cap", 1); got != 1 {
		t.Fatalf("未配置时 hook_entry_cap = %v, want 1（钩入口默认开启）", got)
	}
}

// TestFishWeightCapInvalidValueFallsBack 非法配置值回退到新默认（0）。
func TestFishWeightCapInvalidValueFallsBack(t *testing.T) {
	s, db := newDetachTestServer(t)
	if err := db.TunableSet(context.Background(), "fish_weight_cap", "abc", "test"); err != nil {
		t.Fatal(err)
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if got := s.tunableF("fish_weight_cap", 0); got != 0 {
		t.Fatalf("非法值 fish_weight_cap = %v, want 0（回退默认）", got)
	}
}

// TestFishWeightCapExplicitOffAndOn 管理员显式 1 恢复承力截断、改回 0 关闭，
// 走管理指令 reload 的真实热更路径。
func TestFishWeightCapExplicitOffAndOn(t *testing.T) {
	s, db := newDetachTestServer(t)
	if err := db.TunableSet(context.Background(), "fish_weight_cap", "1", "test"); err != nil {
		t.Fatal(err)
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if got := s.tunableF("fish_weight_cap", 0); got != 1 {
		t.Fatalf("显式 1 后 fish_weight_cap = %v, want 1（承力截断恢复）", got)
	}
	if err := db.TunableSet(context.Background(), "fish_weight_cap", "0", "test"); err != nil {
		t.Fatal(err)
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if got := s.tunableF("fish_weight_cap", 0); got != 0 {
		t.Fatalf("显式 0 后 fish_weight_cap = %v, want 0（承力截断关闭）", got)
	}
}

// TestDispatchIgnoresGearStrength 是策略本身的行为锁：
// 同一条钩，把主线从 1kg 换成 100kg，派鱼上限不应变化；
// 只有钩入口会改变上限。
func TestDispatchIgnoresGearStrength(t *testing.T) {
	s, db := newDetachTestServer(t)
	weak := gearPhysicsSpec{RodPowerKg: 3, LineStrengthKg: 1, HookPresent: true, HookLengthMM: 12, HookSize: 8}
	strong := gearPhysicsSpec{RodPowerKg: 80, LineStrengthKg: 100, HookPresent: true, HookLengthMM: 12, HookSize: 8}

	weakCap := s.fishWeightCapKg(weak)
	strongCap := s.fishWeightCapKg(strong)
	if math.Abs(weakCap-strongCap) > 1e-9 {
		t.Fatalf("派鱼上限不应随钓具强度变化: 弱组=%.3fkg 强组=%.3fkg", weakCap, strongCap)
	}
	if weakCap <= 0 {
		t.Fatalf("默认应只剩钩入口上限，实得 %.3f", weakCap)
	}
	if want := hookEntryCapKg(weak); math.Abs(weakCap-want) > 1e-9 {
		t.Fatalf("默认派鱼上限应等于钩入口上限: got=%.4f want=%.4f", weakCap, want)
	}

	// 显式打开承力截断后，强度才重新生效。
	if err := db.TunableSet(context.Background(), "fish_weight_cap", "1", "test"); err != nil {
		t.Fatal(err)
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	if got, want := s.fishWeightCapKg(weak), gearStrengthCapKg(weak); math.Abs(got-want) > 1e-9 {
		t.Fatalf("开启承力截断后应回到强度上限: got=%.4f want=%.4f", got, want)
	}
}

// TestHookEntryCapCanBeDisabled 钩入口也可以单独关掉，关掉后完全不截断。
func TestHookEntryCapCanBeDisabled(t *testing.T) {
	s, db := newDetachTestServer(t)
	if err := db.TunableSet(context.Background(), "hook_entry_cap", "0", "test"); err != nil {
		t.Fatal(err)
	}
	s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
	spec := gearPhysicsSpec{RodPowerKg: 3, LineStrengthKg: 1, HookPresent: true, HookLengthMM: 12, HookSize: 8}
	if got := s.fishWeightCapKg(spec); got != 0 {
		t.Fatalf("两个开关都关闭时应不截断，实得 %.3f", got)
	}
}
