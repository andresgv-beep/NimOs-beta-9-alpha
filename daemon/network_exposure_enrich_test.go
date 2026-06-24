// Test conceptual de enrichExposureDTO · el cruce app_id → landing_path
package main

import "testing"

func TestEnrichExposureDTO_Logic(t *testing.T) {
	// Simular el cruce: apps expuestas + configMap
	apps := []*NetworkExposedApp{
		{AppID: "pihole", Subdomain: "pihole"},
		{AppID: "codeserver", Subdomain: "codeserver"},
		{AppID: "jellyfin", Subdomain: "jellyfin"},
	}
	configMap := map[string]string{
		"pihole":     `{"landing_path":"/admin"}`,
		"codeserver": `{}`,
		// jellyfin no está en el mapa
	}
	// Replicar la lógica del cruce
	for _, app := range apps {
		if cfg, ok := configMap[app.AppID]; ok {
			if lp := landingPathFromConfig(cfg); lp != "" {
				app.LandingPath = lp
			}
		}
	}
	// pihole → /admin
	if apps[0].LandingPath != "/admin" {
		t.Errorf("pihole landing_path: got %q, want /admin", apps[0].LandingPath)
	}
	// codeserver → "" (config vacío)
	if apps[1].LandingPath != "" {
		t.Errorf("codeserver landing_path debería ser vacío: got %q", apps[1].LandingPath)
	}
	// jellyfin → "" (no en el mapa)
	if apps[2].LandingPath != "" {
		t.Errorf("jellyfin landing_path debería ser vacío: got %q", apps[2].LandingPath)
	}
}
