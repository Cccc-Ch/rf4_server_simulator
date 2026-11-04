package gameserver

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestNativeFlowFormatsCoordinatesAndMissingSamples(t *testing.T) {
	for _, format := range []int{3, 4, 17, 20} {
		stride := map[int]int{3: 3, 4: 4, 17: 8, 20: 16}[format]
		f := nativeFlowField{Width: 2, Height: 2, Format: format, OriginX: -5, OriginZ: 10, SizeX: 10, SizeZ: 20, MaxSpeed: 2, Pixels: make([]byte, 4*stride)}
		p := 2 * stride // 左上像素；不同于左下黑色像素。
		switch format {
		case 3, 4:
			copy(f.Pixels[p:], []byte{255, 0, 255})
		case 17:
			binary.LittleEndian.PutUint16(f.Pixels[p:], 0x3c00)
			binary.LittleEndian.PutUint16(f.Pixels[p+4:], 0x3c00)
		case 20:
			binary.LittleEndian.PutUint32(f.Pixels[p:], math.Float32bits(1))
			binary.LittleEndian.PutUint32(f.Pixels[p+8:], math.Float32bits(1))
		}
		for _, rgb := range []bool{false, true} {
			f.RGB = rgb
			x, z, ok := f.sample(-4, 25)
			wantZ := 2.0
			if rgb {
				wantZ = -2
			}
			if !ok || x != 2 || z != wantZ {
				t.Fatalf("format=%d rgb=%v: %g %g %v", format, rgb, x, z, ok)
			}
		}
		if x, z, ok := f.sample(-4, 11); !ok || x != 0 || z != 0 {
			t.Fatal("黑色流图应为已知静水")
		}
		for _, point := range [][2]float64{{-6, 11}, {5, 11}, {-4, 30}, {math.NaN(), 20}, {math.Inf(1), 20}} {
			if _, _, ok := f.sample(point[0], point[1]); ok {
				t.Fatal("边界外被外推为有效流场", point)
			}
		}
		f.Pixels = nil
		if _, _, ok := f.sample(-4, 11); ok {
			t.Fatal("缺失纹理被当成静水")
		}
	}
	if nativeHalfFloat(1) != math.Ldexp(1, -24) || nativeHalfFloat(0xbc00) != -1 || !math.IsInf(nativeHalfFloat(0x7c00), 1) || !math.IsNaN(nativeHalfFloat(0x7c01)) {
		t.Fatal("半精度解码错误")
	}
}

func TestNativeFlowSceneAssetsAndReferenceSamples(t *testing.T) {
	for _, tc := range []struct {
		level        string
		x, z, vx, vz float64
	}{
		{"level_008_sura_river", 200, 200, -0.2313725490196078, 0.2941176470588235},
		{"level_009_ahtuba", 750, 750, 0.22352942064696668, -0.3082353063658172},
		{"level_010_belaya", 375, 375, -0.3156862503173308, 0.6231373323178744},
		{"level_015_tunguska", 600, 600, -0.5058823529411764, -0.8509803921568628},
		{"level_016_yama", 750, 750, 0.615686274509804, 0.4509803921568627},
		{"level_012_north_sea", 2500, 543, -0.36941177938498704, 0.3929411920846677},
		{"level_005_volhov", 500, 500, -0.0505882373043135, -0.16823530080271704},
	} {
		f, exists := nativeFlowForLevel(tc.level)
		x, z, ok := f.sample(tc.x, tc.z)
		if !exists || !ok || math.Abs(x-tc.vx) > 1e-12 || math.Abs(z-tc.vz) > 1e-12 {
			t.Fatalf("%s: %g %g %v", tc.level, x, z, ok)
		}
	}
	if len(nativeFlows.byLevel) != 9 {
		t.Fatal("地图流场遗漏", len(nativeFlows.byLevel))
	}
	if f, ok := nativeFlowForLevel("level_002_kirzah_river"); !ok || f.Format != 17 || f.Width != 2048 || f.OriginX != -500 || f.OriginZ != -500 {
		t.Fatal("半精度纹理被降为八位或坐标偏移丢失")
	}
	if _, ok := nativeFlowForLevel("unknown"); ok {
		t.Fatal("未知地图错误套用流场")
	}
}

func TestNativeTideAndConfiguredFlowPriority(t *testing.T) {
	_, ebb, ok := nativeTideAt("level_012_north_sea", 3)
	_, flood, _ := nativeTideAt("level_012_north_sea", 9)
	if !ok || ebb != -1 || flood != 1 {
		t.Fatal("涨落潮方向错误", ebb, flood)
	}
	for h := 0.0; h < 24; h += 0.125 {
		level, flow, _ := nativeTideAt("level_012_north_sea", h)
		periodicLevel, periodicFlow, _ := nativeTideAt("level_012_north_sea", h+12)
		if level < -1.5 || level > 0 || math.Abs(flow) > 1.01 || math.Abs(level-periodicLevel) > 1e-12 || math.Abs(flow-periodicFlow) > 1e-12 {
			t.Fatal("潮位、流量或十二小时周期错误", h, level, flow)
		}
	}
	s, db := newBalanceTestServer(t)
	s.setLevel(testUID, "level_012_north_sea")
	hour := 3.0
	spot := fightWaterSpot{CastX: 2500, CastZ: 543, HasCastPosition: true, GameHour: &hour}
	ebbWater := s.fightWaterForRig(t.Context(), testUID, "sea_test", 50, spot)
	hour = 9
	floodWater := s.fightWaterForRig(t.Context(), testUID, "sea_test", 50, spot)
	if ebbWater.CurrentSource != "native_flow_at_cast_tide_clock" || ebbWater.TideLevelM == nil || ebbWater.CurrentBearingDeg == nil || floodWater.CurrentBearingDeg == nil || math.Abs(ebbWater.CurrentMPS-floodWater.CurrentMPS) > 1e-12 || math.Abs(math.Abs(*ebbWater.CurrentBearingDeg-*floodWater.CurrentBearingDeg)-180) > 1e-9 {
		t.Fatal("潮汐没有接入共同水域模型", ebbWater, floodWater)
	}
	if err := db.TunableSet(t.Context(), "fight_water_profiles", `{"sea_test":{"current_mps":0,"current_bearing_deg":0}}`, "test"); err != nil {
		t.Fatal(err)
	}
	configured := s.fightWaterForRig(t.Context(), testUID, "sea_test", 50, spot)
	if configured.CurrentMPS != 0 || configured.CurrentSource != "configured" {
		t.Fatal("已知零配置没有覆盖原生流场", configured)
	}
}
