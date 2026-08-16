package findings

import (
	"strings"
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestConnectionSaturation_breakdownEvidence(t *testing.T) {
	c := &model.Context{
		Limits: &model.Limits{ConnectionsUsed: 90, ConnectionsMax: 100},
		Activity: &model.Activity{Connections: []model.ConnGroup{
			{AppName: "web", User: "app", State: "idle", Count: 60},
			{AppName: "sidekiq", User: "app", State: "active", Count: 20},
		}},
	}
	f := has(Compute(c), "connection_saturation")
	if f == nil {
		t.Fatal("90/100 should saturate")
	}
	if len(f.Evidence) == 0 || !strings.Contains(f.Evidence[0], "web") {
		t.Errorf("evidence should name the top contributor: %v", f.Evidence)
	}
}
