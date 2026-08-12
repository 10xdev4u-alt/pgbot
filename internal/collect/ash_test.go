package collect

import (
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
)

func ws(typ, event string, qid int64) WaitSample {
	s := WaitSample{State: "active", BackendType: "client backend"}
	if typ != "" {
		s.WaitEventType = &typ
	}
	if event != "" {
		s.WaitEvent = &event
	}
	if qid != 0 {
		s.QueryID = &qid
	}
	return s
}

func TestBuildWaitProfile_bucketsAndCPU(t *testing.T) {
	var samples []WaitSample
	for i := 0; i < 44; i++ {
		samples = append(samples, ws("Lock", "transactionid", 1001))
	}
	for i := 0; i < 27; i++ {
		samples = append(samples, ws("", "", 1002)) // on CPU (nil type)
	}
	for i := 0; i < 21; i++ {
		samples = append(samples, ws("IO", "DataFileRead", 1002))
	}
	for i := 0; i < 8; i++ {
		samples = append(samples, ws("LWLock", "BufferMapping", 0))
	}
	wp := buildWaitProfile(samples, 5200*time.Millisecond, map[int64]string{1001: "UPDATE orders SET ..."})

	if wp.Samples != 100 {
		t.Fatalf("want 100 samples, got %d", wp.Samples)
	}
	if wp.Buckets[0].Type != "Lock" { // share-descending
		t.Errorf("top bucket should be Lock, got %s", wp.Buckets[0].Type)
	}
	get := func(typ string) float64 {
		for _, b := range wp.Buckets {
			if b.Type == typ {
				return b.Share
			}
		}
		return -1
	}
	if s := get("CPU"); s < 0.26 || s > 0.28 {
		t.Errorf("CPU synthetic bucket share want ~0.27, got %.3f", s)
	}
	if s := get("Lock"); s < 0.43 || s > 0.45 {
		t.Errorf("Lock share want ~0.44, got %.3f", s)
	}

	// Per-query attribution: query 1001 is entirely on locks.
	found := false
	for _, q := range wp.ByQuery {
		if q.QueryID == 1001 {
			found = true
			if q.LockShare < 0.99 {
				t.Errorf("query 1001 LockShare want 1.0, got %.3f", q.LockShare)
			}
			if q.SampleText == "" {
				t.Error("query 1001 should carry sample text")
			}
			if q.TopType != "Lock" {
				t.Errorf("query 1001 TopType want Lock, got %s", q.TopType)
			}
		}
	}
	if !found {
		t.Error("expected per-query attribution for query 1001")
	}
}

func TestBuildWaitProfile_empty(t *testing.T) {
	wp := buildWaitProfile(nil, 5*time.Second, nil)
	if !wp.Available || wp.Samples != 0 || len(wp.Buckets) != 0 {
		t.Errorf("empty profile should be available with 0 samples and no buckets, got %+v", wp)
	}
	if !wp.Thin() {
		t.Error("a 0-sample profile is thin")
	}
}

func TestAshSQL_versionGate(t *testing.T) {
	pg13 := ashSQL(conn.Capabilities{VersionNum: 130000})
	if !strings.Contains(pg13, "NULL::bigint") {
		t.Error("PG13 SQL must select a NULL literal, not the query_id column")
	}
	pg14 := ashSQL(conn.Capabilities{VersionNum: 140000})
	if strings.Contains(pg14, "NULL::bigint") {
		t.Error("PG14+ SQL must select the real query_id column")
	}
}
