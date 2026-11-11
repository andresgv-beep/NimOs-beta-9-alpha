package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const healthyMemberListing = "Label: 'pool' uuid: example\nTotal devices 2 FS bytes used 1GiB\ndevid 1 size 2GiB used 1GiB path /dev/loop10\ndevid 2 size 2GiB used 1GiB path /dev/loop20\n"

func TestStorageMembershipParsing(t *testing.T) {
	for _, tc := range []struct {
		name, out, want string
		bad             bool
	}{
		{"healthy", healthyMemberListing, "", false},
		{"missing marker", strings.Replace(healthyMemberListing, "/dev/loop20", "<missing disk> MISSING", 1), "2", false},
		{"missing path", strings.Replace(healthyMemberListing, "/dev/loop20", "missing", 1), "2", false},
		{"missing named disk", strings.Replace(healthyMemberListing, "/dev/loop20", "/dev/loop20 MISSING", 1), "2", false},
		{"empty", "", "", true},
		{"error", "ERROR: Input/output error", "", true},
		{"truncated", strings.Split(healthyMemberListing, "devid 2")[0], "", true},
		{"no count", strings.Replace(healthyMemberListing, "Total devices 2 FS bytes used 1GiB\n", "", 1), "", true},
		{"invalid row", strings.Replace(healthyMemberListing, "devid 2 size", "devid 2 unknown", 1), "", true},
		{"duplicate devid", strings.Replace(healthyMemberListing, "devid 2", "devid 1", 1), "", true},
		{"missing warning without identity", healthyMemberListing + "*** Some devices missing\n", "", true},
		{"multiple missing", strings.ReplaceAll(strings.ReplaceAll(healthyMemberListing, "/dev/loop10", "missing"), "/dev/loop20", "missing"), "", true},
		{"invalid id", strings.Replace(healthyMemberListing, "devid 2", "devid x", 1), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			members, err := parseBtrfsMembers(tc.out)
			got := ""
			if err == nil {
				got, err = missingDevidFromMembers(members)
			}
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("got %q, %v; want %q, error=%v", got, err, tc.want, tc.bad)
			}
		})
	}
}

func TestStorageMembershipExactDeviceIdentity(t *testing.T) {
	members, err := parseBtrfsMembers(healthyMemberListing)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want bool
	}{{"/dev/loop1", false}, {"/dev/loop10", true}, {"/dev/loop2", false}, {"/dev/loop20", true}, {"", false}} {
		if got := btrfsMembersContainDevice(members, &Device{CurrentPath: tc.path}); got != tc.want {
			t.Fatalf("path %q: got %v", tc.path, got)
		}
	}
	if btrfsMembersContainDevice([]btrfsMember{{Path: "/dev/loop10", Missing: true}}, &Device{CurrentPath: "/dev/loop10"}) {
		t.Fatal("missing member accepted")
	}
	// Paths in a label must not count as membership evidence.
	labelled := strings.Replace(healthyMemberListing, "'pool'", "'/dev/loop1'", 1)
	members, err = parseBtrfsMembers(labelled)
	if err != nil {
		t.Fatal(err)
	}
	if btrfsMembersContainDevice(members, &Device{CurrentPath: "/dev/loop1"}) {
		t.Fatal("label accepted as device")
	}
}

func TestStorageMembershipStableIDOverridesStalePath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "device")
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "by-id")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	dev := &Device{ByIDPath: link, CurrentPath: other}
	if !btrfsMembersContainDevice([]btrfsMember{{Path: target}}, dev) {
		t.Fatal("resolved stable identity rejected")
	}
	if btrfsMembersContainDevice([]btrfsMember{{Path: other}}, dev) {
		t.Fatal("stale path accepted instead of stable identity")
	}
	dev.ByIDPath = filepath.Join(dir, "missing-id")
	if btrfsMembersContainDevice([]btrfsMember{{Path: other}}, dev) {
		t.Fatal("unresolved stable ID fell back to stale path")
	}
}

func mockMembershipCommand(t *testing.T, output, code string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$NIMOS_TEST_BTRFS_LOG\"\nprintf '%s\\n' \"$NIMOS_TEST_BTRFS_OUTPUT\"\nexit \"$NIMOS_TEST_BTRFS_EXIT\"\n"
	if err := os.WriteFile(filepath.Join(dir, "btrfs"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("NIMOS_TEST_BTRFS_LOG", log)
	t.Setenv("NIMOS_TEST_BTRFS_OUTPUT", output)
	t.Setenv("NIMOS_TEST_BTRFS_EXIT", code)
	return log
}

func TestStorageMembershipQueryFailureIsNotHealthy(t *testing.T) {
	mockMembershipCommand(t, healthyMemberListing, "1")
	if id, err := missingDevidForPool("/mock"); err == nil || id != "" {
		t.Fatalf("failed query accepted: %q %v", id, err)
	}
	if kernelPoolHasDevice("/mock", &Device{CurrentPath: "/dev/loop10"}) {
		t.Fatal("failed query confirmed membership")
	}
}

func TestStorageReplaceUnknownSourceNeverStartsCommand(t *testing.T) {
	for _, tc := range []struct{ name, out, code string }{{"query error", "ERROR: I/O error", "1"}, {"unknown output", "???", "0"}, {"no missing device", healthyMemberListing, "0"}} {
		t.Run(tc.name, func(t *testing.T) {
			log := mockMembershipCommand(t, tc.out, tc.code)
			err := NewRealBtrfsExecutor().ReplaceDevice(context.Background(), "/mock", filepath.Join(t.TempDir(), "absent"), "/unused-target")
			if err == nil {
				t.Fatal("replace accepted unverified source")
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(calls), "replace start") {
				t.Fatalf("mutation started: %s", calls)
			}
		})
	}
}
