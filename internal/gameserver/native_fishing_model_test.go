package gameserver

import (
	"math"
	"testing"

	"rf4server/internal/config"
	"rf4server/internal/logging"
	"rf4server/internal/proto"
	"rf4server/internal/wiregen"
)

func TestNativeDragLastOrdinaryStepAndLock(t *testing.T) {
	g := verifiedForcePerGram * 1000
	last := 0.0
	for step := 0; step < 29; step++ {
		force, locked := nativeReelDragN(10, float64(step), 0, reelDragLocalLinear)
		if locked || force <= last || force > 10*g+1e-9 {
			t.Fatalf("step=%d force=%g locked=%v", step, force, locked)
		}
		last = force
	}
	if math.Abs(last-10*g*29/30) > 1e-9 {
		t.Fatal("本地补丁的内部 28（界面 29）应为额定泄力的 29/30")
	}
	if _, locked := nativeReelDragN(10, 29, 0, reelDragLocalLinear); !locked {
		t.Fatal("内部 29（界面无穷符号）必须锁轮")
	}
	heated, _ := nativeReelDragN(10, 28, 1, reelDragLocalLinear)
	if math.Abs(heated-last*0.95) > 1e-9 {
		t.Fatal("热衰减应作用于普通泄力")
	}
	spec := gearPhysicsSpec{RodPowerKg: 100, LineStrengthKg: 20, ReelPresent: true, ReelDragKg: 80, ReelBaseDragKg: 10, LineLengthM: 100}
	step := 28.0
	in := tackleLoadContext{ForceN: 30 * g, PeakN: 30 * g, NativeDragStep: &step}
	ordinary := evaluateTackleLoad(spec, in)
	step = 29
	locked := evaluateTackleLoad(spec, in)
	if !ordinary.Slipping || locked.Slipping || ordinary.LoadRatio >= 1 || locked.LoadRatio <= 1 {
		t.Fatalf("普通泄力与锁轮未区分: %+v %+v", ordinary, locked)
	}
}

func TestNativeFatigueResistanceDoesNotReduceInitialCapacity(t *testing.T) {
	spent, force := nativeFishFatigue(0, 0, 0.8, 100)
	if spent != 0 || force != 1 {
		t.Fatal("初始疲劳为零，f6 不能直接乘入初始体力")
	}
	spent, force = nativeFishFatigue(spent, 10, 0.8, 100)
	if math.Abs(spent-10) > 1e-9 || math.Abs(force-0.99) > 1e-9 {
		t.Fatal(spent, force)
	}
	spent, force = nativeFishFatigue(spent, 90, 0.8, 100)
	if math.Abs(spent-100) > 1e-9 || force != 0.1 {
		t.Fatal("疲劳极限仍保留 10% 力量", spent, force)
	}
	if spent, force = nativeFishFatigue(0, 1000, 1, 100); spent != 0 || force != 1 {
		t.Fatal("完全抗疲劳时不应扣减", spent, force)
	}
}

func TestNativeDragProfilesKeepOriginalAndLocalRulesSeparate(t *testing.T) {
	g := verifiedForcePerGram * 1000
	local, _ := nativeReelDragN(10, 28, 0, reelDragLocalLinear)
	vanilla, _ := nativeReelDragN(10, 28, 0, reelDragVanilla25034)
	if math.Abs(local-10*g*29/30) > 1e-9 || math.Abs(vanilla-10*g) > 1e-9 {
		t.Fatal("两套客户端曲线被混用", local, vanilla)
	}
	local, _ = nativeReelDragN(0, 0, 0, reelDragLocalLinear)
	vanilla, _ = nativeReelDragN(0, 0, 0, reelDragVanilla25034)
	if local != 0 || vanilla != 1 {
		t.Fatal("本地补丁移除了原版 1N 下限", local, vanilla)
	}
	for _, curve := range []reelDragCurve{reelDragLocalLinear, reelDragVanilla25034} {
		if _, locked := nativeReelDragN(10, 29, 0, curve); !locked {
			t.Fatal("两套曲线都应在内部 29 锁轮")
		}
	}
}

