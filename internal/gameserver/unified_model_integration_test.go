package gameserver

import (
	"context"
	"encoding/json"
	"math"
	"testing"
)

func TestUnifiedWearUsesFishLoadAndPersistsProjection(t *testing.T) {
	s, db := newBalanceTestServer(t)
	ctx := context.Background()
	rod, err := s.CreateItem(ctx, testUID, "tele_10167_700", "backpack", 1)
	if err != nil {
		t.Fatal(err)
	}
	sess := newProbeSess(s)
	for _, id := range []string{"fluoro_15807", "c_hook_20839", "bob_25164"} {
		row, err := s.CreateItem(ctx, testUID, id, "backpack", 1)
		if err != nil {
			t.Fatal(err)
		}
		mountGearForTest(t, s, sess, rod.GUID, row.GUID)
	}
	st := s.stateFor(testUID)
	st.mu.Lock()
	st.GearGuid = rod.GUID
	st.mu.Unlock()
	fish := &RolledFish{Species: "shemaya", WeightG: 1894, LengthM: .43923, FightSeconds: 60}
	before, _ := db.GetItemByGUID(ctx, rod.GUID)
	model := s.catchWearForFish(ctx, testUID, rod.GUID, fish)
	amount, _ := s.catchWearAmount(ctx, model, before, "rod")
	s.wearRigForCatchPersisted(ctx, rod.GUID, fish)
	after, err := db.GetItemByGUID(ctx, rod.GUID)
	if err != nil || math.Abs(after.Durability-(before.Durability-amount)) > 1e-12 {
		t.Fatalf("modeled wear not persisted: %+v %v", after, err)
	}
	fields, err := FieldsFromJSON(after.FieldsJSON)
	idx := fieldIdxByCS(after.WireClass, csDurability)
	if err != nil || idx < 0 || math.Abs(rigTestFieldFloat(t, fields[idx])-after.Durability) > 1e-6 {
		t.Fatal("wire durability differs from stored durability")
	}
	var detail string
	if err := db.SQL.QueryRow(`SELECT detail_json FROM item_damage_log WHERE item_guid=? ORDER BY id DESC LIMIT 1`, rod.GUID).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if json.Unmarshal([]byte(detail), &data) != nil || data["wear_model"] != "potential_load" || data["fight_seconds"] != float64(60) {
		t.Fatal(detail)
	}
}
