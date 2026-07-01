// docker_async.go — Helpers para handlers Docker con soporte sync/async (Beta 8.1.x).
//
// APP-014 · dockerInstall async vía operationsRepo
// APP-053 · dockerPull async vía operationsRepo
//
// Patrón:
//
//   1. Handler valida auth + body (síncrono, rápido).
//   2. Si query string contiene async=true:
//      - operationsRepo.Create(...) crea row con status='pending'
//      - go func() { worker(...) } ejecuta el trabajo en background
//      - response 202 Accepted con {operationId, pollUrl}
//   3. Si no:
//      - worker(...) se ejecuta inline
//      - response 200 OK con resultado (legacy)
//
// El worker es una función pura `(ctx, params...) (result, error)` que NO
// conoce HTTP. Solo conoce trabajo real. Se reutiliza idéntica para sync y
// async; el wrapper handler decide cómo se responde.
//
// Para preservar la semántica HTTP de los errores específicos (e.g. 409 de
// APP-063 en dockerInstall), los workers devuelven `*httpStatusError` cuando
// el código importa. El wrapper sync lo mapea a status HTTP; el wrapper
// async lo aplana a string para el campo error de la operation.

package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// httpStatusError · error tipado con código HTTP específico.
// Cuando un worker devuelve este tipo, el handler sync usa Code en lugar de
// 500 por defecto. El handler async ignora Code y guarda solo Msg.
//
// NO se exporta · solo se usa internamente en handlers que se refactorizan
// a worker. No es contrato HTTP del paquete.
type httpStatusError struct {
	Code int
	Msg  string
}

func (e *httpStatusError) Error() string { return e.Msg }

// asHTTPError envuelve un error con un código HTTP específico.
// Helper para mantener la semántica del jsonError original al refactorizar.
func asHTTPError(code int, format string, args ...interface{}) error {
	return &httpStatusError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// isAsyncRequested devuelve true si la query string del request contiene
// async=true (o "1", o "yes"). Permite alguna flexibilidad sin ser laxo.
func isAsyncRequested(r *http.Request) bool {
	v := r.URL.Query().Get("async")
	return v == "true" || v == "1" || v == "yes"
}

// writeWorkerError envía la respuesta HTTP correcta para un error del worker.
// Si es httpStatusError, usa su código; si no, 500 genérico.
func writeWorkerError(w http.ResponseWriter, err error) {
	if hse, ok := err.(*httpStatusError); ok {
		jsonError(w, hse.Code, hse.Msg)
		return
	}
	jsonError(w, 500, err.Error())
}

// writeAsyncAccepted envía un 202 con el payload estándar de "operation
// pendiente". Usado por handlers que aceptan ?async=true.
func writeAsyncAccepted(w http.ResponseWriter, op *DBOperation) {
	jsonResponse(w, http.StatusAccepted, map[string]interface{}{
		"operationId": op.ID,
		"pollUrl":     "/api/operations/" + op.ID,
		"status":      op.Status,
		"type":        op.Type,
	})
}

// runWorkerAsync envuelve la ejecución de un worker en una goroutine que
// reporta el resultado a operationsRepo. Centraliza el patrón:
//
//  1. MarkRunning
//  2. invocar worker
//  3. MarkSucceeded(resultJSON) ó MarkFailed(error)
//
// El worker recibe ctx (context.Background, dado que el request HTTP ya
// terminó) y devuelve (result map, error). Si result no es nil al success,
// se serializa a JSON para el campo result_json de la operation.
//
// runWorkerAsync NO bloquea · lanza la goroutine y retorna.
func runWorkerAsync(opID string, work func(ctx context.Context) (map[string]interface{}, error)) {
	go func() {
		ctx := context.Background()

		if err := operationsRepo.MarkRunning(ctx, opID); err != nil {
			logMsg("docker_async: MarkRunning failed for %s: %v", opID, err)
			// Continuamos: el trabajo puede que aún tenga sentido aunque
			// el state machine se haya descarriado. Marcamos como failed
			// abajo si peta. Si MarkRunning falló por race con cancel,
			// el siguiente Mark también fallará y al menos quedará trazado.
		}

		result, err := work(ctx)
		if err != nil {
			if markErr := operationsRepo.MarkFailed(ctx, opID, err.Error(), ""); markErr != nil {
				logMsg("docker_async: MarkFailed failed for %s: %v (orig err: %v)", opID, markErr, err)
			}
			return
		}

		resultJSON := ""
		if result != nil {
			if data, jerr := json.Marshal(result); jerr == nil {
				resultJSON = string(data)
			}
		}
		if markErr := operationsRepo.MarkSucceeded(ctx, opID, resultJSON); markErr != nil {
			logMsg("docker_async: MarkSucceeded failed for %s: %v", opID, markErr)
		}
	}()
}

// updateOpProgressSafe · helper para workers que quieren reportar progreso
// solo si están corriendo bajo una operation (modo async).
//
// Si opID es vacío o el repo no está disponible, es no-op silencioso.
// Esto permite escribir workers sin if-else: pasan el opID y el helper
// decide. En sync (opID=""), no hace nada.
func updateOpProgressSafe(ctx context.Context, opID string, progress int, message string) {
	if opID == "" || operationsRepo == nil {
		return
	}
	// Errores se loguean pero no propagan · el progreso es metadata, su
	// fallo no debe abortar el trabajo real.
	if err := operationsRepo.UpdateProgress(ctx, opID, progress, message); err != nil {
		logMsg("docker_async: UpdateProgress failed for %s: %v", opID, err)
	}
}

// getStackHostIP devuelve la primera IP IPv4 no-loopback del host, usada
// como valor por defecto para HOST_IP en los .env de los stacks Docker.
//
// Apps del catálogo NimOS (p.ej. Jellyfin con JELLYFIN_PublishedServerUrl)
// necesitan saber la IP del NAS para generar URLs absolutas dentro del LAN.
// El backend es la fuente canónica · el frontend no debería adivinar.
//
// Fallback: si no se encuentra ninguna IP IPv4 válida, devuelve "127.0.0.1"
// (mejor que cadena vacía · al menos el container arranca).
func getStackHostIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return "127.0.0.1"
}

