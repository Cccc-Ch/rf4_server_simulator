package gameserver

import "math"

// 负载比是锁轮直传时的潜在负荷，不等于客户端实际力度条。
type fishFightAnalysis struct {
	ForceN, PeakForceN           float64
	InitialStamina               float64
	FatigueCapacityMin           float64
	FatigueCapacityMax           float64
	FatigueResistance            float64
	NativeReportUsed             bool
	ReportedRigLengthM           float64
	ReportedEndpointDistanceM    float64
	LineOutM                     float64
	LineOutKnown                 bool
	EndurancePerNewton           float64
	GearLimitKg                  float64
	WaterDragN, PressurePa       float64
	WaterAlongN, WaterCrossN     float64
	CurrentDirectionKnown        bool
	DepthBlocked                 bool
	PotentialLoad, PotentialPeak float64
	MotorPresent                 bool
	MotorPullKg, MotorMaxMPS     float64
	MotorLoadedMaxMPS            float64 // 电动开启、最高控制量下的理论速度；不是实际操作。
	ReelMovementKnown            bool
	ManualTurns, ElectricTurns   float64
	ReleasedM                    float64
	ElectricUsedInWindow         bool
	ComponentCount               int
	CurrentSource                string
}

func (s *Server) analyzeFishFight(f *RolledFish, spec gearPhysicsSpec, reports ...*nativeFishingReport) fishFightAnalysis {
	model := s.fishFightModel(f)
	force := model.ForceN
	var report *nativeFishingReport
	if len(reports) > 0 && reports[0] != nil && f != nil && reports[0].FishGUID == f.FishGUID {
		report = reports[0]
		force *= clampFloat(report.ForceFactor, 0.1, 1)
		if lineOut, known := nativeMainLineOut(spec.RigType, report.RigLengthM, spec.FixedRigLengthM); known {
			spec.LineOutM = &lineOut
		}
	}
	out := fishFightAnalysis{
		ComponentCount: len(spec.Components), CurrentSource: spec.Water.CurrentSource,
		ForceN: force, PeakForceN: force * model.CurvePeakRatio,
		InitialStamina:     model.Stamina,
		FatigueCapacityMin: model.Stamina * 0.7, FatigueCapacityMax: model.Stamina * 1.15,
		FatigueResistance: model.FatigueResistance, NativeReportUsed: report != nil,
		GearLimitKg:  gearBreakLimitKg(spec),
		MotorPresent: spec.Motor.Present, MotorPullKg: spec.Motor.PullKg, MotorMaxMPS: spec.Motor.SpeedMPM / 60,
	}
	if report != nil {
		out.ReportedRigLengthM, out.ReportedEndpointDistanceM = report.RigLengthM, report.EndpointDistanceM
		out.ReelMovementKnown = report.ReelMovementKnown
		if report.ReelMovementKnown {
			out.ManualTurns, out.ElectricTurns, out.ReleasedM = report.ManualTurns, report.ElectricTurns, report.ReleasedM
			out.ElectricUsedInWindow = report.ElectricTurns > 0
		}
	}
	if spec.LineOutM != nil {
		out.LineOutM, out.LineOutKnown = *spec.LineOutM, true
	}
	budget := model.Stamina / math.Max(1e-9, 5*(1-model.FatigueResistance))
	if force > 0 {
		out.EndurancePerNewton = budget / force
	}
	tension, peakTension := force, out.PeakForceN
	if f != nil {
		water := evaluateFightWater(spec, f.Species, float64(model.WeightG), model.LengthM)
		out.WaterDragN, out.PressurePa, out.DepthBlocked = water.DragN, water.PressurePa, water.DepthBlocked
		out.WaterAlongN, out.WaterCrossN, out.CurrentDirectionKnown = water.AlongPullN, water.CrossPullN, water.DirectionKnown
		tension, peakTension = waterCombinedForce(force, water, true), waterCombinedForce(out.PeakForceN, water, true)
		if tension > 0 {
			out.EndurancePerNewton = budget / tension
		}
	}
	in := tackleLoadContext{ForceN: tension, PeakN: peakTension, Locked: true, LineOutM: out.LineOutM, LineOutUnknown: !out.LineOutKnown}
	if report != nil {
		// 汇总窗口内档位不变时才换算泄力，变档窗口保留锁轮潜在负荷。
		if report.DragMin == report.DragMax {
			step := float64(report.DragMean)
			in.NativeDragStep, in.Locked = &step, step == 29
		}
	}
	load := evaluateTackleLoad(spec, in)
	out.PotentialLoad, out.PotentialPeak = load.LoadRatio, load.PeakRatio
	out.MotorLoadedMaxMPS = nativeMotorSpeedMPS(spec.Motor, load.TensionN, 12)

	return out
}

func (s *Server) fishCurvePeakRatio(species string) float64 {
	peak := 1.0
	rows := s.fightCurveRows(species)
	if len(rows) > 0 && rows[0][8] > 0 {
		for _, row := range rows {
			peak = math.Max(peak, math.Max(row[8], row[9])/rows[0][8])
		}
	}
	return peak
}
