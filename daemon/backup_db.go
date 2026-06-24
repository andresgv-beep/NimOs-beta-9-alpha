package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func createBackupTables() error {
	schema := `
	CREATE TABLE IF NOT EXISTS backup_devices (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		addr         TEXT NOT NULL,
		type         TEXT NOT NULL DEFAULT 'nas',
		purposes     TEXT DEFAULT '[]',
		sync_pairs   TEXT DEFAULT '[]',
		pair_token_hash TEXT DEFAULT '',
		pair_token_outbound TEXT DEFAULT '',
		ssh_host_key TEXT DEFAULT '',
		allow_ip_auth INTEGER DEFAULT 0,
		wg_active    INTEGER DEFAULT 0,
		wg_public_key TEXT DEFAULT '',
		wg_endpoint  TEXT DEFAULT '',
		wg_allowed_ips TEXT DEFAULT '',
		wg_local_ip  TEXT DEFAULT '',
		created_at   TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS backup_jobs (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		device_id    TEXT NOT NULL,
		fs_type      TEXT NOT NULL,
		source       TEXT NOT NULL,
		dest         TEXT NOT NULL,
		schedule     TEXT NOT NULL DEFAULT 'daily 02:00',
		retention    TEXT NOT NULL DEFAULT '30d',
		status       TEXT NOT NULL DEFAULT 'ok',
		last_run     TEXT DEFAULT '',
		next_run     TEXT DEFAULT '',
		last_size    INTEGER DEFAULT 0,
		last_snap    TEXT DEFAULT '',
		enabled      INTEGER DEFAULT 1,
		created_at   TEXT NOT NULL,
		FOREIGN KEY (device_id) REFERENCES backup_devices(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS backup_history (
		id           TEXT PRIMARY KEY,
		job_id       TEXT NOT NULL,
		job_name     TEXT NOT NULL,
		device_id    TEXT NOT NULL,
		dest         TEXT NOT NULL,
		ok           INTEGER NOT NULL,
		bytes        INTEGER DEFAULT 0,
		duration     INTEGER DEFAULT 0,
		error        TEXT DEFAULT '',
		time         TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_backup_jobs_device ON backup_jobs(device_id);
	CREATE INDEX IF NOT EXISTS idx_backup_history_job ON backup_history(job_id);
	CREATE INDEX IF NOT EXISTS idx_backup_history_device ON backup_history(device_id);
	CREATE INDEX IF NOT EXISTS idx_backup_history_time ON backup_history(time DESC);

	CREATE TABLE IF NOT EXISTS remote_mounts (
		device_id    TEXT NOT NULL,
		share_name   TEXT NOT NULL,
		remote_path  TEXT NOT NULL,
		mount_point  TEXT NOT NULL,
		device_addr  TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		PRIMARY KEY (device_id, share_name)
	);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return err
	}
	// Migration: add new columns if missing (upgrade from Beta 6)
	db.Exec(`ALTER TABLE backup_devices ADD COLUMN pair_token_hash TEXT DEFAULT ''`)
	db.Exec(`ALTER TABLE backup_devices ADD COLUMN pair_token_outbound TEXT DEFAULT ''`)
	db.Exec(`ALTER TABLE backup_devices ADD COLUMN ssh_host_key TEXT DEFAULT ''`)
	// A2: per-device opt-in flag for IP-based auth fallback (default off).
	db.Exec(`ALTER TABLE backup_devices ADD COLUMN allow_ip_auth INTEGER DEFAULT 0`)
	return nil
}

// dbBackupDeviceSetPairToken stores the pair token hash for a device.
func dbBackupDeviceSetPairToken(deviceID, tokenHash string) error {
	_, err := db.Exec(`UPDATE backup_devices SET pair_token_hash = ? WHERE id = ?`, tokenHash, deviceID)
	return err
}

// dbBackupDeviceSetSSHHostKey stores the SSH host key for a paired device.
func dbBackupDeviceSetSSHHostKey(deviceID, hostKey string) error {
	_, err := db.Exec(`UPDATE backup_devices SET ssh_host_key = ? WHERE id = ?`, hostKey, deviceID)
	return err
}

func dbBackupDeviceCreate(dev map[string]interface{}) error {
	id, _ := dev["id"].(string)
	name, _ := dev["name"].(string)
	addr, _ := dev["addr"].(string)
	devType, _ := dev["type"].(string)
	if devType == "" {
		devType = "nas"
	}

	purposesJSON := "[]"
	if p, ok := dev["purposes"]; ok {
		if b, err := json.Marshal(p); err == nil {
			purposesJSON = string(b)
		}
	}

	syncPairsJSON := "[]"
	if sp, ok := dev["syncPairs"]; ok {
		if b, err := json.Marshal(sp); err == nil {
			syncPairsJSON = string(b)
		}
	}

	_, err := db.Exec(`INSERT INTO backup_devices (id, name, addr, type, purposes, sync_pairs, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, addr, devType, purposesJSON, syncPairsJSON,
		time.Now().UTC().Format(time.RFC3339))
	return err
}

