package main

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS — Backup path validation (C1 hardening)
//
// SECURITY (C1): `source` and `dest` arrive from POST/PUT /api/backup/jobs via
// bodyStr() with no validation. They previously flowed into a shell pipeline.
// Even now that exec is shell-free (exec_pipe.go), `dest` is still handed to the
// remote `btrfs receive <dest>` invoked through ssh, and a crafted value could
// still break out of the intended remote command or smuggle ssh options.
//
// This validator enforces: absolute path, filepath.Clean idempotence (no traversal
// games), no shell/control metacharacters, no leading dash (so it can never be
// mistaken for an ssh/btrfs flag), bounded length. It is intentionally strict;
// legitimate BTRFS subvolume paths are plain absolute paths.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
)

// backupPipeTimeout bounds a single send|receive pipeline. Backups of large
// subvolumes can take a while, so this is generous; it exists only to prevent a
// permanently stalled ssh/btrfs from pinning the worker forever.
const backupPipeTimeout = 6 * time.Hour

// splitSSHOpts tokenizes the SSH option string produced by sshOptsForDevice into
// argv elements. The source string is constructed internally from a fixed set of
// "-o key=value" flags and a known_hosts path (which never contains spaces), so a
// whitespace split is safe and correct here. We intentionally do NOT accept
// arbitrary user input through this path.
func splitSSHOpts(opts string) []string {
	fields := strings.Fields(opts)
	if fields == nil {
		return []string{}
	}
	return fields
}

// backupForbiddenRunes are characters that must never appear in a backup path.
// Covers shell metacharacters, quoting, command substitution, redirection and
// newlines/control sequences.
const backupForbiddenRunes = ";|&`$'\"\\<>(){}[]*?!~\n\r\t\v\f"

// validateBackupPath validates a source or dest path used by backup jobs.
// label is used only for error messages ("source" / "dest").
func validateBackupPath(label, p string) error {
	if p == "" {
		return fmt.Errorf("%s is required", label)
	}
	if len(p) > 4096 {
		return fmt.Errorf("%s too long", label)
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%s must be an absolute path", label)
	}
	// Reject anything that could be parsed as an option flag.
	if strings.HasPrefix(p, "-") {
		return fmt.Errorf("%s must not start with '-'", label)
	}
	// Reject shell/control metacharacters outright.
	if i := strings.IndexAny(p, backupForbiddenRunes); i >= 0 {
		return fmt.Errorf("%s contains a forbidden character", label)
	}
	// Reject any non-printable / non-ASCII control byte not already caught.
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f {
			return fmt.Errorf("%s contains a control character", label)
		}
	}
	// No path traversal: Clean must be a no-op (already canonical, no "..").
	if filepath.Clean(p) != p {
		return fmt.Errorf("%s is not a canonical path", label)
	}
	if strings.Contains(p, "..") {
		return fmt.Errorf("%s must not contain '..'", label)
	}
	return nil
}

// isValidNFSClient reports whether s is a bare IP address or a CIDR range,
// suitable for writing into /etc/exports as a client spec. Anything else
// (wildcards, hostnames, option-bearing strings) is rejected — see A1.
func isValidNFSClient(s string) bool {
	if s == "" {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	if _, _, err := net.ParseCIDR(s); err == nil {
		return true
	}
	return false
}
