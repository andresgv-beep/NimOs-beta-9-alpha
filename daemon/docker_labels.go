// docker_labels.go — Sistema de labels NimOS para containers Docker.
//
// Beta 8.2 · Fase 2 (27/05/2026).
//
// Origen: bug Nextcloud (26/05/2026). Identificar containers gestionados por
// NimOS dependía de matching por nombre vs docker_apps (circular y frágil).
// Si la BD se desincroniza (ej. bug Nextcloud), un container vivo queda
// invisible para reconcile.
//
// Solución: cada container instalado por NimOS lleva labels `com.nimos.*`.
// Eso permite:
//   - docker ps --filter "label=com.nimos.managed=true" → SOLO containers NimOS
//   - Reconcile fiable (Fase 3) · comparación bidireccional con docker_apps
//   - Auditoría · quién instaló qué y cuándo
//   - Robustez · BD puede estar incompleta, los labels en Docker no mienten
//
// ─────────────────────────────────────────────────────────────────────────
// SCHEMA
// ─────────────────────────────────────────────────────────────────────────
//
//   com.nimos.schema_version   · "beta_8.2" (ver SchemaVersion abajo)
//   com.nimos.managed          · "true" · marcador universal
//   com.nimos.app_id           · "nextcloud", "jellyfin", ...
//   com.nimos.app_version      · "29.0.7" del catálogo (puede estar vacío)
//   com.nimos.installed_by     · username
//   com.nimos.installed_at     · ISO 8601 UTC
//   com.nimos.stack            · "true" si es parte de stack, "false" si single
//
// ─────────────────────────────────────────────────────────────────────────
// APLICACIÓN
// ─────────────────────────────────────────────────────────────────────────
//
// **CRÍTICO**: los labels de un container Docker son INMUTABLES tras el
// `docker create`. NO se pueden añadir post-creación con `docker container
// update` (esa API NO acepta --label-add · solo cambia recursos como CPU/RAM).
// Solo `docker service update` (Swarm) acepta --label-add, y NimOS no usa
// Swarm.
//
// Por tanto, los labels deben aplicarse AL CREAR el container:
//
//   Single containers (docker_containers.go) · args --label en `docker run`
//   Stacks (docker_stacks.go) · injection en el YAML antes de `compose up -d`
//
// La función injectNimOSLabelsIntoCompose modifica el YAML del compose
// recibido del catálogo, añadiendo el bloque `labels:` a cada servicio,
// y devuelve el YAML modificado para escribir a disco.
//
// ─────────────────────────────────────────────────────────────────────────
// VERSIONADO
// ─────────────────────────────────────────────────────────────────────────
//
// schema_version permite evolucionar el set de labels sin romper consumers.
// Cuando se añadan/quiten labels en un futuro:
//   1. Incrementar SchemaVersion (ej. "v1.0")
//   2. El reconciler reconoce ambas versiones durante migración
//   3. Tras N días, drop soporte de version antigua

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ─────────────────────────────────────────────────────────────────────────
// Constantes
// ─────────────────────────────────────────────────────────────────────────

// SchemaVersion · valor del label com.nimos.schema_version.
// Cuando se cambie el set de labels, actualizar esto y documentar la
// migración en CHANGELOG.md.
const SchemaVersion = "beta_8.2"

// Nombres canónicos de los labels NimOS.
// Usar SIEMPRE estas constantes, nunca strings literales.
const (
	LabelSchemaVersion = "com.nimos.schema_version"
	LabelManaged       = "com.nimos.managed"
	LabelAppID         = "com.nimos.app_id"
	LabelAppVersion    = "com.nimos.app_version"
	LabelInstalledBy   = "com.nimos.installed_by"
	LabelInstalledAt   = "com.nimos.installed_at"
	LabelStack         = "com.nimos.stack"
)

