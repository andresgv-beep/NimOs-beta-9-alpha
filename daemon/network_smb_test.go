package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func smbTestShares() []DBShare {
	return []DBShare{
		{
			Name:       "projects",
			Path:       "/nimos/pools/tank/shares/projects",
			SMBEnabled: true,
			Permissions: map[string]string{
				"alice": "rw",
				"bob":   "ro",
				"eve":   "none",
			},
		},
		{Name: "private", Path: "/nimos/pools/tank/shares/private", SMBEnabled: false},
	}
}

func TestRenderSMBConfigPreservesExternalConfig(t *testing.T) {
	existing := `[global]
   server role = standalone server
   custom option = keep-me

[external]
   path = /srv/external
`
	got, err := renderSMBConfig(existing, SMBConfig{Workgroup: "STUDIO", ServerName: "NimOS Projects"}, smbTestShares())
	if err != nil {
		t.Fatal(err)
	}
	checks := []string{
		"custom option = keep-me",
		"[external]",
		"path = /srv/external",
		"workgroup = STUDIO",
		"server string = NimOS Projects",
		"[projects]",
		"valid users = @nimos-share-projects alice bob",
		"write list = @nimos-share-projects alice",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("config no contiene %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[private]") || strings.Contains(got, " eve") {
		t.Fatalf("share deshabilitado o usuario none filtrados incorrectamente:\n%s", got)
	}
}

func TestRenderSMBConfigIsIdempotent(t *testing.T) {
	config := SMBConfig{Workgroup: "WORKGROUP", ServerName: "NimOS NAS"}
	first, err := renderSMBConfig("[global]\n   min protocol = SMB2\n", config, smbTestShares())
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderSMBConfig(first, config, smbTestShares())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("render no idempotente\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if strings.Count(second, smbManagedSharesBegin) != 1 || strings.Count(second, "[projects]") != 1 {
		t.Fatalf("bloques duplicados tras segundo render:\n%s", second)
	}
}

func TestRenderSMBConfigRejectsExternalNameCollision(t *testing.T) {
	existing := "[global]\n\n[projects]\n   path = /srv/other\n"
	_, err := renderSMBConfig(existing, defaultSMBConfig(), smbTestShares())
	if err == nil || !strings.Contains(err.Error(), "sección SMB externa") {
		t.Fatalf("esperaba colisión con share externo, err=%v", err)
	}
}

func TestRenderSMBConfigRejectsUnsafeValues(t *testing.T) {
	badPath := smbTestShares()
	badPath[0].Path = "/srv/outside"
	if _, err := renderSMBConfig("[global]\n", defaultSMBConfig(), badPath); err == nil {
		t.Fatal("ruta fuera de /nimos/pools debería rechazarse")
	}
	if _, err := renderSMBConfig("[global]\n", SMBConfig{Workgroup: "BAD WORKGROUP", ServerName: "NAS"}, nil); err == nil {
		t.Fatal("workgroup con espacios debería rechazarse")
	}
	if _, err := renderSMBConfig("[global]\n", SMBConfig{Workgroup: "OK", ServerName: "bad\nname"}, nil); err == nil {
		t.Fatal("server string con salto de línea debería rechazarse")
	}
}

func TestRenderSMBConfigRejectsTruncatedManagedBlock(t *testing.T) {
	existing := "[global]\n" + smbManagedSharesBegin + "\n[broken]\n"
	if _, err := renderSMBConfig(existing, defaultSMBConfig(), nil); err == nil {
		t.Fatal("un bloque administrado truncado no debe sobrescribirse")
	}
}

func TestMigrateToV4AddsSMBColumnIdempotently(t *testing.T) {
	conn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "smb-migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`CREATE TABLE shares (
		name TEXT PRIMARY KEY,
		display_name TEXT NOT NULL,
		path TEXT NOT NULL,
		volume TEXT NOT NULL,
		pool TEXT NOT NULL,
		created_by TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}

	previous := db
	db = conn
	defer func() { db = previous }()
	if err := migrateToV4(); err != nil {
		t.Fatal(err)
	}
	if err := migrateToV4(); err != nil {
		t.Fatalf("segunda migración debe ser no-op: %v", err)
	}

	rows, err := conn.Query(`PRAGMA table_info(shares)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "smb_enabled" {
			found = true
		}
	}
	if !found {
		t.Fatal("migración no creó shares.smb_enabled")
	}
}

func TestStopSMBServiceDoesNotUseSudoAndPropagatesFailure(t *testing.T) {
	previous := smbCommand
	defer func() { smbCommand = previous }()
	var calls []string
	smbCommand = func(command string, args ...string) (string, bool) {
		calls = append(calls, command+" "+strings.Join(args, " "))
		if command == "systemctl" && len(args) > 0 && args[0] == "stop" {
			return "unit failed", false
		}
		return "", true
	}
	if err := stopSMBService(); err == nil || !strings.Contains(err.Error(), "unit failed") {
		t.Fatalf("fallo systemctl no propagado: %v", err)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "sudo ") {
			t.Fatalf("SMB no debe invocar sudo: %v", calls)
		}
	}
}
