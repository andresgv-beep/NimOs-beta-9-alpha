package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// computeNextRun parses a schedule string and returns the next run time as ISO 8601.
// Supported formats:
//   - "daily HH:MM"       → every day at HH:MM UTC
//   - "weekly DAY HH:MM"  → every week on DAY at HH:MM UTC (mon, tue, wed, thu, fri, sat, sun)
//   - "hourly"            → every hour at :00
//   - "every Nh"          → every N hours from now
//   - "every Nm"          → every N minutes from now
func computeNextRun(schedule string) string {
	now := time.Now().UTC()
	parts := strings.Fields(strings.ToLower(schedule))

	if len(parts) == 0 {
		return now.Add(24 * time.Hour).Format(time.RFC3339)
	}

	switch parts[0] {
	case "daily":
		if len(parts) >= 2 {
			hm := strings.Split(parts[1], ":")
			if len(hm) == 2 {
				h := parseInt(hm[0], 2)
				m := parseInt(hm[1], 0)
				next := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.UTC)
				if next.Before(now) {
					next = next.Add(24 * time.Hour)
				}
				return next.Format(time.RFC3339)
			}
		}
		// Default: next day same time
		return now.Add(24 * time.Hour).Format(time.RFC3339)

	case "weekly":
		if len(parts) >= 3 {
			dayMap := map[string]time.Weekday{
				"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
				"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
			}
			targetDay, ok := dayMap[parts[1]]
			if !ok {
				return now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
			}
			hm := strings.Split(parts[2], ":")
			h, m := 2, 0
			if len(hm) == 2 {
				h = parseInt(hm[0], 2)
				m = parseInt(hm[1], 0)
			}
			daysUntil := int(targetDay-now.Weekday()+7) % 7
			if daysUntil == 0 {
				candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.UTC)
				if candidate.Before(now) {
					daysUntil = 7
				}
			}
			next := time.Date(now.Year(), now.Month(), now.Day()+daysUntil, h, m, 0, 0, time.UTC)
			return next.Format(time.RFC3339)
		}
		return now.Add(7 * 24 * time.Hour).Format(time.RFC3339)

	case "hourly":
		next := now.Truncate(time.Hour).Add(time.Hour)
		return next.Format(time.RFC3339)

	case "every":
		if len(parts) >= 2 {
			s := parts[1]
			if strings.HasSuffix(s, "h") {
				n := parseInt(strings.TrimSuffix(s, "h"), 1)
				return now.Add(time.Duration(n) * time.Hour).Format(time.RFC3339)
			}
			if strings.HasSuffix(s, "m") {
				n := parseInt(strings.TrimSuffix(s, "m"), 60)
				return now.Add(time.Duration(n) * time.Minute).Format(time.RFC3339)
			}
		}
		return now.Add(24 * time.Hour).Format(time.RFC3339)
	}

	// Fallback: try "HH:MM" as daily
	if len(parts) == 1 && strings.Contains(parts[0], ":") {
		hm := strings.Split(parts[0], ":")
		if len(hm) == 2 {
			h := parseInt(hm[0], 2)
			m := parseInt(hm[1], 0)
			next := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.UTC)
			if next.Before(now) {
				next = next.Add(24 * time.Hour)
			}
			return next.Format(time.RFC3339)
		}
	}

	return now.Add(24 * time.Hour).Format(time.RFC3339)
}