// parseAppIDLabel · extrae el valor del label com.nimos.app_id de la cadena que
// devuelve `docker ps --format {{.Labels}}` (formato "k=v,k=v,..."). Devuelve ""
// si no está presente. PURA · base del matching robusto container↔app que
// reemplaza la heurística por nombre (item 6 backlog).
func parseAppIDLabel(labelsRaw string) string {
	for _, kv := range strings.Split(labelsRaw, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(kv), LabelAppID+"="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// dockerCmdTimeout · timeout para comandos docker locales (ps, inspect).
// Estos solo consultan el daemon Docker local, deberían ser instantáneos.
const dockerCmdTimeout = 30 * time.Second

// ─────────────────────────────────────────────────────────────────────────
// Modelos
// ─────────────────────────────────────────────────────────────────────────

// NimOSLabels · set completo de labels NimOS para un container.
// Se construye en el handler de install y se aplica al CREAR el container
// (no es posible aplicarlos después en Docker).
type NimOSLabels struct {
	AppID       string
	AppVersion  string // puede estar vacío
	InstalledBy string
	InstalledAt string // ISO 8601 UTC
	IsStack     bool   // true si parte de un stack, false si single container
}

// NewNimOSLabels construye un set de labels para una nueva instalación.
// installedAt se rellena automáticamente con time.Now().UTC().
func NewNimOSLabels(appID, appVersion, installedBy string, isStack bool) NimOSLabels {
	return NimOSLabels{
		AppID:       appID,
		AppVersion:  appVersion,
		InstalledBy: installedBy,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		IsStack:     isStack,
	}
}

// ToDockerLabelArgs devuelve los argumentos `--label key=value` para usar
// directamente en `docker run`. Usado por docker_containers.go.
//
// Ejemplo:
//   labels := NewNimOSLabels("jellyfin", "10.11", "andres", false)
//   args := append([]string{"run", "-d", "--name", "jellyfin"},
//                  labels.ToDockerLabelArgs()...)
//   args = append(args, "jellyfin:latest")
//   exec.Command("docker", args...)
func (l NimOSLabels) ToDockerLabelArgs() []string {
	stackVal := "false"
	if l.IsStack {
		stackVal = "true"
	}
	return []string{
		"--label", LabelSchemaVersion + "=" + SchemaVersion,
		"--label", LabelManaged + "=true",
		"--label", LabelAppID + "=" + l.AppID,
		"--label", LabelAppVersion + "=" + l.AppVersion,
		"--label", LabelInstalledBy + "=" + l.InstalledBy,
		"--label", LabelInstalledAt + "=" + l.InstalledAt,
		"--label", LabelStack + "=" + stackVal,
	}
}

// ToMap devuelve el set de labels como map[string]string. Usado por
// injectNimOSLabelsIntoCompose para añadirlos al YAML del stack.
func (l NimOSLabels) ToMap() map[string]string {
	stackVal := "false"
	if l.IsStack {
		stackVal = "true"
	}
	return map[string]string{
		LabelSchemaVersion: SchemaVersion,
		LabelManaged:       "true",
		LabelAppID:         l.AppID,
		LabelAppVersion:    l.AppVersion,
		LabelInstalledBy:   l.InstalledBy,
		LabelInstalledAt:   l.InstalledAt,
		LabelStack:         stackVal,
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Inyección en YAML de compose
// ─────────────────────────────────────────────────────────────────────────

// injectNimOSLabelsIntoCompose parsea el YAML de un docker-compose recibido
// del catálogo y añade los labels com.nimos.* a cada servicio bajo la clave
// `services:`. Devuelve el YAML modificado.
//
// Si el compose ya tiene labels en algún servicio, los labels NimOS se
// MERGEAN (no se sobreescriben los labels del usuario; los NimOS se añaden).
// Si hay colisión en un nombre exacto de label (ej. el catálogo ya define
// "com.nimos.app_id"), el NimOS gana porque es metadato de gestión.
//
// La función NO modifica:
//   - El resto del YAML (volumes, networks, secrets, ...)
//   - El orden de los servicios
//   - Otros campos de los servicios (image, ports, environment, ...)
//
// Si el YAML no parsea o no tiene clave `services:`, devuelve error.
func injectNimOSLabelsIntoCompose(composeYAML string, labels NimOSLabels) (string, error) {
	if strings.TrimSpace(composeYAML) == "" {
		return "", fmt.Errorf("injectNimOSLabelsIntoCompose: compose vacío")
	}

	// Parseamos como Node para preservar estructura, comentarios y orden.
	// Esto es más robusto que map[string]interface{} para escritura.
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(composeYAML), &root); err != nil {
		return "", fmt.Errorf("yaml unmarshal: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", fmt.Errorf("yaml root no es DocumentNode válido")
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return "", fmt.Errorf("yaml root document no es MappingNode (compose mal formado)")
	}

	// Buscar la clave 'services:' en el mapping raíz.
	servicesNode := findMappingValue(doc, "services")
	if servicesNode == nil {
		return "", fmt.Errorf("compose sin clave 'services:' (no es un compose válido)")
	}
	if servicesNode.Kind != yaml.MappingNode {
		return "", fmt.Errorf("'services:' no es un mapping (compose mal formado)")
	}

	// Recorrer cada servicio bajo services:
	labelMap := labels.ToMap()
	for i := 0; i < len(servicesNode.Content); i += 2 {
		serviceKey := servicesNode.Content[i]
		serviceVal := servicesNode.Content[i+1]

		if serviceVal.Kind != yaml.MappingNode {
			// Servicio mal formado (ej. solo nombre sin definición) · saltamos
			logMsg("docker_labels: servicio %q no es MappingNode, omitido", serviceKey.Value)
			continue
		}

		// Aplicar labels a este servicio (merge si ya existen, add si no).
		mergeLabelsIntoServiceNode(serviceVal, labelMap)
	}

	// Re-serializar a YAML.
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return "", fmt.Errorf("yaml encode: %w", err)
	}
	enc.Close()

	return buf.String(), nil
}

// findMappingValue busca una clave en un MappingNode y devuelve su nodo valor.
// Devuelve nil si no se encuentra. Las claves en YAML mapping son pares
// (key, value) consecutivos en Content[].
func findMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// mergeLabelsIntoServiceNode añade los labels NimOS al servicio.
// Si el servicio ya tiene una clave `labels:` la mergea (no la reemplaza).
// Si no la tiene, crea la clave entera.
//
// Acepta tanto formato map (labels: { key: value }) como secuencia
// (labels: [ "key=value" ]). En ambos casos preserva el formato original
// del usuario · si era map sigue siendo map, si era secuencia sigue siendo
// secuencia.
func mergeLabelsIntoServiceNode(service *yaml.Node, labelMap map[string]string) {
	existing := findMappingValue(service, "labels")

	if existing == nil {
		// No hay labels · creamos el bloque entero en formato map
		labelsNode := buildLabelsMappingNode(labelMap)
		service.Content = append(service.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "labels", Tag: "!!str"},
			labelsNode,
		)
		return
	}

	// Existen labels · mergeamos preservando el formato original
	switch existing.Kind {
	case yaml.MappingNode:
		// Formato map · añadimos los nuestros (sobrescribimos si colisión)
		for k, v := range labelMap {
			setMappingValue(existing, k, v)
		}
	case yaml.SequenceNode:
		// Formato lista ("key=value") · añadimos al final, deduplicando
		for k, v := range labelMap {
			removeSequenceItemWithPrefix(existing, k+"=")
			existing.Content = append(existing.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: k + "=" + v,
			})
		}
	default:
		// Tipo inesperado · loggeamos y omitimos (mejor no romper el deploy)
		logMsg("docker_labels: servicio con labels de tipo inesperado (kind=%d), omitido", existing.Kind)
	}
}

// buildLabelsMappingNode construye un MappingNode con los labels dados.
// Salida YAML típica:
//
//   labels:
//     com.nimos.app_id: "nextcloud"
//     com.nimos.managed: "true"
//     ...
//
// Orden alfabético para output reproducible (facilita tests deterministas).
func buildLabelsMappingNode(labels map[string]string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	// Sort claves manualmente (evita import "sort" extra)
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	for _, k := range keys {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: labels[k], Style: yaml.DoubleQuotedStyle},
		)
	}
	return node
}

