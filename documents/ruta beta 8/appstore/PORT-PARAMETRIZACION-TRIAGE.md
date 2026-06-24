# Triage · Parametrización del puerto editable por app

**Fecha:** 21/06/2026
**Base:** catálogo LIVE (`raw.githubusercontent.com/andresgv-beep/NimOs-appstore/main/catalog.json`)
**Objetivo:** que el puerto host de cada app sea editable en el modal de instalación (estilo Synology/Unraid), reutilizando el contrato `purpose:"network_port"` que ya funciona en code-server.

**Regla de oro del patrón (probado en code-server):**
- Compose: `ports: - '${APP_PORT}:<contenedor>'`
- Config: un `configField` `type:"number"`, `default` = puerto host actual, `validation:{type:"port"}`, `purpose:"network_port"`, `protocol:"tcp"`.
- El frontend ya lleva el valor elegido a `body.port` → `docker_apps.port` → launcher. Cero cambios de motor para TCP.

Consecuencia: al tener cada app su campo de puerto, **el modal aparece solo** (`needsConfigModal` ya da `true`). No hace falta forzar "modal obligatorio" como flag aparte.

---

## ✅ YA HECHAS (parametrizadas)

| App | Compose actual | Estado |
|-----|----------------|--------|
| codeserver | `${CODE_PORT}:8443` | ✓ con configRef |
| minecraft-java | `${MC_PORT}:25565` | ✓ con configRef (RCON 25575 es interno 127.0.0.1, no se toca) |

---

## 🟢 BUCKET FÁCIL · copia directa de code-server (TCP, 1 puerto principal)

Solo hay que parametrizar el puerto host y añadir el `configField`. Sin motor, sin riesgo.

| App | Compose actual | → Parametrizado | Var | Default |
|-----|----------------|-----------------|-----|---------|
| jellyfin | `8096:8096` | `${JELLYFIN_PORT}:8096` | JELLYFIN_PORT | 8096 |
| plex | `32400:32400` | `${PLEX_PORT}:32400` | PLEX_PORT | 32400 |
| navidrome | `4533:4533` | `${NAVIDROME_PORT}:4533` | NAVIDROME_PORT | 4533 |
| nextcloud | `8080:80` | `${NEXTCLOUD_PORT}:80` | NEXTCLOUD_PORT | 8080 |
| vaultwarden | `8082:80` | `${VAULT_PORT}:80` | VAULT_PORT | 8082 |
| portainer | `9000:9000` | `${PORTAINER_PORT}:9000` | PORTAINER_PORT | 9000 |
| prometheus | `9090:9090` | `${PROMETHEUS_PORT}:9090` | PROMETHEUS_PORT | 9090 |
| prowlarr | `9696:9696` | `${PROWLARR_PORT}:9696` | PROWLARR_PORT | 9696 |
| radarr | `7878:7878` | `${RADARR_PORT}:7878` | RADARR_PORT | 7878 |
| sonarr | `8989:8989` | `${SONARR_PORT}:8989` | SONARR_PORT | 8989 |
| n8n | `5678:5678` | `${N8N_PORT}:5678` | N8N_PORT | 5678 |
| ketesa | `8087:8080` | `${KETESA_PORT}:8080` | KETESA_PORT | 8087 |
| element | `8086:80` | `${ELEMENT_PORT}:80` | ELEMENT_PORT | 8086 |
| uptime-kuma | `3002:3001` | `${UPTIME_PORT}:3001` | UPTIME_PORT | 3002 |
| grafana | `3001:3000` | `${GRAFANA_PORT}:3000` | GRAFANA_PORT | 3001 |
| handbrake | `5817:5800` | `${HANDBRAKE_PORT}:5800` | HANDBRAKE_PORT | 5817 |

**Ya tienen configRef** (solo añadir el campo al `.config.json` existente): grafana, handbrake.
**Aparcadas por OTRO motivo** (config, no puerto) pero el puerto es igual de fácil cuando se retomen: matrix-synapse (`${SYNAPSE_PORT}:8008`), authelia (`${AUTHELIA_PORT}:9091`, host 9092), immich (`${IMMICH_PORT}:2283`, además es multi-servicio).

---

## 🟡 BUCKET MULTI-PUERTO · parametrizar SOLO el principal (web)

Publican 2+ puertos host. El principal (web/GUI) es TCP y va igual que el bucket fácil con `purpose:"network_port"`. Los secundarios se dejan fijos por ahora (o se parametrizan SIN purpose más adelante).

