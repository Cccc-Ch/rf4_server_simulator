package gameserver

import "math"

// 北海 WaterTideManager，RVA9F5F40；曲线来自 level24:51203。
// 时钟推算值，不冒充客户端逐帧水面采样。
func nativeTideAt(level string, hour float64) (height, flow float64, ok bool) {
	if level != "level_012_north_sea" || math.IsNaN(hour) || math.IsInf(hour, 0) {
		return
	}
	phase := math.Mod(hour, 12) * math.Pi / 6
	q := clampFloat((math.Cos(phase)+1)/2, 0, 1)
	height = -1.5 + 1.5*q
	x := -q
	if math.Sin(phase) < 0 { // 涨潮：本体用剩余潮差作曲线输入。
		x = 1 - q
	}
	keys := [][4]float64{
		{-1, -1, 0.04726839065551758, 0},
		{-0.10000000149011612, -1, 0, -0.010065100155770779},
		{0.10000000149011612, 1, 0.018717864528298378, 0},
		{1, 1, 0, 0.03506108745932579},
	}
	for i := 1; i < len(keys); i++ {
		a, b := keys[i-1], keys[i]
		if x > b[0] {
			continue
		}
		dt := b[0] - a[0]
		t := clampFloat((x-a[0])/dt, 0, 1)
		t2, t3 := t*t, t*t*t
		flow = (2*t3-3*t2+1)*a[1] + (t3-2*t2+t)*dt*a[3] + (-2*t3+3*t2)*b[1] + (t3-t2)*dt*b[2]
		return height, flow, true
	}
	return height, 1, true
}
