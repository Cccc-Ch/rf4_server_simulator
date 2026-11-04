package gameserver

import (
	"context"
	"math"

	"rf4server/internal/store"
)

type catchWearModel struct {
	fish     *RolledFish
	analysis fishFightAnalysis
	spec     gearPhysicsSpec
	ids      rigGearIDs
	rodGUID  string
}

func (s *Server) catchWearForFish(ctx context.Context, uid int64, rodGUID string, fish *RolledFish) catchWearModel {
	out := catchWearModel{fish: fish, rodGUID: rodGUID}
	if fish == nil {
		return out
	}
	out.ids = s.rigGearIDs(ctx, uid, rodGUID)
	out.spec = s.loadGearSpecForRig(ctx, out.ids, rodGUID)
	s.applyCaughtHook(ctx, fish, &out.spec)
	state := s.stateFor(uid)
	state.mu.Lock()
	loc, spot := state.Loc, fightWaterSpot{Spot: state.Spot, BaitDepthCM: state.DepthCM, CastX: state.CastPosX, CastZ: state.CastPosZ, HasCastPosition: state.HasCastPos}
	if report := state.NativeReport; report != nil && report.FishGUID == fish.FishGUID && report.RodGUID == rodGUID && report.CastGeneration == state.castGeneration {
		hour := report.GameHour
		spot.GameHour = &hour
		if length, known := nativeMainLineOut(out.spec.RigType, report.RigLengthM, out.spec.FixedRigLengthM); known {
			out.spec.LineOutM = &length
		}
	}
	state.mu.Unlock()
	out.spec.Water = s.fightWaterForRig(ctx, uid, loc, out.spec.RigType, spot)
	// 结算磨损仍用锁轮潜在负荷；最后一帧的低负荷不能替代整场受力。
	out.analysis = s.analyzeFishFight(fish, out.spec)
	return out
}

func (s *Server) catchWearPartStrength(ctx context.Context, model catchWearModel, row *store.Item, key string) float64 {
	switch row.GUID {
	case model.rodGUID:
		return model.spec.RodPowerKg
	case model.ids.LineGUID:
		return model.spec.LineStrengthKg
	case model.ids.LeaderGUID:
		return model.spec.LeaderStrengthKg
	case model.ids.LeadcoreGUID:
		return model.spec.LeadcoreStrengthKg
	case model.ids.ConnectorGUID:
		return model.spec.ConnectorStrengthKg
	case model.ids.ReelGUID:
		return model.spec.ReelDragKg
	}
	if key == "hook" {
		if def, err := s.itemDefForRow(ctx, row); err == nil {
			return itemDefF32(def, gearAttrMaxLoad) * clampFloat(row.Durability, 0, 1)
		}
	}
	return 0
}

// 未收到连续搏鱼计时的旧存档按体力估计工作量；不把等口或离线时间算进去。
func catchWearMultiplier(analysis fishFightAnalysis, partKg, fightSeconds float64) float64 {
	if partKg <= 0 || analysis.GearLimitKg <= 0 || analysis.ForceN <= 0 {
		return 1
	}
	load := analysis.PotentialLoad * analysis.GearLimitKg / partKg
	peak := analysis.PotentialPeak * analysis.GearLimitKg / partKg
	strain := 0.75*load*load + 0.25*peak*peak
	referenceStamina := verifiedStaminaPerGram * 1000 * defaultFishStaminaScale
	work := math.Sqrt(math.Max(0, analysis.InitialStamina) / referenceStamina)
	// 本地平衡近似：体力决定起鱼基础工作，连续搏鱼时间增加工作量。
	if fightSeconds > 0 && !math.IsNaN(fightSeconds) && !math.IsInf(fightSeconds, 0) {
		work *= 1 + fightSeconds/30
	}
	multiplier := (0.1 + 0.9*strain) * work
	if math.IsNaN(multiplier) {
		return 1
	}
	// 本地磨损平衡：限制单次损耗，GM 巨物或异常长搏鱼不能一次扣光装备。
	return clampFloat(multiplier, 0.02, 20)
}

func (s *Server) catchWearAmount(ctx context.Context, model catchWearModel, row *store.Item, key string) (float64, map[string]any) {
	detail := map[string]any{"category": key}
	amount := wearPerCatch[key]
	if model.fish == nil || amount <= 0 {
		return amount, detail
	}
	partKg := s.catchWearPartStrength(ctx, model, row, key)
	if partKg <= 0 {
		return amount, detail
	}
	factor := catchWearMultiplier(model.analysis, partKg, model.fish.FightSeconds)
	detail["wear_model"] = "potential_load"
	detail["fish_weight_g"] = model.fish.WeightG
	detail["fish_force_n"] = model.analysis.ForceN
	detail["part_limit_kg"] = partKg
	detail["wear_factor"] = factor
	if model.fish.FightSeconds > 0 {
		detail["fight_seconds"] = model.fish.FightSeconds
	} else {
		detail["work_source"] = "initial_stamina"
	}
	return amount * factor, detail
}