// getStackTimezone devuelve el timezone del host (e.g. "Europe/Madrid") usado
// como valor por defecto para TZ en los .env de los stacks Docker.
//
// Lee /etc/timezone, fallback a la zona de time.Local (que en sistemas
// systemd suele ser correcto), último recurso "UTC".
//
// Apps Docker con cron interno, logs con timestamp, o jobs schedulados
// (e.g. Sonarr/Radarr buscando releases) necesitan TZ para coherencia.
func getStackTimezone() string {
	// /etc/timezone es lo más fiable en Debian/Ubuntu/Raspbian.
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		tz := strings.TrimSpace(string(data))
		if tz != "" {
			return tz
		}
	}
	// Fallback al runtime de Go (refleja TZ=... env var si existe).
	if loc := time.Local; loc != nil {
		s := loc.String()
		if s != "" && s != "Local" {
			return s
		}
	}
	return "UTC"
}

// expandStackEnvRefs · resuelve referencias ${KEY} dentro de los values del
// env recursivamente, hasta `maxPasses` pasadas. Necesario porque docker-compose
// NO interpola variables entre sí dentro del archivo .env (solo las usa para
// interpolar el YAML del compose).
//
// Ejemplo:
//
//	Input:  { CONFIG_PATH: "/a/b/c", PROJECTS_PATH: "${CONFIG_PATH}/projects" }
//	Output: { CONFIG_PATH: "/a/b/c", PROJECTS_PATH: "/a/b/c/projects" }
//
// Solo expande strings · valores numéricos u objetos quedan tal cual.
// Si una referencia apunta a una clave que no existe, queda literal (el
// usuario verá el ${UNKNOWN} en el container, error visible).
//
// maxPasses defiende contra referencias circulares · raro pero posible si el
// catálogo define mal A=${B} y B=${A}. Cuatro pasadas es más que suficiente
// para cualquier caso real (1-2 niveles de anidamiento como máximo).
func expandStackEnvRefs(env map[string]interface{}, maxPasses int) map[string]interface{} {
	if maxPasses <= 0 {
		return env
	}
	// Patrón ${IDENT} con IDENT = letras/dígitos/underscore, empezando por letra o _.
	refRe := regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

	for pass := 0; pass < maxPasses; pass++ {
		changed := false
		for k, v := range env {
			s, ok := v.(string)
			if !ok {
				continue
			}
			expanded := refRe.ReplaceAllStringFunc(s, func(match string) string {
				key := refRe.FindStringSubmatch(match)[1]
				if val, exists := env[key]; exists {
					if sv, ok := val.(string); ok {
						return sv
					}
					return fmt.Sprintf("%v", val)
				}
				return match // ref no resuelta · queda literal
			})
			if expanded != s {
				env[k] = expanded
				changed = true
			}
		}
		if !changed {
			break // estable, no hace falta más pasadas
		}
	}
	return env
}

