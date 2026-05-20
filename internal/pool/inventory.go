package pool

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/Geogboe/boxy/pkg/model"
)

type InventoryRebuildReport struct {
	Changed bool
	Skipped []InventoryRebuildSkip
}

type InventoryRebuildSkip struct {
	ResourceID model.ResourceID
	Reason     string
}

// RebuildReadyInventory rebuilds a pool's ready inventory from persisted
// resources while preserving old embedded inventory as a fallback for older
// state files.
func RebuildReadyInventory(
	p model.Pool,
	resources []model.Resource,
	fallbackInventory []model.Resource,
) (model.Pool, InventoryRebuildReport, error) {
	report := InventoryRebuildReport{}
	fallbackByID := make(map[model.ResourceID]model.Resource, len(fallbackInventory))
	for _, res := range fallbackInventory {
		if res.ID == "" {
			continue
		}
		fallbackByID[res.ID] = res
	}

	globalIDs := make(map[model.ResourceID]struct{}, len(resources))
	accepted := make(map[model.ResourceID]model.Resource)
	for _, res := range resources {
		if res.ID == "" {
			continue
		}
		globalIDs[res.ID] = struct{}{}

		_, inFallback := fallbackByID[res.ID]
		if res.OriginPool != p.Name && (!inFallback || res.OriginPool != "") {
			continue
		}
		if !matchesPoolShape(p, res) {
			if res.OriginPool == p.Name {
				report.Skipped = append(report.Skipped, InventoryRebuildSkip{
					ResourceID: res.ID,
					Reason:     "resource type/profile no longer matches pool config",
				})
			}
			continue
		}
		if res.State != model.ResourceStateReady {
			continue
		}
		accepted[res.ID] = res
	}

	for _, res := range fallbackInventory {
		if res.ID == "" {
			continue
		}
		if _, ok := accepted[res.ID]; ok {
			continue
		}
		if _, hasGlobal := globalIDs[res.ID]; hasGlobal {
			continue
		}
		if !matchesPoolShape(p, res) || res.State != model.ResourceStateReady {
			continue
		}
		accepted[res.ID] = res
	}

	rebuilt := p
	rebuilt.Inventory.Resources = nil
	ids := make([]string, 0, len(accepted))
	for id := range accepted {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := rebuilt.Inventory.Add(accepted[model.ResourceID(id)]); err != nil {
			return model.Pool{}, InventoryRebuildReport{}, fmt.Errorf("add rebuilt resource %q: %w", id, err)
		}
	}
	report.Changed = !reflect.DeepEqual(p.Inventory.Resources, rebuilt.Inventory.Resources)
	return rebuilt, report, nil
}

func matchesPoolShape(p model.Pool, res model.Resource) bool {
	return res.Type == p.Inventory.ExpectedType && res.Profile == p.Inventory.ExpectedProfile
}