// executeBackupJob runs a backup job synchronously.
// It creates a snapshot, sends incremental data to the remote, and records history.
func executeBackupJob(job map[string]interface{}) map[string]interface{} {
	jobID, _ := job["id"].(string)
	jobName, _ := job["name"].(string)
	deviceID, _ := job["deviceId"].(string)
	fsType, _ := job["fsType"].(string)
	source, _ := job["source"].(string)
	dest, _ := job["dest"].(string)
	lastSnap, _ := job["lastSnap"].(string)

	// Prevent double execution
	backupRunningJobsMu.Lock()
	if backupRunningJobs[jobID] {
		backupRunningJobsMu.Unlock()
		return map[string]interface{}{"error": "Job is already running"}
	}
	backupRunningJobs[jobID] = true
	backupRunningJobsMu.Unlock()
	defer func() {
		backupRunningJobsMu.Lock()
		delete(backupRunningJobs, jobID)
		backupRunningJobsMu.Unlock()
	}()

	// Update status → running
	dbBackupJobUpdate(jobID, map[string]interface{}{"status": "running"})

	// Get device address for SSH
	device, err := dbBackupDeviceGet(deviceID)
	if err != nil {
		recordBackupFailure(jobID, jobName, deviceID, dest, "device not found: "+err.Error())
		return map[string]interface{}{"error": "Device not found"}
	}

	remoteAddr, _ := device["addr"].(string)
	startTime := time.Now()

	// Determine the right transport address
	// If WireGuard is active, use the WG local IP of the remote
	if wg, ok := device["wireguard"].(map[string]interface{}); ok {
		if active, _ := wg["active"].(bool); active {
			if wgIP, _ := wg["localIP"].(string); wgIP != "" {
				// Strip CIDR notation if present
				if idx := strings.Index(wgIP, "/"); idx > 0 {
					wgIP = wgIP[:idx]
				}
				remoteAddr = wgIP
			}
		}
	}

	timestamp := time.Now().UTC().Format("20060102-150405")
	var snapName string
	var sendSpec, recvSpec pipeCmdSpec

	// SECURITY (C1): validate user-controlled paths before they reach exec.
	if err := validateBackupPath("source", source); err != nil {
		recordBackupFailure(jobID, jobName, deviceID, dest, "invalid source: "+err.Error())
		return map[string]interface{}{"error": "Invalid source: " + err.Error()}
	}
	if err := validateBackupPath("dest", dest); err != nil {
		recordBackupFailure(jobID, jobName, deviceID, dest, "invalid dest: "+err.Error())
		return map[string]interface{}{"error": "Invalid dest: " + err.Error()}
	}

	// LOGIC-021: Use per-device SSH options (host key verification if available)
	sshOpts := sshOptsForDevice(deviceID)

	switch fsType {
	case "btrfs":
		snapName = fmt.Sprintf("nimbackup-%s", timestamp)
		snapPath := fmt.Sprintf("%s/.snapshots/%s", source, snapName)

		// 1. Ensure .snapshots directory exists
		os.MkdirAll(source+"/.snapshots", 0755)

		// 2. Create readonly snapshot
		if errMsg, err := btrfsSnapshotCreate(source, snapPath); err != nil {
			recordBackupFailure(jobID, jobName, deviceID, dest, "snapshot failed: "+errMsg)
			return map[string]interface{}{"error": "Failed to create snapshot: " + errMsg}
		}

		// 3. Send (incremental if previous snapshot exists).
		// SECURITY (C1): no shell. Two argv-separated commands wired via io.Pipe.
		// ssh options are tokenized to argv; the remote command is a fixed
		// "btrfs receive <dest>" with dest already validated above.
		if lastSnap != "" {
			lastSnapPath := fmt.Sprintf("%s/.snapshots/%s", source, lastSnap)
			sendSpec = pipeCmdSpec{name: "btrfs", args: []string{"send", "-p", lastSnapPath, snapPath}}
		} else {
			sendSpec = pipeCmdSpec{name: "btrfs", args: []string{"send", snapPath}}
		}

		sshArgs := splitSSHOpts(sshOpts)
		sshArgs = append(sshArgs, "root@"+remoteAddr, "btrfs receive "+dest)
		recvSpec = pipeCmdSpec{name: "ssh", args: sshArgs}

	default:
		recordBackupFailure(jobID, jobName, deviceID, dest, "unsupported filesystem: "+fsType)
		return map[string]interface{}{"error": "Unsupported filesystem type: " + fsType}
	}

	// Execute the send/receive (shell-free pipeline)
	logMsg("backup: executing job %s → %s", jobName, remoteAddr)
	out, ok := runPipe(backupPipeTimeout, sendSpec, recvSpec)

	elapsed := int(time.Since(startTime).Seconds())

	if !ok {
		recordBackupFailure(jobID, jobName, deviceID, dest, "send/receive failed: "+out)
		return map[string]interface{}{"error": "Backup failed: " + out}
	}

	// Estimate bytes transferred from BTRFS snapshot exclusive size.
	var transferredBytes int64
	snapPath := fmt.Sprintf("%s/.snapshots/%s", source, snapName)
	if sizeOut, ok := runSafe("btrfs", "subvolume", "show", snapPath); ok {
		for _, line := range strings.Split(sizeOut, "\n") {
			if strings.Contains(line, "Exclusive") {
				fields := strings.Fields(line)
				if len(fields) > 0 {
					transferredBytes = parseByteSize(fields[len(fields)-1])
				}
			}
		}
	}
	_ = fsType // backward-compat: still on the signature, BTRFS-only now

	// Record success
	schedule, _ := job["schedule"].(string)
	nextRun := computeNextRun(schedule)

	dbBackupJobUpdate(jobID, map[string]interface{}{
		"status":   "ok",
		"lastRun":  time.Now().UTC().Format(time.RFC3339),
		"nextRun":  nextRun,
		"lastSize": transferredBytes,
		"lastSnap": snapName,
	})

	dbBackupHistoryAdd(map[string]interface{}{
		"id":       backupID("hist"),
		"jobId":    jobID,
		"jobName":  jobName,
		"deviceId": deviceID,
		"dest":     dest,
		"ok":       true,
		"bytes":    transferredBytes,
		"duration": elapsed,
	})

	// Apply retention policy (clean old snapshots)
	go applyRetention(job)

	logMsg("backup: job %s completed in %ds, %d bytes", jobName, elapsed, transferredBytes)
	return map[string]interface{}{
		"ok":       true,
		"bytes":    transferredBytes,
		"duration": elapsed,
		"snapshot": snapName,
	}
}

