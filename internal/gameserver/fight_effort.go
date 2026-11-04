package gameserver

import "time"

// sampleFightTime 在状态锁内累计连续在线搏鱼时间。
func (st *FishState) sampleFightTime(now time.Time, sessionID uint64) {
	if !st.CastActive || !st.Hooked || st.Pending == nil || st.Resolved {
		st.fightTickAt = time.Time{}
		st.fightTickFish = ""
		return
	}
	if st.fightSessionID == sessionID && st.fightTickFish == st.Pending.FishGUID && !st.fightTickAt.IsZero() {
		elapsed := now.Sub(st.fightTickAt).Seconds()
		if elapsed > 0 && elapsed <= 5 {
			st.Pending.FightSeconds += elapsed
		}
	}
	st.fightTickAt = now
	st.fightTickFish = st.Pending.FishGUID
	st.fightSessionID = sessionID
}
