package main

import (
	"fmt"
	"time"
)

// dbSharesListRaw returns typed share structs from the DB.
// This is the primary query — other functions build on top of it.
func dbSharesListRaw() ([]DBShare, error) {
	rows, err := db.Query(`SELECT name, display_name, description, path, volume, pool, recycle_bin, created_by, created_at FROM shares ORDER BY created_at`)
	if err != nil {
		return nil, err
	}

	// Collect rows first, then close before subqueries
	type shareRow struct {
		DBShare
		recycleBinInt int
	}
	var shareRows []shareRow
	for rows.Next() {
		var s shareRow
		rows.Scan(&s.Name, &s.DisplayName, &s.Description, &s.Path, &s.Volume, &s.Pool, &s.recycleBinInt, &s.CreatedBy, &s.CreatedAt)
		s.RecycleBin = s.recycleBinInt == 1
		shareRows = append(shareRows, s)
	}
	rows.Close()

	var shares []DBShare
	for _, sr := range shareRows {
		s := sr.DBShare
		s.Permissions = map[string]string{}

		prows, _ := db.Query(`SELECT username, permission FROM share_permissions WHERE share_name = ?`, s.Name)
		if prows != nil {
			for prows.Next() {
				var u, p string
				prows.Scan(&u, &p)
				s.Permissions[u] = p
			}
			prows.Close()
		}

		arows, _ := db.Query(`SELECT app_id, uid, permission FROM app_permissions WHERE share_name = ?`, s.Name)
		if arows != nil {
			for arows.Next() {
				var ap AppPermission
				arows.Scan(&ap.AppId, &ap.Uid, &ap.Permission)
				s.AppPermissions = append(s.AppPermissions, ap)
			}
			arows.Close()
		}
		if s.AppPermissions == nil {
			s.AppPermissions = []AppPermission{}
		}

		shares = append(shares, s)
	}
	if shares == nil {
		shares = []DBShare{}
	}
	return shares, nil
}

// dbSharesGetRaw returns a single typed share struct.
func dbSharesGetRaw(name string) (*DBShare, error) {
	raw, err := dbSharesListRaw()
	if err != nil {
		return nil, err
	}
	for _, s := range raw {
		if s.Name == name {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("share not found: %s", name)
}

func dbSharesCreate(name, displayName, desc, path, volume, pool, createdBy string) error {
	_, err := db.Exec(`INSERT INTO shares (name, display_name, description, path, volume, pool, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		name, displayName, desc, path, volume, pool, createdBy, time.Now().UTC().Format(time.RFC3339Nano))
	return dirtyIfOK(err)
}

func dbSharesUpdate(name string, u ShareUpdate) error {
	sets := []string{}
	args := []interface{}{}
	if u.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *u.Description)
	}
	if u.RecycleBin != nil {
		sets = append(sets, "recycle_bin = ?")
		if *u.RecycleBin {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, name)
	query := "UPDATE shares SET " + joinStrings(sets, ", ") + " WHERE name = ?"
	_, err := db.Exec(query, args...)
	return dirtyIfOK(err)
}

func dbSharesDelete(name string) error {
	_, err := db.Exec(`DELETE FROM shares WHERE name = ?`, name)
	return dirtyIfOK(err)
}

func dbShareSetPermission(shareName, username, permission string) error {
	if permission == "none" || permission == "" {
		_, err := db.Exec(`DELETE FROM share_permissions WHERE share_name = ? AND username = ?`, shareName, username)
		return dirtyIfOK(err)
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO share_permissions (share_name, username, permission) VALUES (?, ?, ?)`,
		shareName, username, permission)
	return dirtyIfOK(err)
}

func dbShareSetAppPermission(shareName, appId string, uid int, permission string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO app_permissions (share_name, app_id, uid, permission) VALUES (?, ?, ?, ?)`,
		shareName, appId, uid, permission)
	return dirtyIfOK(err)
}

func dbShareRemoveAppPermission(shareName, appId string) error {
	_, err := db.Exec(`DELETE FROM app_permissions WHERE share_name = ? AND app_id = ?`, shareName, appId)
	return dirtyIfOK(err)
}

func dbPrefsSet(username, key, value string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO preferences (username, key, value) VALUES (?, ?, ?)`,
		username, key, value)
	return err
}

func dbPrefsDelete(username, key string) error {
	_, err := db.Exec(`DELETE FROM preferences WHERE username = ? AND key = ?`, username, key)
	return err
}
