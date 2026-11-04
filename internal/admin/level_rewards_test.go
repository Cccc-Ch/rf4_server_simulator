package admin

import (
	"net/http/httptest"
	"rf4server/internal/config"
	"rf4server/internal/logging"
	"strings"
	"testing"
)

func TestLevelRewardAdminChangesAdvanceHighWaterAtomically(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	if _, err := db.EnsureProfile(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`UPDATE profile SET level=10,xp=123,gold=789 WHERE uid=42`); err != nil {
		t.Fatal(err)
	}
	s := NewServer(config.Admin{JWTSecret: "reward-test"}, db, logging.Discard(), WebFS())
	call := func(level string) int {
		r := httptest.NewRequest("POST", "/api/players/update", strings.NewReader(`{"uid":42,"level":`+level+`}`))
		w := httptest.NewRecorder()
		s.handlePlayerUpdate(w, r)
		return w.Code
	}
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_reward_profile BEFORE UPDATE ON profile BEGIN SELECT RAISE(ABORT,'profile unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if code := call("20"); code != 500 {
		t.Fatalf("failed profile status=%d", code)
	}
	var count int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM level_reward_state`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed update advanced reward state: %d %v", count, err)
	}
	if _, err := db.SQL.Exec(`DROP TRIGGER reject_reward_profile`); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"20", "10"} {
		if code := call(level); code != 200 {
			t.Fatalf("admin update status=%d", code)
		}
	}
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM level_reward_state WHERE uid=42 AND baseline_level=20 AND high_water_level=20`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("admin reward high-water=%d %v", count, err)
	}
	var gold, receipts, mails int64
	if err := db.SQL.QueryRow(`SELECT gold,(SELECT COUNT(*) FROM level_reward_receipt),(SELECT COUNT(*) FROM mail) FROM profile WHERE uid=42`).Scan(&gold, &receipts, &mails); err != nil || gold != 789 || receipts != 0 || mails != 0 {
		t.Fatalf("admin update granted rewards: gold=%d receipts=%d mails=%d %v", gold, receipts, mails, err)
	}
}