// setMappingValue añade o sobrescribe un par clave/valor en un MappingNode.
func setMappingValue(mapping *yaml.Node, key, value string) {
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Kind = yaml.ScalarNode
			mapping.Content[i+1].Tag = "!!str"
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Style = yaml.DoubleQuotedStyle
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle},
	)
}

// removeSequenceItemWithPrefix elimina del SequenceNode los items cuyo Value
// empieza con el prefix dado. Usado para deduplicar al mergear labels en
// formato lista ("key=value").
func removeSequenceItemWithPrefix(seq *yaml.Node, prefix string) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	filtered := seq.Content[:0]
	for _, item := range seq.Content {
		if !strings.HasPrefix(item.Value, prefix) {
			filtered = append(filtered, item)
		}
	}
	seq.Content = filtered
}

// ─────────────────────────────────────────────────────────────────────────
// Consulta de containers gestionados
// ─────────────────────────────────────────────────────────────────────────

// NimOSContainer · representación mínima de un container gestionado.
// Resultado de listNimOSContainers · usado por reconciler (Fase 3).
type NimOSContainer struct {
	ID        string // ID del container Docker
	Name      string // Nombre (sin slash prefix)
	AppID     string // Valor del label com.nimos.app_id
	IsStack   bool   // Valor del label com.nimos.stack
	SchemaVer string // Valor del label com.nimos.schema_version
}

