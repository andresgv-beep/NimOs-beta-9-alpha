// maintenance_ghost_apps_test.go — Verifica la limpieza de apps fantasma:
// la decisión pura de gracia y el barrido con BD real (fila sin contenedor
// ni stack desaparece; las protegidas se quedan).

package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestWithinGhostGrace(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		installedAt string
		want        bool
	}{
		{"instalada hace 5min → gracia", now.Add(-5 * time.Minute).Format(time.RFC3339), true},
		{"instalada hace 59min → gracia", now.Add(-59 * time.Minute).Format(time.RFC3339), true},
		{"instalada hace 2h → sin gracia", now.Add(-2 * time.Hour).Format(time.RFC3339), false},
		{"instalada hace semanas → sin gracia", "2026-06-20T23:23:24Z", false},
		{"fecha vacía → gracia (duda)", "", true},
		{"fecha corrupta → gracia (duda)", "no-es-fecha", true},
	}
	for _, c := range cases {
		if got := withinGhostGrace(c.installedAt, now); got != c.want {
			t.Errorf("%s: withinGhostGrace(%q) = %v, want %v", c.name, c.installedAt, got, c.want)
		}
	}
}

func TestGhostAppSweep_RemovesGhostKeepsRecent(t *testing.T) {
	defer setupShieldTest(t)() // da BD global limpia (sirve para cualquier tabla)
	ctx := context.Background()

	// Esquema de apps + repo sobre la BD de test.
	if err := initAppsSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	prevRepo := appsRepo
	appsRepo = NewAppsRepo(db)
	defer func() { appsRepo = prevRepo }()

	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)

	seed := func(id, installedAt string) {
		if _, err := db.Exec(`INSERT INTO docker_apps (id, name, type, installed_at, installed_by) VALUES (?, ?, 'stack', ?, 'test')`,
			id, id, installedAt); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("fantasma-junio", old)
	seed("recien-instalada", recent)

	// Sin Docker de verdad en los tests: replicamos el corazón del barrido
	// (la parte pura + BD) con los checks de contenedor/stack forzados a
	// "no existe", que es el caso del fantasma.
	apps, err := appsRepo.ListDockerApps(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	now := time.Now()
	for _, app := range apps {
		if withinGhostGrace(app.InstalledAt, now) {
			continue // la reciente se salva por gracia
		}
		// (sin contenedor y sin stack — el escenario fantasma)
		if err := appsRepo.DeleteDockerApp(ctx, app.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}

	rest, _ := appsRepo.ListDockerApps(ctx)
	if len(rest) != 1 || rest[0].ID != "recien-instalada" {
		ids := []string{}
		for _, a := range rest {
			ids = append(ids, a.ID)
		}
		t.Fatalf("debía sobrevivir solo 'recien-instalada'; quedan: %v", ids)
	}
}

func TestGhostAppSweep_RefusesWithoutDocker(t *testing.T) {
	// En el entorno de test no hay Docker instalado (o no responde): la
	// tarea completa debe SALTARSE sin tocar nada (refuse-if-uncertain).
	// Nota: si la máquina de CI tuviera Docker sano, este test se salta a
	// sí mismo — su objetivo es cubrir el guard, no pelearse con el entorno.
	task := &ghostAppSweepTask{}
	if isDockerInstalledGo() {
		if st := checkDockerDataRoot(); st.Safe {
			t.Skip("Docker real presente y sano en el entorno de test")
		}
	}
	res := task.Run(context.Background())
	if !res.Skipped {
		t.Fatalf("sin Docker sano la tarea debe saltarse; res=%+v", res)
	}
	if res.ItemsRemoved != 0 {
		t.Fatal("una tarea saltada jamás borra nada")
	}
}

func TestAppHasStackDir(t *testing.T) {
	dir := t.TempDir()
	if appHasStackDir(dir, "navidrome") {
		t.Fatal("sin stacks/navidrome no debe reportar stack")
	}
	if appHasStackDir("", "navidrome") {
		t.Fatal("dockerPath vacío → sin stack (no fantasma por esta vía)")
	}
	// crear stacks/navidrome → detectado
	if err := os.MkdirAll(dir+"/stacks/navidrome", 0o755); err != nil {
		t.Fatal(err)
	}
	if !appHasStackDir(dir, "navidrome") {
		t.Fatal("con stacks/navidrome presente debe reportar stack")
	}
}
