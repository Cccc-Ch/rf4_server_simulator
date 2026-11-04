package gameserver

import (
	"math"
	"rf4server/internal/proto"
	"rf4server/internal/store"
	"strings"
)

func isCraftedLeader(row *store.Item) bool {
	return row != nil && row.WireClass == "oldfpclaabo" && strings.HasPrefix(row.ItemID, "leader_crafted_")
}
func leaderFieldsStrength(fields []proto.Field, durability float64) float64 {
	limit := 0.0
	for _, key := range []string{gearAttrLeaderIn, gearAttrLeaderLock, "baadhdmaonp"} {
		child := protoFieldAt(fields, "oldfpclaabo", key)
		if child == nil || child.Kind != proto.KindObj {
			continue
		}
		if si := fieldIdxByCS(child.Class, csDurability); si >= 0 && si < len(child.Fields) && child.Fields[si].Num != nil && proto.F32FromBits(uint32(u64Of(child.Fields[si].Num))) <= 0 {
			return 0
		}
		idx := fieldIdxByCS(child.Class, csStrength)
		if idx < 0 || idx >= len(child.Fields) {
			continue
		}
		v := float64(proto.F32FromBits(uint32(u64Of(child.Fields[idx].Num))))
		if !isFiniteStrength(v) {
			return 0
		}
		if limit == 0 || v < limit {
			limit = v
		}
	}
	return limit * math.Max(0, math.Min(1, durability))
}
func isFiniteStrength(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
