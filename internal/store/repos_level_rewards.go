package store

import (
	"context"
	"database/sql"
	"fmt"
)

// RecordLevelRewardHighWaterTx 只推进奖励进度，管理改级不补发奖励。
func RecordLevelRewardHighWaterTx(ctx context.Context, tx *sql.Tx, uid, level int64, now float64) error {
	if tx == nil || uid <= 0 {
		return fmt.Errorf("升级奖励角色无效")
	}
	if level < 1 {
		level = 1
	}
	if level > 58 {
		level = 58
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO level_reward_state
		(uid,baseline_level,high_water_level,created_ts,updated_ts) VALUES(?,?,?,?,?)
		ON CONFLICT(uid) DO UPDATE SET high_water_level=MAX(high_water_level,excluded.high_water_level),updated_ts=excluded.updated_ts`,
		uid, level, level, now, now)
	return err
}
