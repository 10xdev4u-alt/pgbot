package rate

import (
	"math"
	"testing"
	"time"
)

func TestDelta_resetDetected(t *testing.T) {
	if _, ok := Delta(100, 40); ok {
		t.Fatal("expected reset (b<a) to be flagged invalid")
	}
	d, ok := Delta(40, 100)
	if !ok || d != 60 {
		t.Fatalf("want 60,true got %d,%v", d, ok)
	}
}

func TestPerSecond(t *testing.T) {
	v, ok := PerSecond(1000, 1600, time.Second)
	if !ok || *v != 600 {
		t.Fatalf("want 600/s got %v,%v", v, ok)
	}
	if _, ok := PerSecond(1000, 1600, 0); ok {
		t.Fatal("zero interval must be invalid")
	}
	if _, ok := PerSecond(1600, 1000, time.Second); ok {
		t.Fatal("reset must be invalid, not negative rate")
	}
}

func TestRatio_cacheHit(t *testing.T) {
	// hits +900, reads +100 -> 0.9
	v, ok := Ratio(0, 900, 0, 100)
	if !ok || math.Abs(*v-0.9) > 1e-9 {
		t.Fatalf("want 0.9 got %v,%v", v, ok)
	}
	// empty window -> undefined, not 0/NaN
	if _, ok := Ratio(500, 500, 500, 500); ok {
		t.Fatal("empty window must be undefined")
	}
}
