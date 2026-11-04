package gameserver

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"rf4server/internal/logging"
	"rf4server/internal/store"
)

func applyLevelRewardStep(t *testing.T, s *Server, uid, from, to int64) levelRewardResult {
	t.Helper()
	tx, err := s.DB.SQL.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE profile SET level=?,xp=? WHERE uid=?`, to, baseXp(to), uid); err != nil {
		t.Fatal(err)
	}
	r, err := s.applyLevelRewardsTx(context.Background(), tx, uid, from, to, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return r
}

func assertLevelRewardCount(t *testing.T, s *Server, query string, want int64) {
	t.Helper()
	var got int64
	if err := s.DB.SQL.QueryRow(query).Scan(&got); err != nil || got != want {
		t.Fatalf("%s: %d want %d (%v)", query, got, want, err)
	}
}

func TestLevelRewardsFullProgressionAndReplay(t *testing.T) {
	s, db := newDetachTestServer(t)
	const uid = int64(1000001)
	if _, err := db.EnsureProfile(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	r := applyLevelRewardStep(t, s, uid, 1, 58)
	if r.Gold != 149000 || len(r.MailIDs) != 5 {
		t.Fatalf("full progression reward: %+v", r)
	}
	assertLevelRewardCount(t, s, `SELECT gold FROM profile WHERE uid=1000001`, 149000)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_receipt WHERE uid=1000001`, 57)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_state WHERE uid=1000001 AND baseline_level=1 AND high_water_level=58`, 1)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM mail WHERE uid=1000001`, 5)
	for range 2 {
		replay := applyLevelRewardStep(t, s, uid, 1, 58)
		if replay.Gold != 0 || len(replay.MailIDs) != 0 {
			t.Fatalf("replayed rewards: %+v", replay)
		}
	}
	assertLevelRewardCount(t, s, `SELECT gold FROM profile WHERE uid=1000001`, 149000)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM txn WHERE kind='level_reward'`, 1)
	if skillPointsTotal(58) != 153 {
		t.Fatal("level rewards changed the existing skill point curve")
	}
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM skill_node`, 0)
}

func TestLevelRewardsLegacyBaselineAdminChangesAndDisabledPeriod(t *testing.T) {
	s, db := newDetachTestServer(t)
	const uid = int64(1000001)
	if _, err := db.EnsureProfile(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	r := applyLevelRewardStep(t, s, uid, 29, 30)
	if r.Gold != 3000 || len(r.MailIDs) != 1 {
		t.Fatalf("old character received retroactive rewards: %+v", r)
	}
	tx, err := db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLevelRewardHighWaterTx(context.Background(), tx, uid, 50, 101); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLevelRewardHighWaterTx(context.Background(), tx, uid, 10, 102); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if r := applyLevelRewardStep(t, s, uid, 10, 11); r.Gold != 0 || len(r.MailIDs) != 0 {
		t.Fatalf("admin level replay granted rewards: %+v", r)
	}
	if err := db.TunableSet(context.Background(), "level_rewards_enabled", "0", "test"); err != nil {
		t.Fatal(err)
	}
	if r := applyLevelRewardStep(t, s, uid, 11, 52); r.Gold != 0 || len(r.MailIDs) != 0 {
		t.Fatalf("disabled rewards granted: %+v", r)
	}
	if err := db.TunableSet(context.Background(), "level_rewards_enabled", "1", "test"); err != nil {
		t.Fatal(err)
	}
	if r := applyLevelRewardStep(t, s, uid, 52, 53); r.Gold != 5000 || len(r.MailIDs) != 0 {
		t.Fatalf("reenabling rewards backfilled skipped levels: %+v", r)
	}
	assertLevelRewardCount(t, s, `SELECT gold FROM profile WHERE uid=1000001`, 8000)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_receipt WHERE level IN (30,53)`, 2)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_receipt`, 2)
}

