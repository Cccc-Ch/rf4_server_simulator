package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLevelRewardsUpgradePreservesLegacyPlayersAndBaseline(t *testing.T) {
	for _, version := range []int64{8, 17, 30} {
		t.Run(strconv.FormatInt(version, 10), func(t *testing.T) {
			db := openCatchHistoryDBAtVersion(t, version)
			if _, err := db.SQL.Exec(`INSERT INTO profile(uid,name,level,xp,silver,gold) VALUES(81,'历史角色',25,123,456,789);
				INSERT INTO item(guid,uid,item_id) VALUES('legacy-item',81,'legacy-rod');
				INSERT INTO mail(uid,text,read) VALUES(81,'历史邮件',0);
				INSERT INTO kv(k,v) VALUES('legacy-note','keep')`); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := db.Migrate(); err != nil {
					t.Fatal(err)
				}
			}
			assertCount(t, db, `SELECT COUNT(*) FROM profile WHERE uid=81 AND level=25 AND xp=123 AND silver=456 AND gold=789`, 1)
			assertCount(t, db, `SELECT COUNT(*) FROM level_reward_state WHERE uid=81 AND baseline_level=25 AND high_water_level=25`, 1)
			assertCount(t, db, `SELECT COUNT(*) FROM level_reward_receipt`, 0)
			assertCount(t, db, `SELECT COUNT(*) FROM item WHERE guid='legacy-item' AND item_id='legacy-rod'`, 1)
			assertCount(t, db, `SELECT COUNT(*) FROM mail WHERE uid=81 AND text='历史邮件' AND read=0`, 1)
			assertCount(t, db, `SELECT COUNT(*) FROM kv WHERE k='legacy-note' AND v='keep'`, 1)
			if _, err := db.SQL.Exec(`UPDATE profile SET level=40 WHERE uid=81`); err != nil {
				t.Fatal(err)
			}
			if err := db.Migrate(); err != nil {
				t.Fatal(err)
			}
			assertCount(t, db, `SELECT COUNT(*) FROM level_reward_state WHERE uid=81 AND baseline_level=25 AND high_water_level=25`, 1)
		})
	}
}

func TestLevelRewardsMigrationFailureRollsBackAndCanRetry(t *testing.T) {
	db := openCatchHistoryDBAtVersion(t, 30)
	if _, err := db.SQL.Exec(`INSERT INTO profile(uid,level,gold) VALUES(82,30,1200);
		CREATE TRIGGER reject_level_rewards BEFORE INSERT ON schema_migrations
		WHEN NEW.name='migrations/00031_level_rewards.sql'
		BEGIN SELECT RAISE(ABORT,'migration unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err == nil {
		t.Fatal("failed migration unexpectedly succeeded")
	}
	assertCount(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('level_reward_state','level_reward_receipt')`, 0)
	assertCount(t, db, `SELECT COUNT(*) FROM profile WHERE uid=82 AND level=30 AND gold=1200`, 1)
	if _, err := db.SQL.Exec(`DROP TRIGGER reject_level_rewards`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM level_reward_state WHERE uid=82 AND baseline_level=30 AND high_water_level=30`, 1)
}

func TestLevelRewardsEmptyDatabaseRestartAndAdminHighWater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "level-rewards.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM level_reward_state`, 0)
	if _, err := db.EnsureProfile(context.Background(), 83); err != nil {
		t.Fatal(err)
	}
	tx, err := db.SQL.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, level := range []int64{1, 50, 10, 99} {
		if err := RecordLevelRewardHighWaterTx(context.Background(), tx, 83, level, 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Migrate(); err != nil {
		t.Fatal(err)
	}
	assertCount(t, reopened, `SELECT COUNT(*) FROM level_reward_state WHERE uid=83 AND baseline_level=1 AND high_water_level=58`, 1)
	assertCount(t, reopened, `SELECT COUNT(*) FROM level_reward_receipt`, 0)
}
