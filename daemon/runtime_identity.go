package main

import (
	"strconv"
	"strings"
)

// runtimeIdentity declara QUÉ variables de entorno usa una app para fijar el
// UID/GID con el que corre su proceso. NO contiene el UID: contiene los NOMBRES
// de las env vars que NimOS rellena con el UID/GID ÚNICO que él asigna.
// Ver documents/.../PERMISOS-DESIGN addendum (contrato runtimeIdentity).
//
//	gitea       → {uidEnv:"USER_UID", gidEnv:"USER_GID"}
//	linuxserver → {uidEnv:"PUID",     gidEnv:"PGID"}
//	synapse     → {uidEnv:"UID",      gidEnv:"GID"}
type runtimeIdentity struct {
	UIDEnv string `json:"uidEnv"`
	GIDEnv string `json:"gidEnv"`
}

// strFromAny coacciona un valor JSON a string (vacío si no lo es).
func strFromAny(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// parseRuntimeIdentity extrae el bloque runtimeIdentity del body de instalación.
// Devuelve nil si no está presente o no declara ninguna env var (app que no
// necesita inyección: corre como root o con Config.User fijo en la imagen).
func parseRuntimeIdentity(body map[string]interface{}) *runtimeIdentity {
	raw, ok := body["runtimeIdentity"].(map[string]interface{})
	if !ok {
		return nil
	}
	rt := &runtimeIdentity{
		UIDEnv: strings.TrimSpace(strFromAny(raw["uidEnv"])),
		GIDEnv: strings.TrimSpace(strFromAny(raw["gidEnv"])),
	}
	if rt.UIDEnv == "" && rt.GIDEnv == "" {
		return nil
	}
	return rt
}

// runtimeIdentityEnv devuelve las env vars a inyectar en el .env para que la app
// corra con su UID/GID asignado, coincidiendo con el dueño de su volumen.
//
// PURA y testeable. Los valores son enteros como string → NO necesitan escape
// (ver ENV-ESCAPE-HALLAZGO: el único carácter problemático es '$', y un entero
// no lo tiene). Devuelve mapa vacío si rt es nil o no declara nombres.
func runtimeIdentityEnv(rt *runtimeIdentity, uid, gid int) map[string]string {
	out := map[string]string{}
	if rt == nil {
		return out
	}
	if e := strings.TrimSpace(rt.UIDEnv); e != "" {
		out[e] = strconv.Itoa(uid)
	}
	if e := strings.TrimSpace(rt.GIDEnv); e != "" {
		out[e] = strconv.Itoa(gid)
	}
	return out
}