func dbBackupDeviceList() ([]map[string]interface{}, error) {
	rows, err := db.Query(`SELECT id, name, addr, type, purposes, sync_pairs, pair_token_hash, pair_token_outbound, ssh_host_key, allow_ip_auth, wg_active,
		wg_public_key, wg_endpoint, wg_allowed_ips, wg_local_ip, created_at FROM backup_devices ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []map[string]interface{}
	for rows.Next() {
		var id, name, addr, devType, purposesJSON, syncPairsJSON, pairTokenHash, pairTokenOutbound, sshHostKey string
		var allowIPAuth int
		var wgActive int
		var wgPub, wgEndpoint, wgAllowed, wgLocal, createdAt string

		if err := rows.Scan(&id, &name, &addr, &devType, &purposesJSON, &syncPairsJSON, &pairTokenHash, &pairTokenOutbound, &sshHostKey,
			&allowIPAuth, &wgActive, &wgPub, &wgEndpoint, &wgAllowed, &wgLocal, &createdAt); err != nil {
			continue
		}

		dev := map[string]interface{}{
			"id":                id,
			"name":              name,
			"addr":              addr,
			"type":              devType,
			"pairTokenHash":     pairTokenHash,
			"pairTokenOutbound": pairTokenOutbound,
			"sshHostKey":        sshHostKey,
			"allowIpAuth":       allowIPAuth == 1,
			"createdAt":         createdAt,
		}

		// Parse purposes JSON
		var purposes []string
		if json.Unmarshal([]byte(purposesJSON), &purposes) == nil {
			dev["purposes"] = purposes
		} else {
			dev["purposes"] = []string{}
		}

		// Parse sync pairs JSON
		var syncPairs []interface{}
		if json.Unmarshal([]byte(syncPairsJSON), &syncPairs) == nil {
			dev["syncPairs"] = syncPairs
		} else {
			dev["syncPairs"] = []interface{}{}
		}

		// WireGuard info (only if active)
		if wgActive == 1 {
			dev["wireguard"] = map[string]interface{}{
				"active":     true,
				"publicKey":  wgPub,
				"endpoint":   wgEndpoint,
				"allowedIPs": wgAllowed,
				"localIP":    wgLocal,
			}
		}

		devices = append(devices, dev)
	}

	if devices == nil {
		devices = []map[string]interface{}{}
	}
	return devices, nil
}

func dbBackupDeviceGet(id string) (map[string]interface{}, error) {
	devices, err := dbBackupDeviceList()
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if d["id"] == id {
			return d, nil
		}
	}
	return nil, fmt.Errorf("device not found: %s", id)
}

func dbBackupDeviceDelete(id string) error {
	res, err := db.Exec(`DELETE FROM backup_devices WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("device not found")
	}
	// Cascade: also remove jobs and history for this device
	db.Exec(`DELETE FROM backup_jobs WHERE device_id = ?`, id)
	db.Exec(`DELETE FROM backup_history WHERE device_id = ?`, id)
	// Remove WireGuard peer if exists
	removeWGPeer(id) // Errors are non-fatal — peer may not exist
	return nil
}

func dbBackupDeviceUpdatePurposes(id string, purposes []string) error {
	b, err := json.Marshal(purposes)
	if err != nil {
		return err
	}
	res, err := db.Exec(`UPDATE backup_devices SET purposes = ? WHERE id = ?`, string(b), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("device not found")
	}
	return nil
}

