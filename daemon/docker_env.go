// docker_env.go — Manejo del archivo .env de los stacks Docker.
//
// Extraído de docker_stacks.go (dockerStackDeploy era un monolito de ~258
// líneas). Aísla la ESCRITURA del .env para:
//   · poder testearla con casos duros (caracteres especiales en values)
//   · tener un único punto donde se escapan los valores (seguridad)
//   · adelgazar dockerStackDeploy
//
// La CONSTRUCCIÓN del env (autoEnv: merge body.env, expandStackEnvRefs,
// resolveRandomPlaceholders, fillUnresolvedPathVars) sigue en dockerStackDeploy
// · aquí solo se ESCRIBE el resultado.

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// writeEnvFile escribe el mapa de variables a un archivo .env.
//
// Formato: una línea KEY=VALUE por variable, terminadas en "\n".
//
// Las claves se escriben ORDENADAS alfabéticamente · esto hace el .env
// reproducible (antes, el range sobre el map daba orden aleatorio · dificultaba
// diffs y debugging). El contenido es equivalente · docker-compose no depende
// del orden de las líneas.
//
// SEGURIDAD (secretos): el .env se escribe con permisos 0600 (solo el
// propietario, root, puede leerlo). El .env puede contener secretos que el
// usuario mete por el modal de instalación (passwords, tokens). Antes era 0644
// (legible por cualquier usuario del sistema) · agujero. El daemon y docker
// corren como root, así que leen el .env sin problema. El env NO se persiste
// en la BD (los stacks no guardan env en el campo Config) ni se loguea (solo
// el path si falla la escritura) · el .env del disco es el único lugar donde
// vive el secreto, por eso se protege con 0600 aquí.
func writeEnvFile(path string, env map[string]interface{}) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []string
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s=%v", k, env[k]))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		return err
	}
	// os.WriteFile NO cambia los permisos de un fichero que YA existe (solo los
	// aplica al crearlo). En reinstalaciones, un .env previo a 0644 mantendría
	// ese modo · el secreto quedaría legible. Chmod explícito lo garantiza.
	return os.Chmod(path, 0600)
}

// reprotectEnvFile vuelve a poner el .env a 0600.
//
// Necesario porque dockerStackDeploy hace `chmod -R 775 stackPath` DESPUÉS de
// escribir el .env (para que la carpeta del stack sea accesible). Ese chmod
// recursivo PISA el .env y lo deja 775 (legible por todos) · anularía la
// protección del secreto. Esta función se llama JUSTO DESPUÉS del chmod -R
// para devolver el .env a 0600. Es idempotente y silenciosa si el fichero no
// existe (no todos los stacks tienen por qué tener .env).
func reprotectEnvFile(path string) {
	if _, err := os.Stat(path); err != nil {
		return // no existe · nada que proteger
	}
	if err := os.Chmod(path, 0600); err != nil {
		logMsg("docker: no se pudo reproteger %s a 0600: %v", path, err)
	}
}
