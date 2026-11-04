package gameserver

import (
	"context"
	"math"

	"rf4server/internal/proto"
	"rf4server/internal/store"
)

// 电机“功率”在本体中乘 9.8 后作为承载力，不是瓦特。
type reelMotorSpec struct {
	Present          bool
	PullKg, SpeedMPM float64
}

func (s *Server) reelMotorFromRow(ctx context.Context, row *store.Item) reelMotorSpec {
	fields, err := FieldsFromJSON(row.FieldsJSON)
	if err != nil {
		return reelMotorSpec{}
	}
	child, def, _, ok := s.selectedReelPart(ctx, fields, 81)
	if !ok {
		return reelMotorSpec{}
	}
	read := func(cs, attr string) float64 {
		if i := fieldIdxByCS(child.Class, cs); i >= 0 && i < child.FieldCount() && child.Fields[i].Num != nil {
			v := float64(proto.F32FromBits(uint32(*child.Fields[i].Num)))
			if !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
				return v
			}
		}
		return itemDefF32(def, attr)
	}
	return reelMotorSpec{Present: true, PullKg: read("npnlhobgfen", "afmboidbbaj"), SpeedMPM: read("mbbdpnidheg", "fgkfkgimfgo")}
}

// RVA 9544C0：仅在给定张力和控制输入时求电动速度；不推断玩家已开启电动。
func nativeMotorSpeedMPS(motor reelMotorSpec, tensionN, control float64) float64 {
	if !motor.Present || motor.PullKg <= 0 || motor.SpeedMPM <= 0 {
		return 0
	}
	load := clampFloat(math.Max(0, tensionN)/(motor.PullKg*9.8+0.5), 0, 1)
	available := 1 - math.Pow(load, 1.15)
	throttle := 0.35 + 0.65*clampFloat((control-4)/8, 0, 1)
	return motor.SpeedMPM / 60 * math.Min(available, throttle)
}

// RVA 954B90：容量由线径分段决定，不按轮型选一个固定除数。
func nativeSpoolCapacityM(coefficient, diameterMM float64) float64 {
	if coefficient <= 0 || diameterMM <= 0 {
		return 0
	}
	k, exponent := 0.16588345, 1.65
	if diameterMM <= 0.25 {
		k = 0.10153155
	} else if diameterMM < 0.8 {
		k = 0.10153155
		exponent += 2.2 * clampFloat((diameterMM-0.25)/0.55, 0, 1)
	}
	return math.Max(5, math.Floor(k*coefficient/math.Pow(diameterMM, exponent)))
}
