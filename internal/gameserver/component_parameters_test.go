package gameserver

import (
	"math"
	"testing"

	"rf4server/internal/store"
)

func TestComponentPriceFallbackPreservesRawAndCurrency(t *testing.T) {
	s, _ := newBalanceTestServer(t)
	s.componentOnce.Do(func() {})
	def := func(id string, kg, quality float64) store.ItemDef {
		return store.ItemDef{ItemID: id, Category: gearCatLeadcore, AttrsJSON: attrsJSON(t, map[string]any{gearAttrMaxLoad: f32Attr(float32(kg)), reelPartQualityAttr: quality})}
	}
	target, peer := def("target", 0, 75), def("peer", 10, 75)
	s.componentDefs = map[string]*componentCalibration{
		"target": {def: target, silver: 160}, "peer": {def: peer, silver: 10},
		"foreign_currency": {def: def("gold", 1000, 75), gold: 1},
	}
	value := s.positiveComponentParameter(t.Context(), &target, gearAttrMaxLoad)
	if !value.Estimated || value.Source != "price_properties:peer" || math.Abs(value.Value-10*math.Sqrt(2)) > 1e-9 {
		t.Fatal(value)
	}
	if actual := s.positiveComponentParameter(t.Context(), &peer, gearAttrMaxLoad); actual.Estimated || actual.Value != 10 {
		t.Fatal(actual)
	}
	delete(s.componentDefs, "peer")
	if unknown := s.positiveComponentParameter(t.Context(), &target, gearAttrMaxLoad); unknown.Value != 0 || unknown.Estimated {
		t.Fatal("混用了金币报价", unknown)
	}
}

func TestComponentCalibrationUsesStaticPricesAndParentAllocation(t *testing.T) {
	s, _ := newBalanceTestServer(t)
	s.loadComponentCalibration(t.Context())
	if len(s.componentDefs) < 1000 {
		t.Fatal("静态标定数据未加载", len(s.componentDefs))
	}
	for _, id := range []string{"rengine_1", "rengine_2"} {
		part := s.componentDefs[id]
		if part == nil || (part.silver <= 0 && part.gold <= 0) {
			t.Fatal("无独立售价的电机未按母轮分摊", id, part)
		}
	}
	if medianComponentPrice([]float64{100, 4, 2, 6}) != 5 {
		t.Fatal("多母装备分摊应取中位数")
	}
}