func recordBackupFailure(jobID, jobName, deviceID, dest, errMsg string) {
	logMsg("backup: job %s failed: %s", jobName, errMsg)
	dbBackupJobUpdate(jobID, map[string]interface{}{
		"status":  "error",
		"lastRun": time.Now().UTC().Format(time.RFC3339),
	})
	dbBackupHistoryAdd(map[string]interface{}{
		"id":       backupID("hist"),
		"jobId":    jobID,
		"jobName":  jobName,
		"deviceId": deviceID,
		"dest":     dest,
		"ok":       false,
		"error":    errMsg,
	})
}

func parseByteSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var n int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int64(c-'0')
		}
	}
	// Handle suffixes (K, M, G, T)
	su := strings.ToUpper(s)
	if strings.HasSuffix(su, "K") || strings.HasSuffix(su, "KIB") {
		n *= 1024
	} else if strings.HasSuffix(su, "M") || strings.HasSuffix(su, "MIB") {
		n *= 1024 * 1024
	} else if strings.HasSuffix(su, "G") || strings.HasSuffix(su, "GIB") {
		n *= 1024 * 1024 * 1024
	} else if strings.HasSuffix(su, "T") || strings.HasSuffix(su, "TIB") {
		n *= 1024 * 1024 * 1024 * 1024
	}
	return n
}

func startBackupScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	backupSchedulerCancel = cancel

	go func() {
		logMsg("backup: scheduler started")
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logMsg("backup: scheduler stopped")
				return
			case <-ticker.C:
				checkAndRunDueJobs()
			}
		}
	}()
}

func stopBackupScheduler() {
	if backupSchedulerCancel != nil {
		backupSchedulerCancel()
	}
}

func checkAndRunDueJobs() {
	jobs, err := dbBackupJobList()
	if err != nil {
		return
	}

	now := time.Now().UTC()

	for _, job := range jobs {
		enabled, _ := job["enabled"].(bool)
		if !enabled {
			continue
		}

		nextRunStr, _ := job["nextRun"].(string)
		if nextRunStr == "" {
			continue
		}

		nextRun, err := time.Parse(time.RFC3339, nextRunStr)
		if err != nil {
			continue
		}

		if now.After(nextRun) {
			// Time to run this job
			go executeBackupJob(job)
		}
	}
}
