package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type btrfsMember struct {
	ID      string
	Path    string
	Missing bool
}

// A partial or unrecognized listing must never establish absence of missing
// devices. Require the reported count and exactly one valid row per devid.
func parseBtrfsMembers(out string) ([]btrfsMember, error) {
	total := -1
	seen := map[string]bool{}
	var members []btrfsMember
	missingWarning := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ERROR:") {
			return nil, fmt.Errorf("filesystem show: %s", line)
		}
		if strings.Contains(strings.ToLower(line), "some devices missing") {
			missingWarning = true
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if strings.HasPrefix(line, "Total devices ") {
			if total != -1 || len(f) < 3 {
				return nil, fmt.Errorf("ambiguous device count")
			}
			n, err := strconv.Atoi(f[2])
			if err != nil || n < 1 {
				return nil, fmt.Errorf("invalid device count")
			}
			total = n
			continue
		}
		if f[0] != "devid" {
			continue
		}
		if len(f) < 8 || f[2] != "size" || f[4] != "used" || f[6] != "path" {
			return nil, fmt.Errorf("unrecognized device row: %s", line)
		}
		id, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("invalid devid: %s", f[1])
		}
		key := strconv.FormatUint(id, 10)
		if seen[key] {
			return nil, fmt.Errorf("duplicate devid %s", key)
		}
		seen[key] = true
		path := strings.Join(f[7:], " ")
		missing := false
		if strings.HasSuffix(path, " MISSING") {
			missing = true
			path = strings.TrimSuffix(path, " MISSING")
		}
		if path == "missing" || path == "<missing disk>" {
			missing = true
			path = ""
		}
		if path != "" && !filepath.IsAbs(path) {
			return nil, fmt.Errorf("unrecognized device path: %s", path)
		}
		members = append(members, btrfsMember{ID: key, Path: path, Missing: missing})
	}
	if total < 1 || len(members) != total {
		return nil, fmt.Errorf("incomplete device listing: expected %d, got %d", total, len(members))
	}
	if missingWarning {
		found := false
		for _, m := range members {
			found = found || m.Missing
		}
		if !found {
			return nil, fmt.Errorf("missing devices reported without identifiable devid")
		}
	}
	return members, nil
}

func readBtrfsMembers(mountPoint string) ([]btrfsMember, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, code := runSafeStatusContext(ctx, "btrfs", "filesystem", "show", mountPoint)
	if code != 0 {
		return nil, fmt.Errorf("cannot query Btrfs membership (exit %d): %s", code, strings.TrimSpace(out))
	}
	return parseBtrfsMembers(out)
}

// Empty ID with nil error means positively verified: no device is missing.
// Multiple absent devices cannot identify which source the user intended.
func missingDevidFromMembers(members []btrfsMember) (string, error) {
	id := ""
	for _, m := range members {
		if !m.Missing {
			continue
		}
		if id != "" {
			return "", fmt.Errorf("multiple missing devices; source devid is ambiguous")
		}
		id = m.ID
	}
	return id, nil
}

func missingDevidForPool(mountPoint string) (string, error) {
	members, err := readBtrfsMembers(mountPoint)
	if err != nil {
		return "", err
	}
	return missingDevidFromMembers(members)
}

func kernelPoolHasDevice(mountPoint string, dev *Device) bool {
	if dev == nil {
		return false
	}
	members, err := readBtrfsMembers(mountPoint)
	if err != nil {
		return false
	}
	return btrfsMembersContainDevice(members, dev)
}

func btrfsMembersContainDevice(members []btrfsMember, dev *Device) bool {
	if dev == nil {
		return false
	}
	// A stable by-id, when present, is authoritative. A stale CurrentPath must
	// not identify a different disk after device names have been reused.
	candidate := dev.CurrentPath
	if dev.ByIDPath != "" {
		resolved, err := filepath.EvalSymlinks(dev.ByIDPath)
		if err != nil {
			return false
		}
		candidate = resolved
	}
	if candidate == "" || !filepath.IsAbs(candidate) {
		return false
	}
	for _, m := range members {
		if m.Missing || m.Path == "" {
			continue
		}
		actual := m.Path
		if resolved, err := filepath.EvalSymlinks(actual); err == nil {
			actual = resolved
		}
		if actual == candidate {
			return true
		}
	}
	return false
}
