package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// dbUsersListRaw returns typed user summaries from the DB.
func dbUsersListRaw() ([]DBUserSummary, error) {
	rows, err := db.Query(`SELECT username, role, description, totp_enabled, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []DBUserSummary
	for rows.Next() {
		var u DBUserSummary
		var totpEnabled int
		rows.Scan(&u.Username, &u.Role, &u.Description, &totpEnabled, &u.CreatedAt)
		u.TotpEnabled = totpEnabled == 1
		users = append(users, u)
	}
	if users == nil {
		users = []DBUserSummary{}
	}
	return users, nil
}

// dbUsersGetRaw returns a typed DBUser struct.
func dbUsersGetRaw(username string) (*DBUser, error) {
	var u DBUser
	u.Username = username
	var totpEnabled int
	var backupCodesJSON string
	var updatedAt sql.NullString
	err := db.QueryRow(`SELECT password, role, description, totp_secret, totp_enabled, backup_codes, created_at, updated_at FROM users WHERE username = ?`, username).
		Scan(&u.Password, &u.Role, &u.Description, &u.TotpSecret, &totpEnabled, &backupCodesJSON, &u.CreatedAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("user not found: %s", username)
	}
	u.TotpEnabled = totpEnabled == 1

	// Parse backup codes JSON array
	if backupCodesJSON != "" {
		var codes []interface{}
		if json.Unmarshal([]byte(backupCodesJSON), &codes) == nil {
			u.BackupCodes = codes
		}
	}

	return &u, nil
}

func dbUsersCreate(username, password, role, description string) error {
	_, err := db.Exec(`INSERT INTO users (username, password, role, description, created_at) VALUES (?, ?, ?, ?, ?)`,
		username, password, role, description, time.Now().UTC().Format(time.RFC3339Nano))
	return dirtyIfOK(err)
}

func dbUsersUpdate(username string, u UserUpdate) error {
	sets := []string{}
	args := []interface{}{}
	if u.Password != nil {
		sets = append(sets, "password = ?")
		args = append(args, *u.Password)
	}
	if u.Role != nil {
		sets = append(sets, "role = ?")
		args = append(args, *u.Role)
	}
	if u.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *u.Description)
	}
	if u.TotpSecret != nil {
		sets = append(sets, "totp_secret = ?")
		args = append(args, *u.TotpSecret)
	}
	if u.TotpEnabled != nil {
		sets = append(sets, "totp_enabled = ?")
		if *u.TotpEnabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if u.BackupCodes != nil {
		sets = append(sets, "backup_codes = ?")
		jsonData, _ := json.Marshal(u.BackupCodes)
		args = append(args, string(jsonData))
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano))
	args = append(args, username)

	query := "UPDATE users SET " + joinStrings(sets, ", ") + " WHERE username = ?"
	_, err := db.Exec(query, args...)
	return dirtyIfOK(err)
}

func dbUsersDelete(username string) error {
	_, err := db.Exec(`DELETE FROM users WHERE username = ?`, username)
	return dirtyIfOK(err)
}

func dbUsersVerifyPassword(username string) (string, error) {
	var pwd string
	err := db.QueryRow(`SELECT password FROM users WHERE username = ?`, username).Scan(&pwd)
	if err != nil {
		return "", fmt.Errorf("user not found: %s", username)
	}
	return pwd, nil
}
