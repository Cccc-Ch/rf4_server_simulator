package gameserver

import (
	"math"
	"testing"

	"rf4server/internal/proto"
)

func TestNativeMovementReportKeepsTurnsAndMetersSeparate(t *testing.T) {
	tree := nativeReportRequest("rod", 20, 1, 10, 10<<16|10)
	obj := findObj(tree, "cdhlhfejmai")
	for _, tc := range []struct{ manual, electric, released float32 }{{2, 5, 3}, {0, 0, 0}, {2, 0, 4}} {
		for i, value := range []float32{tc.manual, tc.electric, tc.released} {
			obj.Fields[10+i].Num = ptrU64(proto.F32(value).Num)
		}
		report, ok := decodeNativeFishingReport(tree)
		if !ok || !report.ReelMovementKnown || report.ManualTurns != float64(tc.manual) || report.ElectricTurns != float64(tc.electric) || report.ReleasedM != float64(tc.released) || report.RigLengthM != 30 {
			t.Fatal("转数被解释成主线米数或泄出长度", report)
		}
	}
	obj.Fields[11].Num = ptrU64(proto.F32(float32(math.NaN())).Num)
	if report, ok := decodeNativeFishingReport(tree); !ok || report.ReelMovementKnown {
		t.Fatal("非法电动转数污染有效核心报告", report, ok)
	}
}

func TestNativeFixedFloatLengthUsesSavedRigDepth(t *testing.T) {
	s, db := newBalanceTestServer(t)
	ctx := t.Context()
	rod, err := s.CreateItem(ctx, testUID, "tele_10012_tl13", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, rigType := range []int64{1, 2, 3} {
		if !s.rigSetType(ctx, testUID, rod.GUID, rigType) {
			t.Fatal("设置钓组失败", rigType)
		}
		row, err := db.GetItemByGUID(ctx, rod.GUID)
		if err != nil {
			t.Fatal(err)
		}
		fields, err := FieldsFromJSON(row.FieldsJSON)
		if err != nil {
			t.Fatal(err)
		}
		rig := protoFieldAt(fields, row.WireClass, csRigObject)
		rig.Fields[4].Num = ptrU64(proto.U32(175).Num)
		row.FieldsJSON, err = FieldsToJSON(fields)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.UpdateItemFields(ctx, row.GUID, row.FieldsJSON, row.Qty, row.Durability, row.Workmanship, row.RigGUID); err != nil {
			t.Fatal(err)
		}
		spec := s.loadGearSpecForRig(ctx, s.rigGearIDs(ctx, testUID, row.GUID), row.GUID)
		length, known := nativeMainLineOut(rigType, 30, spec.FixedRigLengthM)
		if rigType == 3 {
			if known || spec.FixedRigLengthM != nil {
				t.Fatal("滑漂动态固定段错误套用钓深")
			}
		} else if !known || length != 28.25 {
			t.Fatal("固定浮漂没有扣除真实钓深段", rigType, length, spec.FixedRigLengthM)
		}
	}
	for _, total := range []float64{-1, math.NaN(), math.Inf(1)} {
		if _, known := nativeMainLineOut(7, total); known {
			t.Fatal("非法主线总长被接受")
		}
	}
	fixed := 40.0
	if _, known := nativeMainLineOut(1, 30, &fixed); known {
		t.Fatal("固定段大于总长仍计算余线")
	}
	for _, rig := range []int64{5, 6, 7, 8, 11, 18, 22, 33, 35, 53, 54} {
		if length, known := nativeMainLineOut(rig, 30); !known || length != 30 {
			t.Fatal("原生工厂映射的零固定段遗漏", rig)
		}
	}
}

func TestMountedComponentsAndSelectedCaughtHook(t *testing.T) {
	s, db := newRigTestServer(t)
	ctx := t.Context()
	rod := baitTestRod(t, s)
	hook := mountHookForBaitTest(t, s, rod, 0)
	_, bait := baitTestMount(t, s, rod, "worm_01", 3, 350)
	spare, err := s.CreateItem(ctx, 1000001, "c_hook_20712", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	components := s.mountedFishingComponents(ctx, rod)
	seen := map[string]bool{}
	categories := map[int64]bool{}
	for _, c := range components {
		if seen[c.GUID] || c.GUID == spare.GUID || c.ItemID == "" || c.Role == "" || c.Definition == "" || c.Projection == nil {
			t.Fatal("实际挂载快照重复、缺属性或混入背包备用件", c)
		}
		seen[c.GUID], categories[c.Category] = true, true
	}
	if !seen[rod] || !seen[hook] || !seen[bait] || !categories[36] || !categories[5] {
		t.Fatalf("主装备、内嵌竿件或末端件遗漏: %+v", categories)
	}
	spec := gearPhysicsSpec{Components: components, HookPresent: true, HookStrengthKg: 999}
	fish := &RolledFish{HookGUID: hook, BaitGUIDs: bait}
	s.applyCaughtHook(ctx, fish, &spec)
	if spec.HookStrengthKg <= 0 || spec.HookStrengthKg == 999 || spec.BrokenPart != "" {
		t.Fatal("咬钩实例没有接入", spec.HookStrengthKg, spec.BrokenPart)
	}
	if err := db.SetItemDurability(ctx, hook, 0); err != nil {
		t.Fatal(err)
	}
	s.applyCaughtHook(ctx, fish, &spec)
	if spec.BrokenPart != "hook" {
		t.Fatal("损坏咬钩未传递到统一模型")
	}
	fish.HookGUID = spare.GUID
	s.applyCaughtHook(ctx, fish, &spec)
	if spec.BrokenPart != "hook" || spec.HookStrengthKg != 0 {
		t.Fatal("背包备用钩替代了已脱落咬钩")
	}
}
