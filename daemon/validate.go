// validate.go — Validación de input del daemon privilegiado.
//
// Todo dato que cruza la frontera del socket o del HTTP API y acaba en una
// operación de sistema pasa por estos validadores. Regexes estrictas en
// allowlist (qué SÍ se acepta), nunca blocklist.
// (Extraído de main.go · refactor 11/06/2026.)
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// ═══════════════════════════════════
// Input validation
// ═══════════════════════════════════

var (
	validShareName = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,63}$`)
	validUsername  = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)
	systemUsers    = map[string]bool{
		"root": true, "daemon": true, "nobody": true, "www-data": true,
		"sshd": true, "nimos": true, "systemd-network": true,
		"systemd-resolve": true, "systemd-timesync": true,
		"messagebus": true, "syslog": true, "uuidd": true,
		"_apt": true, "avahi": true,
	}
)

func checkShareName(name string) error {
	if name == "" {
		return fmt.Errorf("shareName required")
	}
	if !validShareName.MatchString(name) {
		return fmt.Errorf("invalid shareName: %s", name)
	}
	return nil
}

func checkUsername(username string) error {
	if username == "" {
		return fmt.Errorf("username required")
	}
	if !validUsername.MatchString(username) {
		return fmt.Errorf("invalid username: %s", username)
	}
	if systemUsers[username] {
		return fmt.Errorf("rejected system username: %s", username)
	}
	return nil
}

func checkPoolPath(poolPath string) error {
	if poolPath == "" {
		return fmt.Errorf("poolPath required")
	}
	if !strings.HasPrefix(poolPath, poolBase) && !strings.HasPrefix(poolPath, "/nimos/") {
		return fmt.Errorf("invalid poolPath: must be within %s", poolBase)
	}
	if strings.Contains(poolPath, "..") {
		return fmt.Errorf("path traversal not allowed")
	}
	if _, err := os.Stat(poolPath); os.IsNotExist(err) {
		return fmt.Errorf("poolPath does not exist: %s", poolPath)
	}
	if !isPathOnMountedPool(poolPath) {
		return fmt.Errorf("pool not mounted at %s — refusing operation", poolPath)
	}
	return nil
}

func checkUid(uid interface{}) (int, error) {
	var n int
	switch v := uid.(type) {
	case float64:
		n = int(v)
	case string:
		var err error
		n, err = strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("invalid UID: %v", uid)
		}
	default:
		return 0, fmt.Errorf("invalid UID type: %v", uid)
	}
	if n < 1000 || n > 65000 {
		return 0, fmt.Errorf("invalid UID: %d (must be 1000-65000)", n)
	}
	return n, nil
}

func checkPermission(perm string) error {
	if perm != "ro" && perm != "rw" {
		return fmt.Errorf("invalid permission: %s (must be ro or rw)", perm)
	}
	return nil
}

// ═══════════════════════════════════
// Input validation helpers
// ═══════════════════════════════════

var (
	reAlphanumDash   = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	reDomain         = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)
	reEmail          = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	reSnapshotName   = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)
	reZfsDatasetSnap = regexp.MustCompile(`^[a-zA-Z0-9_-]+(/[a-zA-Z0-9._-]+)*@[a-zA-Z0-9._-]{1,128}$`)
	reDevName        = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	reUnixUser       = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	reContainerId    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	reWgInterface    = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,15}$`)
	reAbsPath        = regexp.MustCompile(`^/[a-zA-Z0-9/._ -]+$`)
)

func isValidDomain(s string) bool    { return reDomain.MatchString(s) && len(s) <= 253 }
func isValidEmail(s string) bool     { return reEmail.MatchString(s) && len(s) <= 254 }
func isValidSnap(s string) bool      { return reZfsDatasetSnap.MatchString(s) }
func isValidSnapName(s string) bool  { return reSnapshotName.MatchString(s) }
func isValidDev(s string) bool       { return reDevName.MatchString(s) && len(s) <= 64 }
func isValidUnixUser(s string) bool  { return reUnixUser.MatchString(s) && len(s) <= 32 }
func isValidContainer(s string) bool { return reContainerId.MatchString(s) && len(s) <= 128 }
func isValidWgIface(s string) bool   { return reWgInterface.MatchString(s) }
func isValidSafePath(s string) bool  { return reAbsPath.MatchString(s) && !strings.Contains(s, "..") }
