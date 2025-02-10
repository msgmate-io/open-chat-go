package metrics

import (
	"testing"
)

func TestGetMemoryInfo(t *testing.T) {
	memInfo, err := getMemoryInfo()
	if err != nil {
		t.Fatalf("getMemoryInfo failed: %v", err)
	}

	if memInfo.TotalGB <= 0 {
		t.Errorf("Expected TotalGB > 0, got %f", memInfo.TotalGB)
	}
	if memInfo.AvailableGB <= 0 {
		t.Errorf("Expected AvailableGB > 0, got %f", memInfo.AvailableGB)
	}
	if memInfo.UsedPercent < 0 || memInfo.UsedPercent > 100 {
		t.Errorf("Expected UsedPercent between 0 and 100, got %f", memInfo.UsedPercent)
	}
}

func TestGetConnectedNetworks(t *testing.T) {
	networks, err := getConnectedNetworks()
	// Note: It's possible to have no connected networks, so we don't fail on empty list.
	// But we should not get an error unless nmcli is missing (which is expected on some envs, but user asked for it).
	// If nmcli is missing, err will be non-nil.
	if err != nil {
		t.Logf("getConnectedNetworks failed (might be expected if nmcli is missing): %v", err)
	} else {
		t.Logf("Connected networks: %v", networks)
	}
}

func TestGetScreenLocked(t *testing.T) {
	locked, err := getScreenLocked()
	if err != nil {
		t.Logf("getScreenLocked failed (might be expected if not on GNOME): %v", err)
	} else {
		t.Logf("Screen locked: %v", locked)
	}
}
