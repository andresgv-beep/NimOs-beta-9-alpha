// seed_files.go — Sembrado de ficheros de config en el volumen de la app (Beta 8.x)
//
// Algunas apps necesitan un fichero de configuración PRESENTE en su volumen
// ANTES de arrancar, porque su imagen NO autogenera la config desde el env
// (a diferencia de Synapse) y se muere al no encontrarla (Prometheus, Authelia),
// o porque el ajuste no tiene env que lo fije (qBittorrent: la contraseña del
// WebUI vive como hash PBKDF2 en qBittorrent.conf, sin env equivalente).
//
// Por eso el `postInstall: exec` (que corre DESPUÉS de healthy) no sirve para
// estos: la app no llega a healthy / no hay comando que aplicar. La solución es
// sembrar el fichero ANTES del `compose up`.
//
// El catálogo declara:
//
//	"seedFiles": [
//	  { "path": "config/qBittorrent/qBittorrent.conf",
//	    "skipIfExists": true,
//	    "content": "...{{WEBUI_USER}}...{{QBT_PBKDF2:WEBUI_PASS}}..." }
//	]
//
// Placeholders soportados en `content`:
//
//	{{KEY}}             → valor del campo KEY (de los configFields, vía autoEnv)
//	{{QBT_PBKDF2:KEY}}  → hash PBKDF2 de qBittorrent del valor de KEY, en el
//	                      formato exacto del .conf: @ByteArray(salt_b64:hash_b64)
//
// Todo el cálculo (hash, sustitución) es PURO y testeable. Solo writeSeedFiles
// toca disco, y se invoca ANTES de applyAppPermissions (que chowns el volumen al
// UID de la app, cubriendo también lo sembrado).

package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SeedFile es un fichero de config a sembrar en el volumen de la app.
type SeedFile struct {
	Path         string // relativo a CONFIG_PATH (ej. "config/qBittorrent/qBittorrent.conf")
	Content      string // con placeholders {{KEY}} / {{QBT_PBKDF2:KEY}}
	SkipIfExists bool   // si el fichero ya existe, no se sobrescribe (preserva ajustes del user)
}

// Parámetros del PBKDF2 de qBittorrent (verificados Go==Python; pendiente de
// confirmación final contra un qbit real en el Pi).
const (
	qbtPBKDF2Iterations = 100000
	qbtPBKDF2KeyLen      = 64
	qbtSaltLen           = 16
)

// qbtPasswordHashWithSalt genera el hash de contraseña de qBittorrent en el
// formato exacto que guarda en qBittorrent.conf: @ByteArray(<salt_b64>:<hash_b64>),
// con PBKDF2-HMAC-SHA512. El salt se pasa como argumento para poder testear de
// forma determinista; producción usa qbtPasswordHash (salt aleatorio).
func qbtPasswordHashWithSalt(password string, salt []byte) (string, error) {
	dk, err := pbkdf2.Key(sha512.New, password, salt, qbtPBKDF2Iterations, qbtPBKDF2KeyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("@ByteArray(%s:%s)",
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(dk)), nil
}

// qbtPasswordHash genera el hash con un salt aleatorio de 16 bytes (producción).
func qbtPasswordHash(password string) (string, error) {
	salt := make([]byte, qbtSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return qbtPasswordHashWithSalt(password, salt)
}

var seedPlaceholderRe = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// substituteSeedContent sustituye los placeholders de `content` usando `values`.
// Soporta {{KEY}} (valor directo) y {{GEN:KEY}} (generador, p.ej. QBT_PBKDF2).
// Devuelve el contenido sustituido y la lista de errores de generadores (un
// placeholder sin valor directo se sustituye por cadena vacía sin error).
func substituteSeedContent(content string, values map[string]string) (string, []string) {
	var errs []string
	out := seedPlaceholderRe.ReplaceAllStringFunc(content, func(m string) string {
		token := strings.TrimSpace(m[2 : len(m)-2]) // quita {{ }}
		if i := strings.IndexByte(token, ':'); i >= 0 {
			gen := strings.TrimSpace(token[:i])
			key := strings.TrimSpace(token[i+1:])
			switch gen {
			case "QBT_PBKDF2":
				h, err := qbtPasswordHash(values[key])
				if err != nil {
					errs = append(errs, fmt.Sprintf("QBT_PBKDF2(%s): %v", key, err))
					return ""
				}
				return h
			default:
				errs = append(errs, "generador desconocido: "+gen)
				return ""
			}
		}
		return values[token]
	})
	return out, errs
}

// writeSeedFiles escribe los seedFiles bajo configPath (= CONFIG_PATH de la app),
// sustituyendo placeholders con `values`. Crea directorios intermedios, respeta
// skipIfExists y bloquea path-traversal fuera del volumen. NO hace chown: se
// invoca ANTES de applyAppPermissions, que chowns el volumen al UID de la app.
func writeSeedFiles(configPath string, seeds []SeedFile, values map[string]string) {
	base := filepath.Clean(configPath) + string(os.PathSeparator)
	for _, s := range seeds {
		if s.Path == "" || s.Content == "" {
			continue
		}
		dst := filepath.Join(configPath, s.Path)
		if !strings.HasPrefix(filepath.Clean(dst)+string(os.PathSeparator), base) {
			logMsg("seed: path fuera del volumen, ignorado: %q", s.Path)
			continue
		}
		if s.SkipIfExists {
			if _, err := os.Stat(dst); err == nil {
				logMsg("seed: %q ya existe → no se pisa (skipIfExists)", s.Path)
				continue
			}
		}
		content, errs := substituteSeedContent(s.Content, values)
		for _, e := range errs {
			logMsg("seed: %s: %s", s.Path, e)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
			logMsg("seed: no se pudo crear dir para %q: %v", s.Path, err)
			continue
		}
		if err := os.WriteFile(dst, []byte(content), 0640); err != nil {
			logMsg("seed: no se pudo escribir %q: %v", s.Path, err)
			continue
		}
		logMsg("seed: sembrado %q (%d bytes)", s.Path, len(content))
	}
}

// parseSeedFiles reconstruye []SeedFile desde el body del install (body["seedFiles"]).
func parseSeedFiles(raw interface{}) []SeedFile {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var out []SeedFile
	for _, it := range arr {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		s := SeedFile{}
		if v, ok := m["path"].(string); ok {
			s.Path = v
		}
		if v, ok := m["content"].(string); ok {
			s.Content = v
		}
		if v, ok := m["skipIfExists"].(bool); ok {
			s.SkipIfExists = v
		}
		if s.Path != "" && s.Content != "" {
			out = append(out, s)
		}
	}
	return out
}
