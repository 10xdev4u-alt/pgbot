package diff

import (
	"fmt"
	"math"
)

func abs(v float64) float64 { return math.Abs(v) }

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// pctOrNil returns (after-before)/before, or nil when before is zero (a "new"
// item has no meaningful percentage change).
func pctOrNil(before, after float64) *float64 {
	if before == 0 {
		return nil
	}
	p := round4((after - before) / before)
	return &p
}

func shortID(id int64) string { return fmt.Sprintf("queryid %d", id) }
