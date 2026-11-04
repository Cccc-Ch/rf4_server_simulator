package gameserver

import (
	"context"
	"math"
	"sort"

	"rf4server/internal/store"
)

type componentCalibration struct {
	def          store.ItemDef
	silver, gold float64
}

type componentParameter struct {
	Value     float64
	Estimated bool
	Source    string
}

// 仅补正物理量的缺值；耐久、品质、伸长率等合法零值不走补值。
func (s *Server) positiveComponentParameter(ctx context.Context, def *store.ItemDef, key string) componentParameter {
	if value := itemDefNumericScalar(def, key); value > 0 {
		return componentParameter{Value: value, Source: "definition"}
	}
	if def == nil || s == nil || s.DB == nil {
		return componentParameter{}
	}
	s.componentOnce.Do(func() { s.loadComponentCalibration(ctx) })
	target := s.componentDefs[def.ItemID]
	if target == nil {
		return componentParameter{}
	}
	price := target.silver
	gold := price <= 0
	if gold {
		price = target.gold
	}
	if price <= 0 {
		return componentParameter{}
	}
	var sum, weights float64
	nearest, nearestWeight := "", 0.0
	for _, peer := range s.componentDefs {
		if peer.def.Category != def.Category || peer.def.ItemID == def.ItemID {
			continue
		}
		peerPrice := peer.silver
		if gold {
			peerPrice = peer.gold
		}
		value := itemDefNumericScalar(&peer.def, key)
		if value <= 0 || peerPrice <= 0 {
			continue
		}
		// 同品类、同用途，按品质和价格邻近度加权；不跨货币标定。
		if kind := itemDefInt(def, "fopbhlmkcpe"); kind != 0 && itemDefInt(&peer.def, "fopbhlmkcpe") != kind {
			continue
		}
		ratio := price / peerPrice
		distance := math.Abs(math.Log(ratio)) + math.Abs(float64(itemDefInt(def, reelPartQualityAttr)-itemDefInt(&peer.def, reelPartQualityAttr)))/100
		weight := 1 / (1 + distance*distance)
		// 0.25 为本地补值弹性，限幅防止收藏溢价无限抬高物理量。
		value *= math.Pow(clampFloat(ratio, 0.25, 4), 0.25)
		sum += weight * math.Log(value)
		weights += weight
		if weight > nearestWeight || (weight == nearestWeight && peer.def.ItemID < nearest) {
			nearest, nearestWeight = peer.def.ItemID, weight
		}
	}
	if weights == 0 {
		return componentParameter{}
	}
	return componentParameter{Value: math.Exp(sum / weights), Estimated: true, Source: "price_properties:" + nearest}
}

// 静态价目只作标定，不使用优惠价、工坊反算报价或银金币兑换。
func (s *Server) loadComponentCalibration(ctx context.Context) {
	rows, err := s.DB.SQL.QueryContext(ctx, `SELECT d.numeric_id,d.item_id,d.category,COALESCE(d.kind,''),COALESCE(d.brand,''),d.attrs_json,COALESCE(p.silver,0),COALESCE(p.gold,0) FROM item_def d LEFT JOIN item_price p ON p.item_id=d.item_id ORDER BY d.item_id`)
	if err != nil {
		return
	}
	defs := map[string]*componentCalibration{}
	for rows.Next() {
		var c componentCalibration
		if rows.Scan(&c.def.NumericID, &c.def.ItemID, &c.def.Category, &c.def.Kind, &c.def.Brand, &c.def.AttrsJSON, &c.silver, &c.gold) != nil {
			continue
		}
		if n := itemDefInt(&c.def, gearAttrLineLen); n > 1 {
			c.silver /= float64(n)
			c.gold /= float64(n)
		}
		defs[c.def.ItemID] = &c
	}
	readErr := rows.Err()
	_ = rows.Close()
	if readErr != nil {
		return
	}
	// 无独立售价的内嵌件按母装备分摊；目标物理量不参与自身权重。
	allocated := map[string][2][]float64{}
	for _, host := range defs {
		if host.def.Category != gearCatRod && host.def.Category != gearCatReel {
			continue
		}
		total, parts := 8.0, map[string]float64{}
		for _, value := range defAttrs(&host.def) {
			cfg, ok := value.(map[string]any)
			if !ok {
				continue
			}
			id, _ := cfg["nhjofmmmngl"].(string)
			part := defs[id]
			if part == nil {
				continue
			}
			weight := componentPriceWeight(part.def.Category) * (0.5 + clampFloat(float64(itemDefInt(&part.def, reelPartQualityAttr)), 0, 100)/100)
			parts[id] += weight
			total += weight
		}
		for id, weight := range parts {
			values := allocated[id]
			if host.silver > 0 {
				values[0] = append(values[0], host.silver*weight/total)
			}
			if host.gold > 0 {
				values[1] = append(values[1], host.gold*weight/total)
			}
			allocated[id] = values
		}
	}
	for id, prices := range allocated {
		part := defs[id]
		if part.silver == 0 && part.gold == 0 {
			part.silver, part.gold = medianComponentPrice(prices[0]), medianComponentPrice(prices[1])
		}
	}
	s.componentDefs = defs
}

func componentPriceWeight(category int64) float64 {
	switch category {
	case 36, 52, 81:
		return 4
	case 8, 9:
		return 3
	case 4, 5, 7, 50:
		return 2
	default:
		return 1
	}
}

func medianComponentPrice(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	n := len(values)
	return (values[(n-1)/2] + values[n/2]) / 2
}
