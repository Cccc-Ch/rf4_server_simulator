package gameserver

import (
	"math"
	"time"
)

func (s *Server) weatherFishActivity(species string, c fishRollContext) float64 {
	if s.tunableF("fish_weather_enabled", 0) != 1 {
		return 1
	}
	_, hour := worldDayAndHour(c.now.Unix())
	return fishWeatherActivity(species, c.weather, hour, c.depthCM)
}

// 等待节奏参考本水域鱼种；实际吃口仍逐鱼判定，不把低活跃归一化掉。
func (s *Server) weatherBiteDelayFactor(st *FishState, weather fishingWeather, now time.Time) float64 {
	if st == nil || !weather.known || s.tunableF("fish_weather_enabled", 0) != 1 {
		return 1
	}
	weights := map[string]float64{}
	baitLoc := st.Loc
	if s.DB != nil {
		ctx := s.dbCtx()
		loc := s.DB.ResolveStatLoc(ctx, st.Loc)
		if loc != "" {
			baitLoc = loc
			if roster, err := s.DB.LocRoster(ctx, loc); err == nil {
				for _, fish := range roster {
					weights[fish.FishKey] = fish.Catches
				}
			}
			if overrides, err := s.DB.FishWeightOverrides(ctx, loc); err == nil {
				for key, setting := range overrides {
					if setting.Enabled && setting.Weight > 0 {
						weights[key] = setting.Weight
					} else {
						weights[key] = 0
					}
				}
			}
		}
	}
	if len(weights) == 0 {
		pool := fishPools[st.Loc]
		if len(pool) == 0 {
			pool = defaultFishPool
		}
		for _, fish := range pool {
			weights[fish.species]++
		}
	}
	_, hour := worldDayAndHour(now.Unix())
	keys, values := make([]string, 0, len(weights)), make([]float64, 0, len(weights))
	for species, weight := range weights {
		keys, values = append(keys, species), append(values, weight)
	}
	if s.tunableF("fish_bait_rules_enabled", 0) == 1 {
		baits := []string{st.Bait}
		if s.DB != nil && st.GearGuid != "" {
			baits = s.currentBaits(s.dbCtx(), st.GearGuid)
		}
		keys, values = s.filterFishBaitSet(baitLoc, baits, keys, values, now, fishRollContext{rigType: st.RigType})
	}
	var total, active float64
	for i, species := range keys {
		weight := values[i]
		if weight > 0 {
			total += weight
			active += weight * fishWeatherActivity(species, weather, hour, st.DepthCM)
		}
	}
	if total <= 0 || active <= 0 {
		return 1
	}
	return 1 / math.Sqrt(active/total)
}

func (s *Server) emptyWeatherBiteLocked(sess *Session, st *FishState, now time.Time) {
	st.PushSent = false
	st.Pending = nil
	st.Resolved = true
	if s.tunableF("fish_weather_enabled", 0) != 1 || rigTypeLureFamily[st.RigType] || st.Hooked {
		return
	}
	if s.DB != nil && st.GearGuid != "" {
		if rod, _ := s.DB.GetItemByGUID(s.dbCtx(), st.GearGuid); rod != nil && s.itemBrokenForMount(s.dbCtx(), rod) {
			return
		}
	}
	st.Resolved = false
	delay := s.biteDelayInWeather(st, sess.weatherAt(st.Loc, now), now, st.Bonus)
	s.armBiteTimerLocked(sess.Userid, st, now.Add(time.Duration(delay*float64(time.Second))))
}
