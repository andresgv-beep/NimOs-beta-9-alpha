// docker_postinstall.go — Motor de postInstall (Capa 2 del sistema de config).
//
// Ejecuta acciones DESPUÉS de que un stack arranca (ej. crear el primer admin
// de Matrix con register_new_matrix_user · sin terminal). Ver diseño completo
// en POSTINSTALL-DESIGN.md y el contrato en APP-CATALOG-SCHEMA.md.
//
// ON-DEMAND (una vez, al instalar), NO periódico · por eso está separado de
// NimHealth (que vigila el estado con docker ps periódico). Este motor usa
// docker inspect on-demand, que es justo lo que la regla de NimHealth permite.
//
// Multi-arquitectura: docker inspect/exec son idénticos en ARM64 y amd64.

package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// tokenRe captura {{TOKEN}} con TOKEN = letras/dígitos/underscore.
// Ej: {{ADMIN_USER}}, {{ADMIN_PASS}}.
var tokenRe = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}`)

// substituteTokens reemplaza los {{TOKEN}} de un comando por sus valores.
//
// PURA (sin I/O) · testeable a fondo. Usada para construir el comando de una
// acción postInstall a partir de los valores del modal (ADMIN_USER, etc.).
//
// Comportamiento:
//
//	· {{ADMIN_USER}} con values["ADMIN_USER"]="andres" → "andres"
//	· token sin valor en el mapa → se deja LITERAL ("{{X}}") · señal de que
//	  falta un valor (mejor que sustituir por vacío y ejecutar algo a medias)
//	· valores no-string se convierten con fmt (defensivo)
//
// @param command  el comando con tokens, ej. "register ... -u {{ADMIN_USER}}"
// @param values   { "ADMIN_USER": "andres", "ADMIN_PASS": "secreto" }
// @returns        el comando con los tokens sustituidos
func substituteTokens(command string, values map[string]string) string {
	if command == "" {
		return ""
	}
	return tokenRe.ReplaceAllStringFunc(command, func(match string) string {
		key := tokenRe.FindStringSubmatch(match)[1]
		if val, ok := values[key]; ok {
			return val
		}
		// Token sin valor → dejar literal (señal de que falta el dato).
		return match
	})
}

// findUnresolvedTokens devuelve los tokens que QUEDARON sin sustituir en un
// comando (los {{X}} cuyo valor no estaba en el mapa). Sirve para validar antes
// de ejecutar: si quedan tokens, falta algún valor y NO se debe ejecutar.
//
// @param command  comando ya pasado por substituteTokens
// @returns        lista de nombres de token sin resolver (sin las llaves)
func findUnresolvedTokens(command string) []string {
	matches := tokenRe.FindAllStringSubmatch(command, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// ofuscateSecretsInCommand devuelve una versión del comando segura para LOGUEAR,
// ocultando los valores que son secretos. NUNCA se debe loguear el comando con
// el ADMIN_PASS en claro. Recibe los valores secretos a ocultar.
//
// @param command       el comando (ya sustituido o no)
// @param secretValues  valores concretos a ofuscar (los de campos secret:true)
// @returns             el comando con los secretos reemplazados por "***"
func ofuscateSecretsInCommand(command string, secretValues []string) string {
	out := command
	for _, sv := range secretValues {
		if sv == "" {
			continue
		}
		out = strings.ReplaceAll(out, sv, "***")
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────
// FASE 2 · Salud del container (on-demand) · esperar a que esté listo
// ─────────────────────────────────────────────────────────────────────────

// Estados de salud que devuelve containerHealth.
const (
	healthHealthy   = "healthy"
	healthUnhealthy = "unhealthy"
	healthStarting  = "starting"
	healthNone      = "none"    // el container no declara healthcheck
	healthUnknown   = "unknown" // no se pudo determinar (container no existe, etc.)
)

// parseHealthOutput interpreta la salida cruda de `docker inspect` del health.
// PURA (sin docker) · testeable. containerHealth la usa tras llamar a docker.
//
// @param out  salida de docker inspect (ya con TrimSpace o no)
// @param ok   si el comando docker tuvo éxito
// @returns    uno de los health* const
func parseHealthOutput(out string, ok bool) string {
	if !ok {
		return healthUnknown
	}
	status := strings.TrimSpace(out)
	switch status {
	case healthHealthy, healthUnhealthy, healthStarting, healthNone:
		return status
	case "":
		return healthUnknown
	default:
		// Docker podría devolver algo inesperado · tratarlo como unknown.
		return healthUnknown
	}
}

// containerHealth consulta el estado de salud REAL de un container vía
// `docker inspect` (ON-DEMAND · no periódico · respeta la regla de NimHealth).
//
// El formato distingue limpio el caso "sin healthcheck":
//
//	{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}
//
// Devuelve: "healthy" | "unhealthy" | "starting" | "none" | "unknown".
//
//	· none    → el container existe pero su imagen/compose no declara healthcheck
//	· unknown → docker inspect falló (container no existe, docker caído...)
//
// @param container  nombre o id del container
// @returns          uno de los health* const
func containerHealth(container string) string {
	if container == "" {
		return healthUnknown
	}
	out, ok := runSafe("docker", "inspect", container,
		"--format", "{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}")
	return parseHealthOutput(out, ok)
}

// waitForHealthy espera a que un container alcance el estado "healthy",
// reusando waitForCondition (polling con backoff + timeout).
//
// IMPORTANTE (decisión D4 · Opción B · estricto): si el container NO declara
// healthcheck (containerHealth == "none"), NO se puede saber cuándo está listo
// de forma determinista. En ese caso devuelve false con el flag noHealthcheck
// para que el llamador dé un ERROR CLARO ("la app necesita healthcheck para
// postInstall") en vez de adivinar.
//
// @param ctx        contexto (cancelación)
// @param container  nombre del container
// @param timeout    máximo a esperar (ej. 90s · Synapse 1ª vez tarda)
// @returns ok            true si llegó a "healthy"
// @returns noHealthcheck true si el container no declara healthcheck (none)
func waitForHealthy(ctx context.Context, container string, timeout time.Duration) (ok bool, noHealthcheck bool) {
	return waitForHealthyWith(ctx, container, timeout, containerHealth)
}

// waitForHealthyWith es la versión testeable: recibe la función que consulta la
// salud (inyectada). En producción se le pasa containerHealth (docker real); en
// tests se le pasa un stub. Misma técnica que el refactor de permisos (hasFilesFn).
func waitForHealthyWith(ctx context.Context, container string, timeout time.Duration, healthFn func(string) string) (ok bool, noHealthcheck bool) {
	// Primer chequeo: ¿el container declara healthcheck siquiera?
	first := healthFn(container)
	if first == healthNone {
		return false, true // sin healthcheck · el llamador debe dar error claro
	}

	healthy := waitForCondition(ctx, "postinstall:"+container, timeout, func() bool {
		return healthFn(container) == healthHealthy
	})
	return healthy, false
}

// ─────────────────────────────────────────────────────────────────────────
// FASE 3 · Ejecutar las acciones postInstall (docker exec)
// ─────────────────────────────────────────────────────────────────────────

// PostInstallAction representa una acción del contrato (ver APP-CATALOG-SCHEMA).
type PostInstallAction struct {
	ID         string // identidad para tracking (ej. "create_admin")
	Type       string // "exec" (hoy) · http/sql/... a futuro
	WaitFor    string // "healthy" → esperar el healthcheck antes de ejecutar
	Container  string // container donde ejecutar
	Command    string // comando con {{TOKENS}}
	Idempotent bool   // si true, "ya existe" no es error
}

// PostInstallResult · resultado de ejecutar una acción (para tracking/UI).
type PostInstallResult struct {
	ID      string
	OK      bool
	Skipped bool   // idempotente · ya estaba hecho
	Err     string // mensaje de error (ofuscado · sin secretos)
}

// idempotentAlreadyExists detecta si la salida de un comando indica que el
// recurso YA existía (caso idempotente · no es un error real). Conservador ·
// busca señales típicas. PURA · testeable.
func idempotentAlreadyExists(output string) bool {
	low := strings.ToLower(output)
	signals := []string{
		"already exists",
		"already registered",
		"user already",
		"duplicate",
		"ya existe",
	}
	for _, s := range signals {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// execTimeout · tiempo máximo para esperar healthy antes de ejecutar.
// Synapse genera su config la 1ª vez · puede tardar · 120s de margen.
const postInstallHealthTimeout = 120 * time.Second

// runPostInstallAction ejecuta UNA acción postInstall.
//
// Flujo:
//  1. (si WaitFor=="healthy") esperar a que el container esté healthy.
//     · si no tiene healthcheck → ERROR claro (decisión D4 · estricto).
//  2. sustituir {{TOKENS}} en el comando con los valores.
//     · si quedan tokens sin resolver → error (falta un valor).
//  3. docker exec <container> sh -c "<comando>".
//  4. si idempotent y la salida dice "ya existe" → OK (skipped).
//  5. NUNCA loguear el comando con secretos en claro (se ofusca).
//
// @param ctx          contexto
// @param action       la acción a ejecutar
// @param values       valores para los tokens (ADMIN_USER, ADMIN_PASS...)
// @param secretValues valores que son secretos (para ofuscar en logs)
// @param execFn       función que ejecuta (inyectable para tests)
// @returns            resultado de la acción
func runPostInstallAction(
	ctx context.Context,
	action PostInstallAction,
	values map[string]string,
	secretValues []string,
	execFn func(container, command string) (string, bool),
) PostInstallResult {
	res := PostInstallResult{ID: action.ID}

	// Solo soportamos "exec" hoy (el contrato permite más a futuro).
	if action.Type != "" && action.Type != "exec" {
		res.Err = "tipo de acción no soportado todavía: " + action.Type
		return res
	}

	// 1. Esperar healthy si se pide.
	if action.WaitFor == "healthy" {
		ok, noHC := waitForHealthy(ctx, action.Container, postInstallHealthTimeout)
		if noHC {
			res.Err = "la app declara postInstall con waitFor:healthy pero el container '" +
				action.Container + "' no tiene healthcheck en su compose"
			return res
		}
		if !ok {
			res.Err = "timeout esperando a que '" + action.Container + "' esté healthy"
			return res
		}
	}

	// 2. Sustituir tokens.
	cmd := substituteTokens(action.Command, values)
	if unresolved := findUnresolvedTokens(cmd); len(unresolved) > 0 {
		res.Err = "faltan valores para los tokens: " + strings.Join(unresolved, ", ")
		return res
	}

	// 3. Ejecutar (vía la función inyectada · en prod hace docker exec).
	output, ok := execFn(action.Container, cmd)

	// 4. Idempotencia · "ya existe" cuenta como OK.
	if !ok {
		if action.Idempotent && idempotentAlreadyExists(output) {
			res.OK = true
			res.Skipped = true
			return res
		}
		// Error real · ofuscar secretos en el mensaje antes de devolverlo.
		res.Err = ofuscateSecretsInCommand(output, secretValues)
		return res
	}

	res.OK = true
	return res
}

// dockerExecForPostInstall ejecuta el comando dentro del container vía docker
// exec, SIN loguear el comando en claro (runSafe loguearía el secreto si falla).
// Esta es la execFn de producción.
//
// @returns salida combinada (stdout+stderr) y ok
func dockerExecForPostInstall(container, command string) (string, bool) {
	// docker exec <container> sh -c "<command>"
	// Nota: usamos sh -c para soportar comandos compuestos. No usamos runSafe
	// directamente porque loguea el comando (con el secreto) si falla.
	out, ok := runSafeNoLog("docker", "exec", container, "sh", "-c", command)
	return out, ok
}

// runPostInstall orquesta TODAS las acciones de una app, en orden.
// Para en la primera que falle de verdad (no idempotente). Devuelve los
// resultados de cada acción (para tracking).
//
// @param ctx     contexto
// @param actions las acciones del catálogo (postInstall)
// @param values  valores de los campos (ADMIN_USER, ADMIN_PASS...)
// @param secrets nombres de los campos que son secret:true (para ofuscar)
// @param execFn  función de ejecución (inyectable · prod: dockerExecForPostInstall)
// @returns       resultados + error global (nil si todo OK)
func runPostInstall(
	ctx context.Context,
	actions []PostInstallAction,
	values map[string]string,
	secretKeys []string,
	execFn func(container, command string) (string, bool),
) ([]PostInstallResult, error) {
	// Valores secretos concretos (para ofuscar en logs/errores).
	var secretValues []string
	for _, k := range secretKeys {
		if v, ok := values[k]; ok && v != "" {
			secretValues = append(secretValues, v)
		}
	}

	var results []PostInstallResult
	for _, action := range actions {
		r := runPostInstallAction(ctx, action, values, secretValues, execFn)
		results = append(results, r)
		if !r.OK {
			// Loguear el fallo (ofuscado · sin secretos) y parar.
			logMsg("postinstall: acción '%s' falló: %s", r.ID, r.Err)
			return results, &postInstallError{actionID: r.ID, msg: r.Err}
		}
		if r.Skipped {
			logMsg("postinstall: acción '%s' ya estaba hecha (idempotente)", r.ID)
		} else {
			logMsg("postinstall: acción '%s' OK", r.ID)
		}
	}
	return results, nil
}

// postInstallError · error tipado para identificar qué acción falló.
type postInstallError struct {
	actionID string
	msg      string
}

func (e *postInstallError) Error() string {
	return "postinstall '" + e.actionID + "': " + e.msg
}

// ─────────────────────────────────────────────────────────────────────────
// FASE 4 · Parseo del body (frontend) → structs · y orquestación desde install
// ─────────────────────────────────────────────────────────────────────────

// parsePostInstallActions reconstruye las PostInstallAction desde el body del
// install (lo que manda el frontend en body["postInstall"]).
//
// El frontend manda un array de objetos con las claves del contrato. Esta
// función los convierte a structs, tolerando campos ausentes (defensivo).
//
// @param raw  body["postInstall"] (interface{} del JSON)
// @returns    las acciones parseadas (vacío si no hay)
func parsePostInstallActions(raw interface{}) []PostInstallAction {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var out []PostInstallAction
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		action := PostInstallAction{
			ID:        strFromMap(m, "id"),
			Type:      strFromMap(m, "type"),
			WaitFor:   strFromMap(m, "waitFor"),
			Container: strFromMap(m, "container"),
			Command:   strFromMap(m, "command"),
		}
		if v, ok := m["idempotent"].(bool); ok {
			action.Idempotent = v
		}
		out = append(out, action)
	}
	return out
}

// parsePostInstallValues extrae los valores de los campos postInstall que mandó
// el frontend (body["postInstallValues"]), ej. { ADMIN_USER, ADMIN_PASS }.
//
// @param raw  body["postInstallValues"]
// @returns    map de valores (vacío si no hay)
func parsePostInstallValues(raw interface{}) map[string]string {
	out := map[string]string{}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return out
	}
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		} else if v != nil {
			out[k] = fmt.Sprintf("%v", v)
		}
	}
	return out
}

// parseSecretKeys extrae los nombres de campos marcados secret:true que vienen
// en body["postInstallSecretKeys"] (para ofuscarlos en logs).
//
// @param raw  body["postInstallSecretKeys"] (array de strings)
// @returns    lista de claves secretas
func parseSecretKeys(raw interface{}) []string {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var out []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// strFromMap · helper para leer un string de un map[string]interface{}.
func strFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// maybeRunPostInstall lanza el postInstall en GOROUTINE (async) si el body
// trae acciones. No bloquea la respuesta del install (Synapse tarda en estar
// healthy). Los resultados se loguean; el tracking fino por operations es
// trabajo futuro (de momento, logs).
//
// @param body  el body del install
func maybeRunPostInstall(appID string, body map[string]interface{}) {
	actions := parsePostInstallActions(body["postInstall"])
	if len(actions) == 0 {
		return // la app no tiene postInstall · nada que hacer
	}
	values := parsePostInstallValues(body["postInstallValues"])
	secretKeys := parseSecretKeys(body["postInstallSecretKeys"])

	go func() {
		ctx := context.Background()
		logMsg("postinstall: iniciando %d acción(es) para %s", len(actions), appID)
		_, err := runPostInstall(ctx, actions, values, secretKeys, dockerExecForPostInstall)
		if err != nil {
			logMsg("postinstall: %s falló: %v", appID, err)
		} else {
			logMsg("postinstall: %s completado OK", appID)
		}
	}()
}
