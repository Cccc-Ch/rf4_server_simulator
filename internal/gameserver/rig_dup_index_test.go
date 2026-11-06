package gameserver

// 同名品类重复挂载回归：海钓竿换钓组类型后，旧类型的前导没有拆下来，
// f5 里留下两个 cat=13、extra=175 的组件。客户端的钓组槽位只有 175 一个，
// 两个组件叠在同一索引上，抛竿后整组被判无效并立即收回（表现为饵一入水
// 就回到杆上）。去重后同索引只保留一条，多余的退回背包。

import (
	"context"
	"testing"
)

// TestDuplicateRigIndexIsReturnedByProjection 验证历史重复索引在投影重建时收敛。
func TestDuplicateRigIndexIsReturnedByProjection(t *testing.T) {
	srv, db := newDetachTestServer(t)
	ctx := context.Background()
	const uid = int64(1000001)

	rod, err := srv.CreateItem(ctx, uid, "pilkr_10212_92-80190", "backpack", 1)
	if err != nil {
		t.Fatalf("造竿失败: %v", err)
	}
	if !srv.rigSetType(ctx, uid, rod.GUID, 50) { // rig_MarinePilker：前导只有 175 一个槽
		t.Fatal("rigSetType 失败")
	}
	kept, err := srv.CreateItem(ctx, uid, "sh_leader_40815", "backpack", 1)
	if err != nil {
		t.Fatalf("造前导失败: %v", err)
	}
	extra, err := srv.CreateItem(ctx, uid, "sh_leader_40815", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	srv.rigSetComponent(ctx, uid, rod.GUID, 0, kept.GUID, 175,
		rigSetOpts{ParentType: familyRod, EnforceCategory: true})

	// 构造旧版本/换类型留下的第二个同索引前导。
	row, err := db.GetItemByGUID(ctx, rod.GUID)
	if err != nil || row == nil {
		t.Fatal(err)
	}
	fields, err := FieldsFromJSON(row.FieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	idx := fieldIdxByCS(row.WireClass, csRigObject)
	name, rigType, ok := rigIdentity(fields[idx].Val)
	if !ok {
		t.Fatal("缺少钓组身份")
	}
	comps := rigCompsOf(fields[idx].Val)
	comps[rigCompKey(13, 1)] = rigComp{Category: 13, Ordinal: 1, GUID: extra.GUID, Extra: 175}
	fields[idx].Val = buildRig153Like(fields[idx].Val, name, rigType, comps, 100, 0)
	srv.saveItemFields(ctx, row, fields)
	if err := db.SetItemRigLink(ctx, extra.GUID, rod.GUID, 13); err != nil {
		t.Fatal(err)
	}

	if err := srv.reconcileRigProjection(ctx, rod.GUID); err != nil {
		t.Fatalf("投影重建失败: %v", err)
	}
	fresh, err := db.GetItemByGUID(ctx, rod.GUID)
	if err != nil {
		t.Fatal(err)
	}
	got := rigTestCategoryComps(t, rigStoredF5(t, fresh), 13)
	if len(got) != 1 || got[0].GUID != kept.GUID {
		t.Fatalf("重复前导未清理: %+v", got)
	}
	returned, err := db.GetItemByGUID(ctx, extra.GUID)
	if err != nil || returned == nil {
		t.Fatal(err)
	}
	if returned.Container != "backpack" || returned.RigGUID != "" {
		t.Fatalf("多余前导未退回背包: container=%q rig=%q", returned.Container, returned.RigGUID)
	}
}

// TestRigTypeSwitchDedupesSameIndex 验证换钓组类型时同索引组件被收敛。
func TestRigTypeSwitchDedupesSameIndex(t *testing.T) {
	srv, db := newDetachTestServer(t)
	ctx := context.Background()
	const uid = int64(1000001)

	rod, err := srv.CreateItem(ctx, uid, "pilkr_10212_92-80190", "backpack", 1)
	if err != nil {
		t.Fatalf("造竿失败: %v", err)
	}
	first, err := srv.CreateItem(ctx, uid, "sh_leader_40815", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := srv.CreateItem(ctx, uid, "sh_leader_40815", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	srv.rigSetComponent(ctx, uid, rod.GUID, 0, first.GUID, 175,
		rigSetOpts{ParentType: familyRod, EnforceCategory: true})

	// 手工制造两条同索引前导，模拟两个钓组类型各装过一次。
	row, err := db.GetItemByGUID(ctx, rod.GUID)
	if err != nil || row == nil {
		t.Fatal(err)
	}
	fields, err := FieldsFromJSON(row.FieldsJSON)
	if err != nil {
		t.Fatal(err)
	}
	idx := fieldIdxByCS(row.WireClass, csRigObject)
	name, rigType, _ := rigIdentity(fields[idx].Val)
	comps := rigCompsOf(fields[idx].Val)
	comps[rigCompKey(13, 1)] = rigComp{Category: 13, Ordinal: 1, GUID: second.GUID, Extra: 175}
	fields[idx].Val = buildRig153Like(fields[idx].Val, name, rigType, comps, 100, 0)
	srv.saveItemFields(ctx, row, fields)

	if !srv.rigSetType(ctx, uid, rod.GUID, 53) {
		t.Fatal("切换钓组类型失败")
	}
	fresh, err := db.GetItemByGUID(ctx, rod.GUID)
	if err != nil {
		t.Fatal(err)
	}
	got := rigTestCategoryComps(t, rigStoredF5(t, fresh), 13)
	if len(got) != 1 {
		t.Fatalf("换类型后同索引前导仍有 %d 条: %+v", len(got), got)
	}
}
