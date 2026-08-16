package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestLowHotUpdateRatio(t *testing.T) {
	busy := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "sessions", Updates: 1_000_000, HotUpdates: 200_000}, // 20% HOT
	}}}
	if has(Compute(busy), "low_hot_update_ratio") == nil {
		t.Error("20% HOT on a heavily-updated table should fire")
	}
	healthy := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "sessions", Updates: 1_000_000, HotUpdates: 900_000}, // 90% HOT
	}}}
	if has(Compute(healthy), "low_hot_update_ratio") != nil {
		t.Error("90% HOT must not fire")
	}
	lowvol := &model.Context{Tables: &model.Tables{Top: []model.TableStat{
		{Schema: "public", Name: "tiny", Updates: 100, HotUpdates: 5}, // low ratio, tiny volume
	}}}
	if has(Compute(lowvol), "low_hot_update_ratio") != nil {
		t.Error("low update volume must not fire (cold table)")
	}
}
