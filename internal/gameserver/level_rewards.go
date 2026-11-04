package gameserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"rf4server/internal/store"
)

type levelRewardResult struct {
	Gold    int64
	MailIDs []int64
}

type levelRewardGift struct {
	itemID string
	qty    int64
}

// 非计件装备逐件列出；线按定义生成整盘，不把数量当作米数。
var levelRewardGifts = map[int64][]levelRewardGift{
	10: {
		{"match_10026_M110M", 1}, {"spin_5184", 1}, {"mono_15095", 1},
		{"fc_leader_40048", 1}, {"fc_leader_40048", 1},
		{"clas_20163", 1}, {"clas_20163", 1}, {"clas_20163", 1},
		{"bob_25012", 1}, {"worm_01", 60},
	},
	15: {
		{"spin_10034_S68MH", 1}, {"spin_5186", 1}, {"braid_15007", 1},
		{"fc_leader_40049", 1}, {"spoon_lures_1736", 1}, {"spoon_lures_1737", 1},
	},
	20: {
		{"feeder_10050_FD360MH", 1}, {"spin_5119", 1}, {"braid_15284", 1},
		{"fc_leader_40049", 1}, {"fc_leader_40049", 1},
		{"clas_20163", 1}, {"clas_20163", 1}, {"clas_20163", 1}, {"clas_20163", 1},
		{"feeder_30028", 1}, {"syb_quiv_1.0", 1}, {"worm_01", 60}, {"corn", 60},
	},
	25: {
		{"carp_10046_360H", 1}, {"spin_5124", 1}, {"braid_15144", 1},
		{"fc_leader_40050", 1}, {"fc_leader_40050", 1},
		{"c_hook_20713", 1}, {"c_hook_20713", 1}, {"c_hook_20713", 1}, {"c_hook_20713", 1},
		{"arsinker_30082", 5}, {"corn", 90},
	},
	30: {
		{"spin_5119", 1}, {"braid_15284", 1}, {"braid_15144", 1},
		{"fc_leader_40050", 1}, {"fc_leader_40050", 1},
		{"c_hook_20713", 1}, {"c_hook_20713", 1}, {"c_hook_20713", 1}, {"c_hook_20713", 1},
		{"worm_01", 90}, {"corn", 90},
	},
}

func levelRewardGold(level int64) int64 {
	switch {
	case level < 2 || level > maxLevel:
		return 0
	case level < 10:
		return 500
	case level < 20:
		return 1000
	case level < 30:
		return 2000
	case level < 40:
		return 3000
	case level < 50:
		return 4000
	default:
		return 5000
	}
}

// applyLevelRewardsTx 与经验、等级共用事务；邮件领取沿用普通物品生成流程。
func (s *Server) applyLevelRewardsTx(ctx context.Context, tx *sql.Tx, uid, oldLevel, newLevel int64, now float64) (levelRewardResult, error) {
	var result levelRewardResult
	if err := store.RecordLevelRewardHighWaterTx(ctx, tx, uid, oldLevel, now); err != nil {
		return result, err
	}
	if newLevel > maxLevel {
		newLevel = maxLevel
	}
	var highWater int64
	if err := tx.QueryRowContext(ctx, `SELECT high_water_level FROM level_reward_state WHERE uid=?`, uid).Scan(&highWater); err != nil {
		return result, err
	}
	var enabled string
	err := tx.QueryRowContext(ctx, `SELECT value FROM tunables WHERE key='level_rewards_enabled'`).Scan(&enabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if err == nil && enabled != "1" || newLevel <= highWater {
		return result, store.RecordLevelRewardHighWaterTx(ctx, tx, uid, newLevel, now)
	}
	for level := highWater + 1; level <= newLevel; level++ {
		gold := levelRewardGold(level)
		inserted, err := tx.ExecContext(ctx, `INSERT INTO level_reward_receipt(uid,level,gold,created_ts)
			VALUES(?,?,?,?) ON CONFLICT(uid,level) DO NOTHING`, uid, level, gold, now)
		if err != nil {
			return levelRewardResult{}, err
		}
		count, err := inserted.RowsAffected()
		if err != nil {
			return levelRewardResult{}, err
		}
		if count == 0 {
			continue
		}
		if gifts := levelRewardGifts[level]; len(gifts) > 0 {
			items := make([]store.MailItem, 0, len(gifts))
			for _, gift := range gifts {
				items = append(items, store.MailItem{GUID: NewItemGUID(), ItemID: gift.itemID, Qty: gift.qty})
			}
			payload, err := json.Marshal(items)
			if err != nil {
				return levelRewardResult{}, err
			}
			mail, err := tx.ExecContext(ctx, `INSERT INTO mail(uid,sender,kind,text,items_json,created_ts,read)
				VALUES(?,0,1,?,?,?,0)`, uid, fmt.Sprintf("%d级升级装备礼包", level), string(payload), now)
			if err != nil {
				return levelRewardResult{}, err
			}
			mailID, err := mail.LastInsertId()
			if err != nil {
				return levelRewardResult{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE level_reward_receipt SET mail_id=? WHERE uid=? AND level=?`, mailID, uid, level); err != nil {
				return levelRewardResult{}, err
			}
			result.MailIDs = append(result.MailIDs, mailID)
		}
		result.Gold += gold
	}
	if result.Gold > 0 {
		var currentGold int64
		if err := tx.QueryRowContext(ctx, `SELECT gold FROM profile WHERE uid=?`, uid).Scan(&currentGold); err != nil {
			return levelRewardResult{}, err
		}
		if currentGold > math.MaxInt64-result.Gold {
			return levelRewardResult{}, fmt.Errorf("升级奖励金币超出范围")
		}
		if err := applyWalletTx(ctx, tx, uid, 0, result.Gold, "level_reward", "", "", fmt.Sprintf("升级奖励 %d->%d", oldLevel, newLevel)); err != nil {
			return levelRewardResult{}, err
		}
	}
	if err := store.RecordLevelRewardHighWaterTx(ctx, tx, uid, newLevel, now); err != nil {
		return levelRewardResult{}, err
	}
	return result, nil
}