func TestLevelRewardsRollbackAndGoldOverflowKeepProgressAtomic(t *testing.T) {
	s, db := newDetachTestServer(t)
	const uid = int64(1000001)
	if _, err := db.EnsureProfile(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_level_reward_mail BEFORE INSERT ON mail
		BEGIN SELECT RAISE(ABORT,'mail unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE profile SET level=10,xp=? WHERE uid=?`, baseXp(10), uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyLevelRewardsTx(context.Background(), tx, uid, 1, 10, 100); err == nil {
		t.Fatal("mail failure did not fail the reward transaction")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertLevelRewardCount(t, s, `SELECT level FROM profile WHERE uid=1000001`, 1)
	assertLevelRewardCount(t, s, `SELECT gold FROM profile WHERE uid=1000001`, 0)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_state`, 0)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_receipt`, 0)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM mail`, 0)
	if _, err := db.SQL.Exec(`DROP TRIGGER reject_level_reward_mail`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`UPDATE profile SET gold=? WHERE uid=?`, int64(math.MaxInt64-1), uid); err != nil {
		t.Fatal(err)
	}
	tx, err = db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyLevelRewardsTx(context.Background(), tx, uid, 1, 2, 101); err == nil {
		t.Fatal("overflowing gold unexpectedly succeeded")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertLevelRewardCount(t, s, `SELECT gold FROM profile WHERE uid=1000001`, math.MaxInt64-1)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_receipt`, 0)
}

func TestLevelRewardEquipmentDefinitionsFitTheirRodsAndSpools(t *testing.T) {
	s, db := newDetachTestServer(t)
	ctx := context.Background()
	for level, gifts := range levelRewardGifts {
		for _, gift := range gifts {
			def, err := db.GetItemDefByItemID(ctx, gift.itemID)
			if err != nil || def == nil {
				t.Fatalf("level %d missing %s: %v", level, gift.itemID, err)
			}
			if !countableCategories[def.Category] && gift.qty != 1 {
				t.Fatalf("noncountable item %s was bundled as qty %d", gift.itemID, gift.qty)
			}
		}
		if level == 30 {
			continue
		}
		rod, err := db.GetItemDefByItemID(ctx, gifts[0].itemID)
		if err != nil {
			t.Fatal(err)
		}
		line, err := db.GetItemDefByItemID(ctx, gifts[2].itemID)
		if err != nil {
			t.Fatal(err)
		}
		reel, err := s.prepareNewItem(ctx, 1000001, gifts[1].itemID, "backpack", 1, "")
		if err != nil {
			t.Fatal(err)
		}
		_, _, capacity := s.castReelSpoolStats(ctx, reel, line.ItemID)
		if rod.Category != gearCatRod || line.Category != gearCatLine || lineStrengthKg(line) > rodPowerKg(rod) || capacity+1 < float64(lineLengthM(line)) {
			t.Fatalf("level %d kit incompatible: rod=%v line=%v spool=%v length=%v", level, rodPowerKg(rod), lineStrengthKg(line), capacity, lineLengthM(line))
		}
	}
}

func TestLevelRewardMailClaimIsCompleteAndRetrySafe(t *testing.T) {
	s, db := newDetachTestServer(t)
	const uid = int64(1000001)
	if _, err := db.EnsureProfile(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	r := applyLevelRewardStep(t, s, uid, 1, 30)
	sess := newProbeSess(s)
	ctx := &Ctx{Sess: sess, Log: logging.Discard()}
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_reward_item BEFORE INSERT ON item
		WHEN NEW.item_id='corn' BEGIN SELECT RAISE(ABORT,'inventory unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if count, _ := s.acceptMails(ctx, r.MailIDs); count != 0 {
		t.Fatal("failed claim issued part of a kit")
	}
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM item WHERE uid=1000001`, 0)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM mail WHERE uid=1000001 AND read=0 AND items_json IS NOT NULL`, 5)
	if _, err := db.SQL.Exec(`DROP TRIGGER reject_reward_item`); err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, gifts := range levelRewardGifts {
		want += len(gifts)
	}
	count, granted := s.acceptMails(ctx, r.MailIDs)
	if count != want || len(granted) != want {
		t.Fatalf("incomplete kits: granted=%d want=%d", count, want)
	}
	for _, item := range granted {
		if item.Durability != 1 || item.Workmanship != 1 || item.BuyPrice != 0 || item.RigGUID != "" || item.Container != "backpack" {
			t.Fatalf("reward item was not an ordinary new backpack item: %+v", item)
		}
		if item.WireClass == "cnolibojkao" {
			fields, err := FieldsFromJSON(item.FieldsJSON)
			if err != nil || len(fields) <= 5 || fields[5].Val == nil {
				t.Fatalf("reward rod lacks a stored rig: %v", err)
			}
			for _, index := range []int{0, 1, 2} {
				if fields[index].Val == nil {
					t.Fatalf("reward rod lacks factory part %d", index)
				}
			}
		}
		if _, err := s.ItemToTree(context.Background(), item); err != nil {
			t.Fatalf("reward item cannot be sent to the client: %v", err)
		}
	}
	if count, _ := s.acceptMails(ctx, r.MailIDs); count != 0 {
		t.Fatal("mail replay granted another kit")
	}
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM mail WHERE uid=1000001 AND read=1 AND items_json IS NULL`, 5)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_receipt WHERE mail_id IS NOT NULL`, 5)
	var payload string
	if err := db.SQL.QueryRow(`SELECT COALESCE(items_json,'[]') FROM mail WHERE id=?`, r.MailIDs[0]).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var attachments []store.MailItem
	if err := json.Unmarshal([]byte(payload), &attachments); err != nil || len(attachments) != 0 {
		t.Fatal("claimed mail retained attachments")
	}
}