// listNimOSContainers devuelve los containers que tienen label
// com.nimos.managed=true, vivos o parados. Es la fuente de verdad para
// el reconciler Docker (Fase 3).
//
// El reconciler compara esta lista con `docker_apps` y detecta:
//   - Containers vivos sin row (huérfanos a importar)
//   - Rows sin container correspondiente (apps perdidas)
func listNimOSContainers(ctx context.Context) ([]NimOSContainer, error) {
	cctx, cancel := context.WithTimeout(ctx, dockerCmdTimeout)
	defer cancel()

	// docker ps -a · incluir parados también (el reconciler decide qué hacer)
	// --filter label=com.nimos.managed=true · solo containers NimOS
	// --format · campos separados por tabs para parsing trivial
	cmd := exec.CommandContext(cctx, "docker", "ps", "-a",
		"--filter", "label="+LabelManaged+"=true",
		"--format", "{{.ID}}\t{{.Names}}\t{{.Label \""+LabelAppID+"\"}}\t{{.Label \""+LabelStack+"\"}}\t{{.Label \""+LabelSchemaVersion+"\"}}")

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps --filter label: %w", err)
	}

	var result []NimOSContainer
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			logMsg("docker_labels: malformed ps output line: %q (skipping)", line)
			continue
		}
		result = append(result, NimOSContainer{
			ID:        fields[0],
			Name:      fields[1],
			AppID:     fields[2],
			IsStack:   fields[3] == "true",
			SchemaVer: fields[4],
		})
	}
	return result, nil
}

