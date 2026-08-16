package collect

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/conn"
)

func TestArchiverAvailable_offOnStandby(t *testing.T) {
	if (archiverCollector{}).Available(conn.Capabilities{RecoveryChecked: true, InRecovery: true}) {
		t.Error("archiver must not run on a confirmed standby")
	}
	if !(archiverCollector{}).Available(conn.Capabilities{RecoveryChecked: true, InRecovery: false}) {
		t.Error("archiver must run on a primary")
	}
	if !(archiverCollector{}).Available(conn.Capabilities{}) {
		t.Error("archiver must run when recovery state is unknown")
	}
}