func TestLevelRewardsCatchAndReleaseCommitTogether(t *testing.T) {
	for _, action := range []string{"catch", "release"} {
		t.Run(action, func(t *testing.T) {
			s, db := newProgressSrv(t)
			ctx := t.Context()
			const uid = int64(1000001)
			if _, err := db.EnsureProfile(ctx, uid); err != nil {
				t.Fatal(err)
			}
			oldXP := baseXp(10) - 1
			if _, err := db.SQL.Exec(`UPDATE profile SET level=9,xp=?,gold=0 WHERE uid=?`, oldXP, uid); err != nil {
				t.Fatal(err)
			}
			fish := &RolledFish{FishGUID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Species: "crucian", WeightG: 200, Grade: 1}
			perform := func() error {
				if action == "catch" {
					_, err := s.settleCatchOnce(ctx, uid, fish, 1)
					return err
				}
				_, _, err := s.releaseCatchOnce(ctx, uid, fish.FishGUID, 2)
				return err
			}
			if _, err := db.SQL.Exec(`CREATE TRIGGER reject_reward_mail BEFORE INSERT ON mail BEGIN SELECT RAISE(ABORT,'mail unavailable'); END`); err != nil {
				t.Fatal(err)
			}
			if err := perform(); err == nil {
				t.Fatal("reward failure did not fail progress transaction")
			}
			var level, xp, gold int64
			if err := db.SQL.QueryRow(`SELECT level,xp,gold FROM profile WHERE uid=?`, uid).Scan(&level, &xp, &gold); err != nil || level != 9 || xp != oldXP || gold != 0 {
				t.Fatalf("failed reward changed progress: %d/%d/%d %v", level, xp, gold, err)
			}
			assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_state`, 0)
			assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_receipt`, 0)
			assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM mail`, 0)
			if _, err := db.SQL.Exec(`DROP TRIGGER reject_reward_mail`); err != nil {
				t.Fatal(err)
			}
			if err := perform(); err != nil {
				t.Fatal(err)
			}
			if err := perform(); err != nil {
				t.Fatal(err)
			}
			assertLevelRewardCount(t, s, `SELECT level FROM profile WHERE uid=1000001`, 10)
			assertLevelRewardCount(t, s, `SELECT gold FROM profile WHERE uid=1000001`, 1000)
			assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM mail WHERE uid=1000001`, 1)
			assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_receipt WHERE uid=1000001`, 1)
		})
	}
}

func TestLevelRewardsReleasePreservesLegacyExperience(t *testing.T) {
	s, db := newProgressSrv(t)
	ctx := t.Context()
	const uid = int64(1000001)
	if _, err := db.EnsureProfile(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`UPDATE profile SET level=50,xp=123,gold=1234 WHERE uid=?`, uid); err != nil {
		t.Fatal(err)
	}
	const guid = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for i := range 2 {
		result, replayed, err := s.releaseCatchOnce(ctx, uid, guid, 10)
		if err != nil || result.Level != 50 || result.XP != 133 || result.LevelsGained != 0 || replayed != (i > 0) {
			t.Fatalf("release changed legacy XP semantics: result=%+v replayed=%v err=%v", result, replayed, err)
		}
	}
	assertLevelRewardCount(t, s, `SELECT xp FROM profile WHERE uid=1000001`, 133)
	assertLevelRewardCount(t, s, `SELECT gold FROM profile WHERE uid=1000001`, 1234)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM level_reward_receipt`, 0)
	assertLevelRewardCount(t, s, `SELECT COUNT(*) FROM mail`, 0)
}
