package gameserver

type fishFightModelResult struct {
	WeightG                                                     int64
	LengthM, ForceN, Stamina, FatigueResistance, CurvePeakRatio float64
	Statics                                                     map[int]float64
}

// 发鱼、负荷分析及磨损使用同一组鱼体参数。
func (s *Server) fishFightModel(f *RolledFish) fishFightModelResult {
	if f == nil {
		return fishFightModelResult{}
	}
	model := fishFightModelResult{
		WeightG: f.WeightG, LengthM: f.LengthM,
		ForceN: s.fishForceFor(f), Stamina: s.fishStaminaFor(f),
		FatigueResistance: s.fishInitialStaminaRatio(), CurvePeakRatio: s.fishCurvePeakRatio(f.Species),
		Statics: fishFightStatics(f),
	}
	if model.LengthM <= 0 && model.WeightG > 0 {
		k, p := 0.0355, 1.0/3
		if s.DB != nil {
			k, p = s.DB.SpeciesLengthKP(s.dbCtx(), f.Species)
		}
		model.LengthM = fishBodyLength(f.Species, float64(model.WeightG), k, p)
	}
	return model
}

func fishFightStatics(f *RolledFish) map[int]float64 {
	st := officialSpeciesStatics[f.Species]
	if st == nil {
		st = officialSpeciesStatics[performanceForFish(f.Species).motion]
	}
	if st == nil {
		return fallbackFightStatics(float64(f.WeightG))
	}
	w := float64(f.WeightG)
	return map[int]float64{
		9: staticPow(st.f9, w), 10: staticPow(st.f10, w), 11: staticPow(st.f11, w),
		12: 0, 13: gradeMapVal(st.f13, f.Grade), 14: gradeMapVal(st.f14, f.Grade),
		15: gradeMapVal(st.f15, f.Grade), 16: 1, 17: gradeMapVal(st.f17, f.Grade), 18: 1,
		19: staticPow(st.f19, w), 20: st.f20,
	}
}
