package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPoolJSONShapeEnriched(t *testing.T) {
	p := Pool{
		ID: "p1", Name: "test1", BtrfsUUID: "abc",
		Profile: ProfileRaid1, MountPoint: "/x",
		ControlState: "managed",
		Usage:        &PoolUsage{TotalBytes: 100, UsedBytes: 20, AvailableBytes: 80, UsagePercent: 20},
		Health:       &PoolHealth{Status: "healthy"},
		IsPrimary:    true, Mounted: true,
	}
	data, _ := json.Marshal(p)
	s := string(data)
	// Snake_case checks
	for _, key := range []string{
		`"id":`, `"name":`, `"btrfs_uuid":`,
		`"profile":`, `"mount_point":`, `"control_state":`,
		`"usage":`, `"health":`, `"is_primary":`, `"mounted":`,
		`"total_bytes":`, `"used_bytes":`, `"available_bytes":`, `"usage_percent":`,
	} {
		if !strings.Contains(s, key) {
			t.Errorf("missing %s in JSON: %s", key, s)
		}
	}
	// PascalCase NOT present
	for _, key := range []string{
		`"ID":`, `"Name":`, `"BtrfsUUID":`, `"MountPoint":`,
		`"ControlState":`, `"Usage":`, `"IsPrimary":`,
	} {
		if strings.Contains(s, key) {
			t.Errorf("unexpected PascalCase %s in JSON: %s", key, s)
		}
	}
}
