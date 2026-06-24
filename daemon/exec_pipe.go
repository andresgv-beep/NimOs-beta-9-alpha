package main

// ═══════════════════════════════════════════════════════════════════════════════
// NimOS — Pipelined exec helper (no shell)
//
// SECURITY (C1): Replaces the previous `runShellStatic(fmt.Sprintf("... | ssh ..."))`
// pipeline used by the backup module. That pattern built a shell command line with
// interpolated, unvalidated user paths (dest/source) → command injection / RCE as root.
//
// This helper wires two exec.Cmd processes together with an io.Pipe, passing every
// argument as a separate argv element. No `sh -c`, no shell metacharacter
// interpretation. The remote `btrfs receive <dest>` is passed to ssh as a single
// argv string, but `dest` is validated (see backup_validate.go) before it ever
// reaches here, and ssh runs the remote command without a local shell.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"time"
)

// pipeCmdSpec describes one stage of a two-stage pipeline.
type pipeCmdSpec struct {
	name string
	args []string
}

// runPipe executes `left | right` by connecting left.Stdout to right.Stdin via
// an in-memory pipe. Both commands run with arguments as separate argv elements
// (no shell). Returns combined stderr output of both stages and ok=false on any
// non-zero exit or start failure.
//
// A context with timeout bounds the whole pipeline so a stalled ssh/btrfs cannot
// hang the backup worker indefinitely.
func runPipe(timeout time.Duration, left pipeCmdSpec, right pipeCmdSpec) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	lcmd := exec.CommandContext(ctx, left.name, left.args...)
	rcmd := exec.CommandContext(ctx, right.name, right.args...)
	lcmd.Env = cLocaleEnv()
	rcmd.Env = cLocaleEnv()

	// left.Stdout → right.Stdin
	pr, pw := io.Pipe()
	lcmd.Stdout = pw
	rcmd.Stdin = pr

	var lStderr, rStderr bytes.Buffer
	lcmd.Stderr = &lStderr
	rcmd.Stderr = &rStderr

	// Start the receiver first so the pipe has a reader before the sender writes.
	if err := rcmd.Start(); err != nil {
		logMsg("runPipe: failed to start %s: %v", right.name, err)
		return "start receive: " + err.Error(), false
	}
	if err := lcmd.Start(); err != nil {
		logMsg("runPipe: failed to start %s: %v", left.name, err)
		pw.Close()
		pr.Close()
		_ = rcmd.Process.Kill()
		_ = rcmd.Wait()
		return "start send: " + err.Error(), false
	}

	// Wait for the sender, then close the write end so the receiver sees EOF.
	lErr := lcmd.Wait()
	pw.Close()
	rErr := rcmd.Wait()
	pr.Close()

	if lErr != nil {
		logMsg("runPipe: send (%s) failed: %v → %s", left.name, lErr, lStderr.String())
		return "send failed: " + strTrim(lStderr.String()), false
	}
	if rErr != nil {
		logMsg("runPipe: receive (%s) failed: %v → %s", right.name, rErr, rStderr.String())
		return "receive failed: " + strTrim(rStderr.String()), false
	}

	out := strTrim(lStderr.String() + rStderr.String())
	return out, true
}

// strTrim is a tiny local trim wrapper kept here to avoid importing strings just
// for one call in this file; mirrors strings.TrimSpace behavior for our needs.
func strTrim(s string) string {
	// Trim leading/trailing ASCII whitespace.
	start := 0
	end := len(s)
	for start < end && isSpaceByte(s[start]) {
		start++
	}
	for end > start && isSpaceByte(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}
