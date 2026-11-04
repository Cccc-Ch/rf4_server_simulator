package gameserver

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"math"
	"sync"
)

//go:embed assets/native_flow_fields.json.gz
var nativeFlowData []byte

type nativeFlowField struct {
	Level    string  `json:"level"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Format   int     `json:"format"`
	OriginX  float64 `json:"origin_x"`
	OriginZ  float64 `json:"origin_z"`
	SizeX    float64 `json:"size_x"`
	SizeZ    float64 `json:"size_z"`
	MaxSpeed float64 `json:"max_speed"`
	RGB      bool    `json:"rgb"`
	Pixels   []byte  `json:"pixels"`
}

var nativeFlows struct {
	sync.Once
	byLevel map[string]nativeFlowField
}

func nativeFlowForLevel(level string) (nativeFlowField, bool) {
	nativeFlows.Do(func() {
		reader, err := gzip.NewReader(bytes.NewReader(nativeFlowData))
		if err != nil {
			return
		}
		defer reader.Close()
		var data struct {
			Fields []nativeFlowField `json:"fields"`
		}
		if json.NewDecoder(reader).Decode(&data) != nil {
			return
		}
		nativeFlows.byLevel = make(map[string]nativeFlowField, len(data.Fields))
		for _, field := range data.Fields {
			nativeFlows.byLevel[field.Level] = field
		}
	})
	f, ok := nativeFlows.byLevel[level]
	return f, ok
}

func nativeHalfFloat(v uint16) float64 {
	sign := 1.0
	if v&0x8000 != 0 {
		sign = -1
	}
	exponent, mantissa := int(v>>10&31), int(v&1023)
	if exponent == 0 {
		return sign * math.Ldexp(float64(mantissa), -24)
	}
	if exponent == 31 {
		if mantissa != 0 {
			return math.NaN()
		}
		return math.Inf(int(sign))
	}
	return sign * math.Ldexp(1+float64(mantissa)/1024, exponent-15)
}

// 本体 RVA946720：地形归一化、整数采样；浮点流图保留原精度。
func (f nativeFlowField) sample(x, z float64) (vx, vz float64, ok bool) {
	if f.Width <= 0 || f.Height <= 0 || f.SizeX <= 0 || f.SizeZ <= 0 || math.IsNaN(x) || math.IsNaN(z) {
		return
	}
	u, v := (x-f.OriginX)/f.SizeX, (z-f.OriginZ)/f.SizeZ
	// 地形外的纹理环绕不作为有效钓点外推。
	if u < 0 || u >= 1 || v < 0 || v >= 1 {
		return
	}
	stride := map[int]int{3: 3, 4: 4, 17: 8, 20: 16}[f.Format]
	if stride == 0 || len(f.Pixels) != f.Width*f.Height*stride {
		return
	}
	p := (int(v*float64(f.Height))*f.Width + int(u*float64(f.Width))) * stride
	var rgb [3]float64
	for i := range rgb {
		switch f.Format {
		case 3, 4:
			rgb[i] = float64(f.Pixels[p+i]) / 255
		case 17:
			rgb[i] = nativeHalfFloat(binary.LittleEndian.Uint16(f.Pixels[p+i*2:]))
		case 20:
			rgb[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(f.Pixels[p+i*4:])))
		}
		if math.IsNaN(rgb[i]) || math.IsInf(rgb[i], 0) {
			return 0, 0, false
		}
	}
	if rgb[0] == 0 && rgb[1] == 0 {
		return 0, 0, true
	}
	vx, vz = (2*rgb[0]-1)*f.MaxSpeed, -(2*rgb[1]-1)*f.MaxSpeed
	if f.RGB {
		vx, vz = vx*rgb[2], -vz*rgb[2]
	}
	return vx, vz, true
}