// getNimOSContainerLabels devuelve TODOS los labels de un container concreto
// vía `docker inspect`. Usado por el reconciler (Fase 3) para reconstruir la
// row de docker_apps de un huérfano con la máxima fidelidad (installed_by,
// installed_at, etc).
//
// Devuelve un map vacío (no error) si el container no tiene labels.
func getNimOSContainerLabels(ctx context.Context, containerID string) (map[string]string, error) {
	cctx, cancel := context.WithTimeout(ctx, dockerCmdTimeout)
	defer cancel()

	// --format con range genera "key=value" por línea · parsing trivial,
	// sin depender de jq ni de parsear JSON.
	cmd := exec.CommandContext(cctx, "docker", "inspect", containerID,
		"--format", `{{range $k, $v := .Config.Labels}}{{$k}}={{$v}}{{"\n"}}{{end}}`)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker inspect %s: %w", containerID, err)
	}

	labels := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Split en el PRIMER "=" · los valores pueden contener "=".
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := line[:idx]
		val := line[idx+1:]
		labels[key] = val
	}
	return labels, nil
}

// ─────────────────────────────────────────────────────────────────────────
// Borrado de imágenes de una app (Beta 8.2 · uninstall WIPE)
// ─────────────────────────────────────────────────────────────────────────
//
// Problema: al desinstalar una app, NimOS borraba containers/volúmenes/datos
// pero NUNCA las imágenes Docker. Resultado: se acumulan GB de imágenes
// huérfanas (en producción real, ~7.7GB tras varias reinstalaciones).
//
// Solución: en modo WIPE ("Desinstalar y borrar todos los datos"), tras quitar
// los containers, borrar las imágenes que usaba la app.
//
// SEGURIDAD ENTRE APPS (requisito de Andrés): usamos `docker rmi` SIN -f.
// Docker rechaza borrar una imagen si CUALQUIER otro container (de otra app)
// la está usando. Por tanto es IMPOSIBLE romper otra app: si dos apps comparten
// redis:alpine y borras una, Docker se niega a borrar la imagen porque la otra
// la usa, y la dejamos intacta. Solo se borran imágenes que quedan realmente
// sin usar tras quitar esta app.
//
// El modo SOFT ("Desinstalar", recomendado) NO llama a esto · conserva las
// imágenes para que una reinstalación sea instantánea (no re-descarga GB).

// getStackImages devuelve las imágenes usadas por los containers de un stack.
// Debe llamarse ANTES de `compose down` (necesita los containers vivos).
//
// APPROACH (corregido 28/05/2026): NO usa `docker compose images` porque ese
// comando EVALÚA el YAML del compose · si el compose tiene una variable sin
// definir (ej. ${MUSIC_PATH} en navidrome sin valor en .env), el comando
// FALLA ENTERO y no devuelve ninguna imagen. Bug real visto en producción.
//
// En su lugar, busca los containers del stack por su label com.nimos.app_id
// (inyectado en Fase 2) y obtiene la imagen de cada uno con `docker inspect`.
// Esto trabaja sobre containers YA CREADOS · inmune a variables del YAML.
//
// Devuelve los nombres de imagen (ej. "nextcloud:latest"), deduplicados.
func getStackImages(ctx context.Context, appID string) []string {
	cctx, cancel := context.WithTimeout(ctx, dockerCmdTimeout)
	defer cancel()

	if appID == "" {
		logMsg("docker_labels: getStackImages llamado con appID vacío")
		return nil
	}

	// 1. Listar IDs de containers del stack por label com.nimos.app_id.
	//    Trabaja sobre containers reales · no evalúa el compose YAML.
	psCmd := exec.CommandContext(cctx, "docker", "ps", "-a",
		"--filter", "label="+LabelAppID+"="+appID,
		"--format", "{{.ID}}")
	out, err := psCmd.Output()
	if err != nil {
		logMsg("docker_labels: getStackImages ps falló para %s: %v (no se borrarán imágenes)", appID, err)
		return nil
	}

	containerIDs := strings.Fields(strings.TrimSpace(string(out)))
	if len(containerIDs) == 0 {
		// Fallback · containers ya parados sin label (instalación pre-Fase 2).
		// No es error · simplemente no hay imágenes que rastrear por label.
		logMsg("docker_labels: getStackImages · sin containers con label %s=%s", LabelAppID, appID)
		return nil
	}

	// 2. Por cada container, obtener su imagen con inspect.
	var images []string
	for _, id := range containerIDs {
		img := getContainerImage(ctx, id)
		if img != "" {
			images = append(images, img)
		}
	}

	return dedupeNonEmpty(images)
}

