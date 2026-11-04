package gameserver

import (
	"context"
	"sort"
	"strings"

	"rf4server/internal/proto"
)

// 各部件按所属阶段参与，辅助件不强行串入最弱承力链。
var fishingComponentRoles = map[int64]string{
	3: "rod_load", 4: "rod_grip", 5: "line_contact", 36: "rod_blank", 56: "bite_tip",
	6: "reel_load", 7: "rotation", 8: "spool", 9: "drag", 10: "lubrication", 34: "crank_grip",
	48: "line_contact", 49: "drag_control", 50: "crank", 51: "drag_control", 52: "transmission", 53: "reel_native", 54: "line_lay", 81: "motor",
	11: "mainline", 13: "leader", 14: "connector", 15: "leader_material", 57: "leadcore", 69: "flyline", 70: "flyleader",
	12: "hook", 28: "float", 29: "sinker", 31: "feeder", 33: "spacer", 44: "sbirolino", 17: "lure", 27: "bait",
	38: "groundbait_delivery", 59: "groundbait_delivery", 60: "groundbait_delivery", 30: "pva", 45: "pva", 46: "groundbait", 18: "groundbait", 19: "groundbait", 20: "groundbait", 58: "dip", 47: "bite_signal", 79: "rod_support",
}

type fishingComponentInput struct {
	GUID, ParentGUID, ItemID, Role string
	Category                       int64
	Durability                     *float64
	Definition                     string // 原字段保留，未知字段不猜成力学量。
	Projection                     *proto.Value
}

// 共同快照收集实际挂载及内嵌件，客户端仍负责接触和逐帧运动。
func (s *Server) mountedFishingComponents(ctx context.Context, rodGUID string) []fishingComponentInput {
	if s == nil || s.DB == nil || rodGUID == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []fishingComponentInput
	var visit func(*proto.Value, string)
	visit = func(value *proto.Value, parent string) {
		if value == nil {
			return
		}
		if value.Kind == proto.KindObj {
			def := s.reelPartDef(ctx, value)
			if def != nil && fishingComponentRoles[def.Category] != "" {
				guid := ""
				if i := fieldIdxByCS(value.Class, csGUID); i >= 0 && i < value.FieldCount() {
					guid = value.Fields[i].Hex
				}
				if guid != "" && !seen[guid] {
					seen[guid] = true
					var condition *float64
					if i := fieldIdxByCS(value.Class, csDurability); i >= 0 && i < value.FieldCount() && value.Fields[i].Num != nil {
						v := float64(proto.F32FromBits(uint32(*value.Fields[i].Num)))
						condition = &v
					}
					out = append(out, fishingComponentInput{GUID: guid, ParentGUID: parent, ItemID: def.ItemID, Category: def.Category, Role: fishingComponentRoles[def.Category], Durability: condition, Definition: def.AttrsJSON, Projection: value})
					parent = guid
				}
			}
			for _, field := range value.Fields {
				visit(field.Val, parent)
			}
		}
		for _, item := range value.Items {
			visit(item, parent)
		}
	}
	read := func(guid, parent string) {
		if guid == "" || seen[guid] {
			return
		}
		if row, err := s.DB.GetItemByGUID(ctx, guid); err == nil && row != nil {
			if tree, err := s.ItemToTree(ctx, row); err == nil {
				visit(tree, parent)
			}
		}
	}
	read(rodGUID, "")
	if row, err := s.DB.GetItemByGUID(ctx, rodGUID); err == nil && row != nil {
		if fields, err := FieldsFromJSON(row.FieldsJSON); err == nil {
			for _, cs := range []string{csLine, csReel} {
				if i := fieldIdxByCS(row.WireClass, cs); i >= 0 && i < len(fields) {
					read(fields[i].GuidHex(), rodGUID)
				}
			}
		}
	}
	for _, comp := range s.storedRigComponents(ctx, rodGUID) {
		read(comp.GUID, rodGUID)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].GUID < out[j].GUID
	})
	return out
}

func (s *Server) applyCaughtHook(ctx context.Context, fish *RolledFish, spec *gearPhysicsSpec) {
	if fish == nil || spec == nil || fish.HookGUID == "" {
		return
	}
	mounted := false
	for _, component := range spec.Components {
		if component.Category == catHook && component.GUID == fish.HookGUID {
			mounted = true
			break
		}
	}
	if !mounted {
		spec.HookPresent, spec.HookStrengthKg = true, 0
		spec.BrokenPart = "hook"
		return
	}
	if !s.applyBaitedHookSpec(ctx, baitedHookSet{hookGUID: fish.HookGUID, baitGUIDs: strings.Fields(fish.BaitGUIDs)}, spec) {
		spec.BrokenPart = "hook"
	}
}
