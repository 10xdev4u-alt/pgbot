package conn

import "testing"

func TestCapabilityGating(t *testing.T) {
	// Unknown recovery state: neither Primary nor Standby asserts.
	var unknown Capabilities
	if unknown.Primary() || unknown.Standby() {
		t.Error("unverified recovery state must report neither primary nor standby")
	}
	primary := Capabilities{RecoveryChecked: true, InRecovery: false}
	if !primary.Primary() || primary.Standby() {
		t.Error("checked+not-in-recovery must be primary")
	}
	standby := Capabilities{RecoveryChecked: true, InRecovery: true}
	if standby.Primary() || !standby.Standby() {
		t.Error("checked+in-recovery must be standby")
	}
	if !(Capabilities{Provider: ProviderRDS}).ManagedProvider() ||
		(Capabilities{Provider: ProviderUnknown}).ManagedProvider() {
		t.Error("ManagedProvider must be true for rds, false for unknown")
	}
}
