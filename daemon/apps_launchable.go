// apps_launchable.go — Endpoint /api/apps/launchable (read model / API projection).
//
// Compone la información que el Launcher necesita para ABRIR cada app, cruzando
// tres fuentes (cada una de SU dominio):
//   · docker_apps   → apps instaladas (id, name, puerto, config/landing_path)
//   · network exposure → subdominio de cada app expuesta
//   · network config   → dominio base + puerto HTTPS de Caddy
//
// DOCTRINA (la que define Andrés): el BACKEND compone la información PERSISTENTE
// (exposición, subdominio, landing_path, dominio, puerto HTTPS). El FRONTEND usa
// la información CONTEXTUAL (hostname/protocolo por el que entró el usuario).
// Cada capa usa lo que realmente conoce.
//
// Contrato de salida por app:
//   {
//     "id": "pihole",
//     "name": "Pi-hole",
//     "local_port": 8088,                  ← el Launcher arma la URL local con SU hostname
//     "landing_path": "/admin",
//     "open_url_external": "https://pihole.dominio:444/admin",  ← completa (o "" si no expuesta)
//     "exposed": true
//   }

package main

import (
	"fmt"
	"net/http"
)

// LaunchableApp · el DTO que consume el Launcher.
type LaunchableApp struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Icon            string `json:"icon,omitempty"`
	LocalPort       int    `json:"local_port"`
	LandingPath     string `json:"landing_path,omitempty"`
	OpenURLExternal string `json:"open_url_external,omitempty"`
	Exposed         bool   `json:"exposed"`
	OpenMode        string `json:"open_mode,omitempty"` // "internal"|"external"|"game" · el Launcher decide cómo abrir
}

// buildExternalURL compone la URL externa (Caddy) de una app expuesta.
// PURA · testeable. Reusa la misma regla de puerto que el frontend (443 se omite).
//
// @param subdomain    ej. "pihole"
// @param baseDomain   ej. "nimosbarraca1.duckdns.org"
// @param httpsPort    ej. 444 (puerto de Caddy)
// @param landingPath  ej. "/admin"
// @returns            "https://pihole.nimosbarraca1.duckdns.org:444/admin" o "" si falta dato
func buildExternalURL(subdomain, baseDomain string, httpsPort int, landingPath string) string {
	if subdomain == "" || baseDomain == "" {
		return ""
	}
	portPart := ""
	if httpsPort != 0 && httpsPort != 443 {
		portPart = fmt.Sprintf(":%d", httpsPort)
	}
	return fmt.Sprintf("https://%s.%s%s%s", subdomain, baseDomain, portPart, landingPath)
}

// buildLaunchableApps compone la lista de apps lanzables cruzando las fuentes.
// Separada del handler para poder testearla (recibe los datos ya cargados).
//
// @param apps        apps instaladas (docker_apps)
// @param exposedByID map app_id → exposición (subdomain)
// @param baseDomain  dominio base de Caddy
// @param httpsPort   puerto HTTPS de Caddy
// @returns           los DTO listos para el Launcher
func buildLaunchableApps(
	apps []*DBDockerApp,
	exposedByID map[string]*NetworkExposedApp,
	baseDomain string,
	httpsPort int,
) []LaunchableApp {
	out := make([]LaunchableApp, 0, len(apps))
	for _, a := range apps {
		landingPath := landingPathFromConfig(a.Config)
		la := LaunchableApp{
			ID:          a.ID,
			Name:        a.Name,
			Icon:        a.Icon,
			LocalPort:   a.Port,
			LandingPath: landingPath,
			OpenMode:    a.OpenMode,
		}
		// Si la app está expuesta, componer la URL externa (Caddy).
		if exp, ok := exposedByID[a.ID]; ok && exp != nil {
			la.Exposed = true
			la.OpenURLExternal = buildExternalURL(exp.Subdomain, baseDomain, httpsPort, landingPath)
		}
		out = append(out, la)
	}
	return out
}

// handleLaunchableApps · GET /api/apps/launchable.
func handleLaunchableApps(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	ctx := r.Context()

	// 1. Apps instaladas (docker_apps).
	apps, err := appsRepo.ListDockerApps(ctx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 2. Exposiciones · map app_id → exposición (un query, anti N+1).
	exposedByID := map[string]*NetworkExposedApp{}
	baseDomain := ""
	httpsPort := 443
	if networkRepo != nil {
		exposed, err := networkRepo.ListExposedApps(ctx)
		if err == nil {
			for _, e := range exposed {
				exposedByID[e.AppID] = e
			}
		}
		if cfg, err := networkRepo.GetExposureConfig(ctx); err == nil {
			baseDomain = cfg.BaseDomain
			httpsPort = cfg.HTTPSPort
		}
	}

	// 3. Componer el DTO (read model).
	launchable := buildLaunchableApps(apps, exposedByID, baseDomain, httpsPort)
	jsonOk(w, launchable)
}
