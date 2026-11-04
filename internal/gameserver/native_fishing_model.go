package gameserver

import (
	"math"
	"time"

	"rf4server/internal/proto"
)

type reelDragCurve int

const (
	reelDragLocalLinear reelDragCurve = iota
	reelDragVanilla25034
)

// 两种客户端都以内部 29 锁轮；普通档曲线由明确的客户端适配设置选择。
func nativeReelDragN(baseKg, step, heat float64, curve reelDragCurve) (float64, bool) {
	step = clampFloat(step, 0, 29)
	if step >= 29 {
		return 0, true
	}
	maximum := math.Max(0, baseKg*verifiedForcePerGram*1000)
	drag := maximum * (step + 1) / 30
	if curve == reelDragVanilla25034 {
		maximum = math.Max(1, maximum)
		drag = 1 + (maximum-1)*math.Pow((step+1)/29, 1.5)
	}
	return drag * (1 - 0.05*clampFloat(heat, 0, 1)), false
}

// 27.f6 控制疲劳累积；capacity 已包含客户端抽取的 0.7..1.15 随机系数。
func nativeFishFatigue(spent, work, resistance, capacity float64) (float64, float64) {
	spent = math.Max(0, spent) + math.Max(0, work)*5*(1-clampFloat(resistance, 0, 1))
	if capacity <= 0 {
		return spent, 0.1
	}
	return spent, clampFloat(1-math.Pow(spent/capacity, 2), 0.1, 1)
}

// 14/8 是客户端汇总值；不把平均档位当作逐帧操作，也不在服务端再扣一次疲劳。
type nativeFishingReport struct {
	ReceivedAt                    time.Time
	SessionID, CastGeneration     uint64
	FishGUID, RodGUID             string
	WindowSeconds, FightSeconds   float64
	FatigueSpent, ForceFactor     float64
	EndpointDistanceM, RigLengthM float64
	DragMean, DragMin, DragMax    int
	ManualTurns, ElectricTurns    float64
	ReleasedM                     float64
	ReelMovementKnown             bool
	GameHour                      float64
	Analysis                      fishFightAnalysis
	Components                    []fishingComponentInput
}

// 这些原版钓组的 phcpbddiocc 固定段为零；其他钓组不能用连接器总长冒充主线长。
func nativeMainLineOut(rigType int64, rigLength float64, fixedLengths ...*float64) (float64, bool) {
	if rigLength < 0 || math.IsNaN(rigLength) || math.IsInf(rigLength, 0) {
		return 0, false
	}
	if len(fixedLengths) > 0 && fixedLengths[0] != nil {
		fixed := *fixedLengths[0]
		if math.IsNaN(fixed) || math.IsInf(fixed, 0) || fixed < 0 || rigLength+1e-4 < fixed {
			return 0, false
		}
		return math.Max(0, rigLength-fixed), true
	}
	switch rigType {
	case 5, 6, 7, 8, 11, 18, 22, 33, 35, 53, 54:
		return math.Max(0, rigLength), true
	default:
		return 0, false
	}
}

func nativeReportFloat(o *proto.Value, i int) (float64, bool) {
	if o == nil || i < 0 || i >= o.FieldCount() || o.Field(i).Num == nil {
		return 0, false
	}
	v := float64(proto.F32FromBits(uint32(*o.Field(i).Num)))
	return v, !math.IsNaN(v) && !math.IsInf(v, 0)
}

func decodeNativeFishingReport(tree *proto.Value) (nativeFishingReport, bool) {
	var out nativeFishingReport
	o := findObj(tree, "cdhlhfejmai")
	if o == nil || o.FieldCount() != 25 {
		return out, false
	}
	for _, field := range []struct {
		idx int
		dst *float64
	}{{0, &out.WindowSeconds}, {3, &out.FatigueSpent}, {4, &out.ForceFactor}, {19, &out.EndpointDistanceM}, {20, &out.RigLengthM}, {23, &out.FightSeconds}} {
		v, ok := nativeReportFloat(o, field.idx)
		if !ok || v < 0 {
			return nativeFishingReport{}, false
		}
		*field.dst = v
	}
	if out.ForceFactor < 0.1-1e-6 || out.ForceFactor > 1 || o.Field(14).Num == nil || o.Field(15).Num == nil {
		return nativeFishingReport{}, false
	}
	out.DragMean = int(u32Of(o.Field(14).Num))
	packed := u32Of(o.Field(15).Num)
	out.DragMin, out.DragMax = int(packed&0xffff), int(packed>>16)
	if out.DragMin > out.DragMean || out.DragMean > out.DragMax || out.DragMax > 29 {
		return nativeFishingReport{}, false
	}
	// Reel+D4=手摇转数，D0=电动转数，C0=泄出米数；窗口结束清零。
	manual, manualOK := nativeReportFloat(o, 10)
	electric, electricOK := nativeReportFloat(o, 11)
	released, releasedOK := nativeReportFloat(o, 12)
	if manualOK && electricOK && releasedOK && manual >= 0 && electric >= 0 && released >= 0 {
		out.ManualTurns, out.ElectricTurns, out.ReleasedM = manual, electric, released
		out.ReelMovementKnown = true
	}
	return out, true
}

func recordNativeFishingReport(ctx *Ctx) bool {
	report, ok := decodeNativeFishingReport(ctx.Tree)
	guids := deepGUIDs(ctx.Tree)
	if !ok || len(guids) == 0 {
		return false
	}
	srv := ctx.Sess.Srv
	st := srv.stateFor(ctx.Sess.Userid)
	if st == nil {
		return false
	}
	st.mu.Lock()
	if !st.CastActive || st.Resolved || st.Pending == nil || st.GearGuid != guids[0] {
		st.mu.Unlock()
		return false
	}
	report.RodGUID, report.FishGUID = guids[0], st.Pending.FishGUID
	report.ReceivedAt, report.SessionID, report.CastGeneration = time.Now(), ctx.Sess.ID, st.castGeneration
	report.GameHour = sessionGameMinuteAt(ctx.Sess, report.ReceivedAt) / 60
	if old := st.NativeReport; old != nil && old.CastGeneration == report.CastGeneration && old.FishGUID == report.FishGUID && old.SessionID == report.SessionID && report.FightSeconds < old.FightSeconds {
		st.mu.Unlock()
		return false
	}
	fish, loc, spot := *st.Pending, st.Loc, fightWaterSpot{Spot: st.Spot, BaitDepthCM: st.DepthCM, CastX: st.CastPosX, CastZ: st.CastPosZ, HasCastPosition: st.HasCastPos}
	spot.GameHour = &report.GameHour
	st.mu.Unlock()
	if srv.DB != nil {
		cctx := ctx.Sess.pushCtx()
		spec := srv.loadGearSpecForRig(cctx, srv.rigGearIDs(cctx, ctx.Sess.Userid, report.RodGUID), report.RodGUID)
		srv.applyCaughtHook(cctx, &fish, &spec)
		report.Components = spec.Components
		spec.Water = srv.fightWaterForRig(cctx, ctx.Sess.Userid, loc, spec.RigType, spot)
		report.Analysis = srv.analyzeFishFight(&fish, spec, &report)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.CastActive || st.Resolved || st.Pending == nil || st.Pending.FishGUID != report.FishGUID || st.castGeneration != report.CastGeneration || st.GearGuid != report.RodGUID {
		return false
	}
	if old := st.NativeReport; old != nil && old.ReceivedAt.After(report.ReceivedAt) {
		return false
	}
	st.NativeReport = &report
	st.Hooked = true
	return true
}
