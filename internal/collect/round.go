package collect

import "math"

// Rounding keeps the JSON tidy and stable. Rates are inherently approximate
// (one short sample), so trailing float noise is meaningless precision.

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

func round2p(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := round2(*v)
	return &r
}

func round4p(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := round4(*v)
	return &r
}