func dbBackupDeviceUpdateSyncPairs(id string, pairs interface{}) error {
	b, err := json.Marshal(pairs)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE backup_devices SET sync_pairs = ? WHERE id = ?`, string(b), id)
	return err
}

func dbBackupDeviceUpdate(id, field, value string) error {
	// Only allow safe fields to be updated
	allowed := map[string]bool{"addr": true, "name": true, "type": true}
	if !allowed[field] {
		return fmt.Errorf("field %s not updatable", field)
	}
	_, err := db.Exec(fmt.Sprintf(`UPDATE backup_devices SET %s = ? WHERE id = ?`, field), value, id)
	return err
}

func dbBackupJobCreate(job map[string]interface{}) error {
	id, _ := job["id"].(string)
	name, _ := job["name"].(string)
	deviceID, _ := job["deviceId"].(string)
	fsType, _ := job["fsType"].(string)
	source, _ := job["source"].(string)
	dest, _ := job["dest"].(string)
	schedule, _ := job["schedule"].(string)
	retention, _ := job["retention"].(string)

	if schedule == "" {
		schedule = "daily 02:00"
	}
	if retention == "" {
		retention = "30d"
	}

	nextRun := computeNextRun(schedule)

	_, err := db.Exec(`INSERT INTO backup_jobs (id, name, device_id, fs_type, source, dest, schedule, retention, status, next_run, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'ok', ?, ?)`,
		id, name, deviceID, fsType, source, dest, schedule, retention,
		nextRun, time.Now().UTC().Format(time.RFC3339))
	return err
}

func dbBackupJobList() ([]map[string]interface{}, error) {
	rows, err := db.Query(`SELECT id, name, device_id, fs_type, source, dest, schedule, retention,
		status, last_run, next_run, last_size, last_snap, enabled, created_at FROM backup_jobs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []map[string]interface{}
	for rows.Next() {
		var id, name, deviceID, fsType, source, dest, schedule, retention string
		var status, lastRun, nextRun, lastSnap, createdAt string
		var lastSize int64
		var enabled int

		if err := rows.Scan(&id, &name, &deviceID, &fsType, &source, &dest, &schedule, &retention,
			&status, &lastRun, &nextRun, &lastSize, &lastSnap, &enabled, &createdAt); err != nil {
			continue
		}

		jobs = append(jobs, map[string]interface{}{
			"id":        id,
			"name":      name,
			"deviceId":  deviceID,
			"fsType":    fsType,
			"source":    source,
			"dest":      dest,
			"schedule":  schedule,
			"retention": retention,
			"status":    status,
			"lastRun":   lastRun,
			"nextRun":   nextRun,
			"lastSize":  lastSize,
			"lastSnap":  lastSnap,
			"enabled":   enabled == 1,
			"createdAt": createdAt,
		})
	}

	if jobs == nil {
		jobs = []map[string]interface{}{}
	}
	return jobs, nil
}

func dbBackupJobGet(id string) (map[string]interface{}, error) {
	jobs, err := dbBackupJobList()
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j["id"] == id {
			return j, nil
		}
	}
	return nil, fmt.Errorf("job not found: %s", id)
}

func dbBackupJobUpdate(id string, fields map[string]interface{}) error {
	// Build dynamic UPDATE — only update fields that are provided
	sets := []string{}
	args := []interface{}{}

	allowed := map[string]string{
		"name": "name", "schedule": "schedule", "retention": "retention",
		"source": "source", "dest": "dest", "status": "status",
		"lastRun": "last_run", "nextRun": "next_run", "lastSize": "last_size",
		"lastSnap": "last_snap", "enabled": "enabled",
	}

	for jsonKey, dbCol := range allowed {
		if v, ok := fields[jsonKey]; ok {
			sets = append(sets, dbCol+" = ?")
			// Handle bool → int for enabled
			if jsonKey == "enabled" {
				if b, ok := v.(bool); ok {
					if b {
						args = append(args, 1)
					} else {
						args = append(args, 0)
					}
					continue
				}
			}
			args = append(args, v)
		}
	}

	if len(sets) == 0 {
		return fmt.Errorf("no valid fields to update")
	}

	args = append(args, id)
	query := fmt.Sprintf("UPDATE backup_jobs SET %s WHERE id = ?", strings.Join(sets, ", "))
	res, err := db.Exec(query, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job not found")
	}
	return nil
}

