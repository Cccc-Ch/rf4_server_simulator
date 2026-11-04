package gameserver

import (
	"math"
	"testing"
)

func TestNativeMotorForceSpeedAndStall(t *testing.T) {
	motor := reelMotorSpec{Present: true, PullKg: 33, SpeedMPM: 180}
	if got := nativeMotorSpeedMPS(motor, 0, 12); got != 3 {
		t.Fatal("180 米/分钟应为 3 米/秒", got)
	}
	if got := nativeMotorSpeedMPS(motor, 0, 4); math.Abs(got-1.05) > 1e-9 {
		t.Fatal("低控制量速度错误", got)
	}
	if got := nativeMotorSpeedMPS(motor, 33*9.8+0.5, 12); math.Abs(got) > 1e-12 {
		t.Fatal("电机超负荷仍在收线", got)
	}
	if got := nativeMotorSpeedMPS(reelMotorSpec{}, 0, 12); got != 0 {
		t.Fatal("普通轮凭空获得电机", got)
	}
}

func TestNativeSpoolCapacityDiameterBranches(t *testing.T) {
	for _, tc := range []struct{ diameter, coefficient, want float64 }{
		{0.2, 100, 144}, {0.25, 100, 100}, {0.5, 100, 63}, {0.8, 100, 23}, {1, 100, 16}, {1, 1, 5},
	} {
		if got := nativeSpoolCapacityM(tc.coefficient, tc.diameter); got != tc.want {
			t.Errorf("d=%g coefficient=%g capacity=%g want=%g", tc.diameter, tc.coefficient, got, tc.want)
		}
	}
}

func TestMountedMotorUsesSelectedInstanceAndPreservesZero(t *testing.T) {
	s, db := newBalanceTestServer(t)
	ctx := t.Context()
	reel, err := s.CreateItem(ctx, testUID, "elcon_5968", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := FieldsFromJSON(reel.FieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	motor, _, _, ok := s.selectedReelPart(ctx, fields, 81)
	if !ok {
		t.Fatal("缺少电机")
	}
	setWireFloat(motor.Fields, motor.Class, fieldIdxByCS(motor.Class, "npnlhobgfen"), 0)
	reel.FieldsJSON, _ = FieldsToJSON(fields)
	if err = db.UpdateItemFields(ctx, reel.GUID, reel.FieldsJSON, reel.Qty, reel.Durability, reel.Workmanship, reel.RigGUID); err != nil {
		t.Fatal(err)
	}
	spec := s.loadGearSpecForRig(ctx, rigGearIDs{Reel: reel.ItemID, ReelGUID: reel.GUID, ReelDurability: 1}, "")
	if !spec.Motor.Present || spec.Motor.PullKg != 0 || spec.Motor.SpeedMPM <= 0 {
		t.Fatal(spec.Motor)
	}
}

func TestLeadcoreUsesExactLoadAndBreaksWithoutTakingMainLine(t *testing.T) {
	s, db := newBalanceTestServer(t)
	ctx := t.Context()
	rod, err := s.CreateItem(ctx, testUID, "tele_10180_4", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	sess := newProbeSess(s)
	var leadcoreGUID string
	for _, id := range []string{"mono_15095", "fc_leader_40048", "lc_40200", "clas_20163", "worm_01", "bob_25001"} {
		row, err := s.CreateItem(ctx, testUID, id, "backpack", 1)
		if err != nil {
			t.Fatal(err)
		}
		result := mountGearForTest(t, s, sess, rod.GUID, row.GUID)
		if id == "lc_40200" {
			leadcoreGUID = result.MountedGUID
		}
	}
	if err = db.SetItemDurability(ctx, leadcoreGUID, 0.01); err != nil {
		t.Fatal(err)
	}
	before := rigSlotGUIDs(t, s, rod.GUID)
	lineLength := lineLengthOfTestItem(t, s, before[catLine])
	ids := s.rigGearIDs(ctx, testUID, rod.GUID)
	spec := s.loadGearSpecForRig(ctx, ids, rod.GUID)
	if ids.LeadcoreGUID != leadcoreGUID || math.Abs(spec.LeadcoreStrengthKg-0.09) > 1e-6 || gearWeakestPart(spec) != "leadcore" {
		t.Fatal(ids, spec)
	}
	out, err := s.breakLineOrRod(ctx, testUID, rod.GUID, leadcoreGUID)
	if err != nil || out.BreakPart != "leadcore" || out.LineLossM != 0 {
		t.Fatal(out, err)
	}
	for _, category := range []int64{gearCatLeadcore, catLead, catHook, catBait} {
		if row, _ := db.GetItemByGUID(ctx, before[category]); row != nil {
			t.Fatalf("断铅芯残留类别 %d", category)
		}
	}
	if row, _ := db.GetItemByGUID(ctx, before[catFloat]); row == nil || lineLengthOfTestItem(t, s, before[catLine]) != lineLength {
		t.Fatal("断铅芯损失上方浮漂或主线")
	}
}
