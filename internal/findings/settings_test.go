package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestIoTimingOff(t *testing.T) {
	off := &model.Context{Settings: &model.Settings{Params: map[string]string{"track_io_timing": "off"}}}
	if has(Compute(off), "io_timing_off") == nil {
		t.Error("track_io_timing=off should fire")
	}
	on := &model.Context{Settings: &model.Settings{Params: map[string]string{"track_io_timing": "on"}}}
	if has(Compute(on), "io_timing_off") != nil {
		t.Error("track_io_timing=on must not fire")
	}
}