func dbBackupJobDelete(id string) error {
	res, err := db.Exec(`DELETE FROM backup_jobs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job not found")
	}
	return nil
}

func dbBackupHistoryAdd(entry map[string]interface{}) error {
	id, _ := entry["id"].(string)
	jobID, _ := entry["jobId"].(string)
	jobName, _ := entry["jobName"].(string)
	deviceID, _ := entry["deviceId"].(string)
	dest, _ := entry["dest"].(string)
	ok := false
	if v, exists := entry["ok"]; exists {
		ok, _ = v.(bool)
	}
	var bytes int64
	if v, exists := entry["bytes"]; exists {
		switch b := v.(type) {
		case float64:
			bytes = int64(b)
		case int64:
			bytes = b
		case int:
			bytes = int64(b)
		}
	}
	duration := 0
	if v, exists := entry["duration"]; exists {
		switch d := v.(type) {
		case float64:
			duration = int(d)
		case int:
			duration = d
		}
	}
	errMsg, _ := entry["error"].(string)

	okInt := 0
	if ok {
		okInt = 1
	}

	_, err := db.Exec(`INSERT INTO backup_history (id, job_id, job_name, device_id, dest, ok, bytes, duration, error, time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, jobID, jobName, deviceID, dest, okInt, bytes, duration, errMsg,
		time.Now().UTC().Format(time.RFC3339))
	return err
}

func dbBackupHistoryList(deviceID string, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	query := `SELECT id, job_id, job_name, device_id, dest, ok, bytes, duration, error, time
		FROM backup_history ORDER BY time DESC LIMIT ?`
	args := []interface{}{limit}

	if deviceID != "" {
		query = `SELECT id, job_id, job_name, device_id, dest, ok, bytes, duration, error, time
			FROM backup_history WHERE device_id = ? ORDER BY time DESC LIMIT ?`
		args = []interface{}{deviceID, limit}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []map[string]interface{}
	for rows.Next() {
		var id, jobID, jobName, devID, dest, errMsg, ts string
		var ok int
		var bytes int64
		var duration int

		if err := rows.Scan(&id, &jobID, &jobName, &devID, &dest, &ok, &bytes, &duration, &errMsg, &ts); err != nil {
			continue
		}

		history = append(history, map[string]interface{}{
			"id":       id,
			"jobId":    jobID,
			"jobName":  jobName,
			"deviceId": devID,
			"dest":     dest,
			"ok":       ok == 1,
			"bytes":    bytes,
			"duration": duration,
			"error":    errMsg,
			"time":     ts,
		})
	}

	if history == nil {
		history = []map[string]interface{}{}
	}
	return history, nil
}

func createRemoteMountsTable() error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS remote_mounts (
		device_id    TEXT NOT NULL,
		share_name   TEXT NOT NULL,
		remote_path  TEXT NOT NULL,
		mount_point  TEXT NOT NULL,
		device_addr  TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		PRIMARY KEY (device_id, share_name)
	);`)
	return err
}

func saveMountRecord(deviceID, shareName, remotePath, mountPoint, deviceAddr string) {
	db.Exec(`INSERT OR REPLACE INTO remote_mounts (device_id, share_name, remote_path, mount_point, device_addr, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		deviceID, shareName, remotePath, mountPoint, deviceAddr,
		time.Now().UTC().Format(time.RFC3339))
}

func getMountRecord(deviceID, shareName string) map[string]interface{} {
	var remotePath, mountPoint, deviceAddr string
	err := db.QueryRow(`SELECT remote_path, mount_point, device_addr FROM remote_mounts
		WHERE device_id = ? AND share_name = ?`, deviceID, shareName).Scan(&remotePath, &mountPoint, &deviceAddr)
	if err != nil {
		return nil
	}
	return map[string]interface{}{
		"remotePath": remotePath,
		"mountPoint": mountPoint,
		"deviceAddr": deviceAddr,
	}
}