// resolveRandomPlaceholders sustituye values "{RANDOM}" por cadenas aleatorias
// de 24 caracteres alfanuméricos. Idempotente por persistencia: si el archivo
// .env del stack ya existe, lee los valores previos y los reusa.
//
// Por qué la idempotencia importa:
//
//	Postgres y otras bases de datos almacenan el hash de la password en su
//	data dir al inicializarse. Si en una reinstalación generamos una pass
//	aleatoria distinta, el container fallaría al autenticarse contra el
//	data dir existente. Reusar el valor previo del .env preserva la data.
//
// Comportamiento:
//
//	Primera instalación        · .env no existe   · genera 24 chars random
//	Reinstalación, .env existe · valor previo "{RANDOM}" literal · MANTIENE literal
//	Reinstalación, .env existe · valor previo "abc123..." random · MANTIENE "abc123..."
//
// El segundo caso (mantener "{RANDOM}" literal) es deliberado: protege a
// instalaciones existentes que ya estaban funcionando con el placeholder
// sin resolver. Si quisieran obtener pass aleatoria real, harían una
// desinstalación completa + nueva instalación.
//
// Caracteres usados para el random · [A-Za-z0-9] · 62 valores · 24 chars
// dan 24*log2(62) ≈ 143 bits de entropía. Más que suficiente para uso de
// credenciales internas de stacks Docker que no se exponen al exterior.
func resolveRandomPlaceholders(env map[string]interface{}, existingEnvPath string) map[string]interface{} {
	// Leer valores previos si .env existía
	previousValues := readEnvFile(existingEnvPath)

	for k, v := range env {
		s, ok := v.(string)
		if !ok || s != "{RANDOM}" {
			continue
		}
		// Si en el .env previo había un valor para esta key, reusarlo
		// (sea literal "{RANDOM}" o un random ya generado · ambos respetan
		// la inicialización original del container).
		if prev, exists := previousValues[k]; exists && prev != "" {
			env[k] = prev
			continue
		}
		// Primera vez · generar random nuevo
		env[k] = generateRandomString(24)
	}
	return env
}

// readEnvFile parsea un archivo .env simple (líneas KEY=VALUE) y devuelve un
// map. Líneas vacías y las que empiezan con # se ignoran. Si el archivo no
// existe o no se puede leer, devuelve map vacío (caso primera instalación).
//
// No soporta escapes ni quotes complejos · suficiente para nuestros .env
// generados por dockerStackDeploy que son siempre KEY=value literal.
func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		out[key] = val
	}
	return out
}

// generateRandomString devuelve una cadena alfanumérica de length caracteres
// usando crypto/rand para entropía criptográficamente segura. Caracteres
// del alfabeto [A-Za-z0-9] · 62 valores.
func generateRandomString(length int) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	randBytes := make([]byte, length)
	if _, err := cryptorand.Read(randBytes); err != nil {
		// Fallback extremo · timestamp + sequence. Casi imposible que falle
		// crypto/rand en un sistema Linux moderno, pero defensive.
		for i := range b {
			b[i] = charset[(int(time.Now().UnixNano())+i)%len(charset)]
		}
		return string(b)
	}
	for i := range b {
		b[i] = charset[int(randBytes[i])%len(charset)]
	}
	return string(b)
}