// getContainerImage devuelve el nombre de imagen de un single container.
// Debe llamarse ANTES de `docker rm`.
func getContainerImage(ctx context.Context, containerName string) string {
	cctx, cancel := context.WithTimeout(ctx, dockerCmdTimeout)
	defer cancel()

	// .Config.Image da el nombre tal como se especificó (ej. "jellyfin/jellyfin:latest")
	cmd := exec.CommandContext(cctx, "docker", "inspect", containerName,
		"--format", "{{.Config.Image}}")
	out, err := cmd.Output()
	if err != nil {
		logMsg("docker_labels: getContainerImage falló para %s: %v", containerName, err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

// removeAppImages borra las imágenes dadas con `docker rmi` SIN -f.
//
// SEGURO ENTRE APPS: si una imagen está en uso por otro container (otra app),
// `docker rmi` falla para ESA imagen y la dejamos intacta. Las demás se borran.
// Nunca usamos -f · eso rompería apps que comparten imagen.
//
// Devuelve cuántas imágenes se borraron con éxito.
func removeAppImages(ctx context.Context, images []string) int {
	removed := 0
	for _, img := range images {
		img = strings.TrimSpace(img)
		if img == "" || strings.Contains(img, "<none>") {
			continue // imagen sin tag claro o vacía · saltamos por seguridad
		}
		// docker rmi SIN -f · Docker protege imágenes en uso por otras apps.
		out, ok := runSafe("docker", "rmi", img)
		if !ok {
			// Falla esperado si la imagen está compartida con otra app viva.
			// No es error · es la protección funcionando.
			logMsg("docker_labels: imagen %s no borrada (probablemente en uso por otra app): %s",
				img, out)
			continue
		}
		removed++
		logMsg("docker_labels: imagen %s borrada (wipe uninstall)", img)
	}
	return removed
}

// dedupeNonEmpty elimina duplicados y strings vacíos de un slice.
func dedupeNonEmpty(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" || seen[it] {
			continue
		}
		seen[it] = true
		result = append(result, it)
	}
	return result
}

// getStackContainerNames devuelve los NOMBRES de los containers que pertenecen
// a una app, buscándolos por el label com.nimos.app_id.
//
// BUG FIX (15/06/2026): antes NimOS asumía que el container se llamaba como el
// app_id (ej. app "matrix" → container "matrix"). Pero las apps multi-servicio
// usan container_name propios (matrix → matrix_synapse + matrix_element ·
// immich → immich_server + immich_postgres + ...). Buscar por nombre = app_id
// fallaba con "No such container: matrix". Ahora buscamos por LABEL, que NimOS
// inyecta en TODOS los containers del stack (com.nimos.app_id=<appID>).
//
// Incluye containers parados (-a) para poder arrancarlos. Devuelve nombres
// (no IDs) porque docker start/stop/logs aceptan nombre y son más legibles
// en logs. Vacío si no hay containers con ese label.
func getStackContainerNames(ctx context.Context, appID string) []string {
	cctx, cancel := context.WithTimeout(ctx, dockerCmdTimeout)
	defer cancel()

	if appID == "" {
		return nil
	}

	psCmd := exec.CommandContext(cctx, "docker", "ps", "-a",
		"--filter", "label="+LabelAppID+"="+appID,
		"--format", "{{.Names}}")
	out, err := psCmd.Output()
	if err != nil {
		logMsg("docker_labels: getStackContainerNames ps falló para %s: %v", appID, err)
		return nil
	}

	names := strings.Fields(strings.TrimSpace(string(out)))
	return names
}