func TestNativeBrakeConditionMatchesBothClientProfiles(t *testing.T) {
	s, db := newBalanceTestServer(t)
	ctx := t.Context()
	row, err := s.CreateItem(ctx, testUID, "spin_5820", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, condition := range []float64{0.5, 0} {
		fields, err := FieldsFromJSON(row.FieldsJSON)
		if err != nil {
			t.Fatal(err)
		}
		brake, _, _, ok := s.selectedReelPart(ctx, fields, 9)
		if !ok {
			t.Fatal("缺少摩擦片")
		}
		setWireFloat(brake.Fields, brake.Class, fieldIdxByCS(brake.Class, csDurability), condition)
		row.FieldsJSON, err = FieldsToJSON(fields)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.UpdateItemFields(ctx, row.GUID, row.FieldsJSON, row.Qty, row.Durability, row.Workmanship, row.RigGUID); err != nil {
			t.Fatal(err)
		}
		tree, err := s.ItemToTree(ctx, row)
		if err != nil {
			t.Fatal(err)
		}
		brake, _, _, _ = s.selectedReelPart(ctx, tree.Fields, 9)
		if got := f32At(brake, fieldIdxByCS(brake.Class, csDurability)); got != condition {
			t.Fatal("读取物品把摩擦片耐久恢复了", got)
		}
		perf, err := s.syncReelPerformance(ctx, row)
		if err != nil {
			t.Fatal(err)
		}
		for _, profile := range []string{"0", "1"} {
			if err = db.TunableSet(ctx, "fight_reel_drag_curve", profile, "test"); err != nil {
				t.Fatal(err)
			}
			s.execAdminCmd(&AdminCommand{Op: "reload_tunables"})
			spec := s.loadGearSpecForRig(ctx, rigGearIDs{Reel: row.ItemID, ReelGUID: row.GUID, ReelDurability: 1}, "")
			want := perf.LockedDragKg
			if profile == "0" {
				want *= condition
			}
			if math.Abs(spec.ReelDragKg-want) > 1e-9 || spec.ReelEffectiveDragKg == nil || math.Abs(*spec.ReelEffectiveDragKg-perf.BaseDragKg*condition) > 1e-9 {
				t.Fatalf("profile=%s condition=%g spec=%+v", profile, condition, spec)
			}
		}
	}
}

func TestNativeLineLengthDoesNotConfuseDistanceDepthAndFixedSections(t *testing.T) {
	s := NewServer(config.Game{}, logging.Discard(), nil)
	fish := &RolledFish{FishGUID: "fish", Species: "c.carp", WeightG: 10000}
	report := &nativeFishingReport{FishGUID: "fish", ForceFactor: 1, DragMean: 5, DragMin: 5, DragMax: 5, EndpointDistanceM: 10, RigLengthM: 30}
	spec := gearPhysicsSpec{RigType: 7, RodPowerKg: 20, LinePresent: true, LineStrengthKg: 10, LineLengthM: 100, Water: fightWaterContext{FishDepthM: 2}}
	texas := s.analyzeFishFight(fish, spec, report)
	if !texas.LineOutKnown || texas.LineOutM != 30 {
		t.Fatal("已核准固定段为零的钓组没有使用连接器长", texas)
	}
	spec.RigType = 1
	floating := s.analyzeFishFight(fish, spec, report)
	if floating.LineOutKnown || floating.LineOutM != 0 || floating.ReportedRigLengthM != 30 || floating.ReportedEndpointDistanceM != 10 {
		t.Fatal("未核准固定段的钓组把总长、距离或水深冒充主线长", floating)
	}
	unknown := evaluateTackleLoad(spec, tackleLoadContext{LineOutM: 1000, LineOutUnknown: true})
	if unknown.LineExhausted || unknown.ReserveKnown {
		t.Fatal("未知放线长被当成确定的线量耗尽")
	}
}

func nativeReportRequest(rod string, elapsed, factor float64, step, packed uint32) *proto.Value {
	return proto.Args524(proto.Guid(rod), wiregen.MustMakeObj("cdhlhfejmai", map[int]*proto.Value{
		0: proto.F32(1), 3: proto.F32(10), 4: proto.F32(float32(factor)),
		14: proto.U32(step), 15: proto.U32(packed),
		19: proto.F32(10), 20: proto.F32(30), 23: proto.F32(float32(elapsed)),
	}))
}

func TestNativeReportScopeOrderingAndInvalidValues(t *testing.T) {
	s := NewServer(config.Game{}, logging.Discard(), nil)
	sess := newProbeSess(s)
	sess.ID = 42
	rod := "11111111111111111111111111111111"
	st := s.stateFor(sess.Userid)
	st.GearGuid, st.CastActive = rod, true
	st.Pending = &RolledFish{FishGUID: "22222222222222222222222222222222"}
	ctx := &Ctx{Sess: sess, Tree: nativeReportRequest(rod, 12, 0.81, 15, 15<<16|15), Log: logging.Discard()}
	if !recordNativeFishingReport(ctx) || st.NativeReport == nil || !st.Hooked {
		t.Fatal("有效上报未接入")
	}
	if st.NativeReport.EndpointDistanceM != 10 || st.NativeReport.RigLengthM != 30 || st.NativeReport.FatigueSpent != 10 {
		t.Fatal("空间距离、连接器长度或疲劳被混用", st.NativeReport)
	}
	previous := st.NativeReport
	for _, invalid := range []*proto.Value{
		nativeReportRequest(rod, 11, 0.81, 15, 15<<16|15),
		nativeReportRequest("33333333333333333333333333333333", 13, 0.81, 15, 15<<16|15),
		nativeReportRequest(rod, 13, math.NaN(), 15, 15<<16|15),
		nativeReportRequest(rod, 13, 0.81, 30, 30<<16|30),
		nativeReportRequest(rod, 13, 0.81, 15, 14<<16|16),
	} {
		ctx.Tree = invalid
		if recordNativeFishingReport(ctx) || st.NativeReport != previous {
			t.Fatal("乱序、其他竿或无效数据覆盖当前状态")
		}
	}
	st.Resolved = true
	ctx.Tree = nativeReportRequest(rod, 14, 0.81, 15, 15<<16|15)
	if recordNativeFishingReport(ctx) {
		t.Fatal("已结束的一杆仍接收上报")
	}
}

func TestNativeReportUsesSharedFishAndTackleModel(t *testing.T) {
	s := NewServer(config.Game{}, logging.Discard(), nil)
	fish := &RolledFish{FishGUID: "fish", Species: "c.carp", WeightG: 50_000}
	spec := gearPhysicsSpec{RodPowerKg: 40, LineStrengthKg: 10, ReelPresent: true, ReelDragKg: 30, ReelBaseDragKg: 5}
	baseline := s.analyzeFishFight(fish, spec)
	report := &nativeFishingReport{FishGUID: "fish", ForceFactor: 0.5, DragMean: 10, DragMin: 10, DragMax: 10, RigLengthM: 70, EndpointDistanceM: 30}
	live := s.analyzeFishFight(fish, spec, report)
	if !live.NativeReportUsed || live.ForceN != baseline.ForceN*0.5 || live.PotentialLoad >= baseline.PotentialLoad || live.InitialStamina != baseline.InitialStamina {
		t.Fatal("疲劳、泄力和原有部件承力未共同参与", baseline, live)
	}
	if fish.Force != 0 || fish.Stamina != 0 || report.FatigueSpent != 0 {
		t.Fatal("服务端重复修改客户端疲劳或鱼固有参数")
	}
	report.FishGUID = "previous-fish"
	if stale := s.analyzeFishFight(fish, spec, report); stale.NativeReportUsed || stale.ForceN != baseline.ForceN {
		t.Fatal("上一尾鱼的状态污染了当前鱼")
	}
}