| App | Puerto principal (parametrizar) | Secundarios (dejar fijos de momento) |
|-----|-------------------------------|--------------------------------------|
| gitea | `${GITEA_PORT}:3000` (web) | `2222:22` (SSH) |
| qbittorrent | `${QBIT_PORT}:8081` (web) | `6881` tcp+**udp** (torrent → wiring) |
| syncthing | `${SYNCTHING_PORT}:8384` (GUI) | `22000` tcp+**udp** (sync → wiring) |
| transmission | `${TRANSMISSION_PORT}:9091` (web) | `51413` tcp+**udp** (peer → wiring) |

---

## 🔴 BUCKET QUE NECESITA MOTOR (udp/rango/conflicto) · NO tocar aún

Aquí el puerto que de verdad importa NO es un TCP web simple. Necesitan el wiring de `PortBinding` udp/rangos antes de que su campo sirva.

| App | Por qué |
|-----|---------|
| **pihole** | Web (`8088:80`) es TCP fácil, PERO el puerto que causa conflictos es el **DNS `53` tcp+udp** → udp wiring + es el caso "elige una" (protocolo fijo, no reasignable como un web). |
| **adguard** | Igual que pihole: web (`3000`/`8083:80`) fácil, pero el **DNS `53` tcp+udp** es el del incidente. udp wiring + "elige una". |

**Ironía útil:** el puerto que disparó todo el lío (el `:53` de Pi-hole/AdGuard) es justo el que NO es copia directa de code-server. Por eso el **check de conflicto al instalar** es el compañero imprescindible — ver abajo.

---

## ⚪ ESPECIALES / SE SALTAN

| App | Motivo |
|-----|--------|
| **docker-engine** | App de sistema, sin puerto publicado. |
| **homeassistant** | `network_mode: host` → no usa mapeo `ports:`. Cambiar su puerto es config de HA, no de docker. Especial. |
| **nginx-proxy** | Es infra de proxy: `80/443/81`. El 80/443 son fijos por su función (romperlos rompe el proxy). No parametrizar. |

---

## ⚠️ BUG PRE-EXISTENTE detectado al hacer el triage (independiente de esta feature)

- **emby**: `catalog.port = 8096` pero el compose publica host **`8920:8096`**. O sea NimOS apunta al **8096** pero Emby sirve en el **8920** → **acceso roto hoy** (si está instalada). Al parametrizar, poner `${EMBY_PORT}:8096` con **default 8920** y corregir `catalog.port` a 8920. (Revisar también que adguard `catalog.port=3000` apunte a donde quieres: setup en 3000 vs dashboard en 8083.)

---

## 📋 TEMPLATE (rellenar `<APP>`, `<VAR>`, `<HOST>`, `<CONTENEDOR>`)

**1) En `catalog.json`, el compose de la app** — cambiar la línea de `ports:`:
```yaml
ports:
  - '${<VAR>}:<CONTENEDOR>'
```

**2) En `configs/<APP>.config.json`** (crear si no existe, o añadir el campo si ya hay configRef):
```json
{
  "key": "<VAR>",
  "label": "Puerto web",
  "type": "number",
  "required": true,
  "default": "<HOST>",
  "validation": { "type": "port" },
  "hint": "Puerto por el que accederás a la app. Cámbialo si el <HOST> ya está ocupado.",
  "purpose": "network_port",
  "protocol": "tcp"
}
```

**3) Si la app NO tenía configRef**, añadir en su entrada de `catalog.json`:
```json
"configRef": "configs/<APP>.config.json"
```

---

## Compañero imprescindible (no es el modal): CHECK DE CONFLICTO

Aunque parametrices todo, dos apps pueden seguir pidiendo el mismo puerto host. En el flujo de install, antes de `docker compose up`:
1. Cruzar el puerto elegido contra los `docker_apps.port` de las apps ya instaladas.
2. Si choca y es reasignable (95%, TCP web) → avisar y ofrecer un puerto libre.
3. Si es protocolo fijo (DNS `:53`) → "elige una" (no se pueden dos).

Esto es lo que mata el error críptico de docker (`Bind for 0.0.0.0:53 failed`) ANTES de que ocurra. Vive en el frontend (modal) + un endpoint de consulta de puertos ocupados.

---

## Orden de ataque sugerido

1. **Bucket fácil (16 apps)** — ripear con el template. Cero riesgo, cubre el 95%.
2. **Multi-puerto (4 apps)** — solo el puerto principal. Rápido.
3. **Check de conflicto** — el verdadero premio de seguridad.
4. **Wiring udp/rangos** — desbloquea pihole/adguard (DNS), qbit/syncthing/transmission (secundarios), Valheim.
5. **Fix emby** (8920) — de paso, ya que se toca.
