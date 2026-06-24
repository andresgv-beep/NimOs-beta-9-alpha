# NimBackup — Documentación de Desarrollo

**Versión:** Beta 5  
**Estado:** UI completa · Daemon pendiente  
**App ID:** `nimbackup`  
**Dimensiones:** 920 × 600

---

## Visión general

NimBackup es la app de backup y sincronización de NimOS. Permite emparejar múltiples NAS (u otros dispositivos) y gestionar trabajos de backup incrementales usando ZFS send/receive o Btrfs send/receive, además de sincronización bidireccional de carpetas.

El emparejamiento entre dispositivos usa WireGuard como túnel cifrado cuando la conexión es remota (WAN). En red local (LAN) se conecta directamente sin túnel.

---

## Arquitectura

```
NimBackup.svelte          →  src/lib/apps/NimBackup.svelte
Wizard emparejamiento     →  src/lib/apps/NimLink.svelte  (pendiente)
Daemon endpoints          →  daemon/backup.go              (pendiente)
WireGuard gestión         →  daemon/wireguard.go           (pendiente)
```

---

## Detección LAN vs WAN

La app detecta automáticamente si el dispositivo es local o remoto:

```js
function isLocal(addr) {
  return addr.startsWith('192.168.') ||
         addr.startsWith('10.')      ||
         addr.startsWith('172.')     ||
         addr === 'localhost';
}

// Puerto según tipo de conexión
// LAN  →  http  · puerto 5000
// WAN  →  https · puerto 5009

function devicePort(addr)  { return isLocal(addr) ? 5000 : 5009; }
function deviceProto(addr) { return isLocal(addr) ? 'http' : 'https'; }
```

---

## Iconos de dispositivo

Los iconos son SVG inline definidos en `DEVICE_ICONS` — fácil de ampliar o reemplazar:

```js
const DEVICE_ICONS = {
  nas:    `<svg>...</svg>`,   // Servidor NAS con discos
  usb:    `<svg>...</svg>`,   // Disco externo / USB
  server: `<svg>...</svg>`,   // Servidor rack
}
```

Cada dispositivo tiene un campo `type: 'nas' | 'usb' | 'server'` que mapea al icono. Para añadir un nuevo tipo basta con añadir la clave y el SVG.

---

## Modelo de datos

### Dispositivo emparejado

```json
{
  "id": "dev_abc123",
  "name": "Casa Playa",
  "addr": "192.168.1.50",
  "type": "nas",
  "online": true,
  "ping": "4ms",
  "freeSpace": "2.1 TB",
  "version": "Beta 5",
  "purposes": ["backup_dest", "sync"],
  "syncPairs": [
    {
      "id": "pair_001",
      "local": "/data/documentos",
      "remote": "/data/documentos",
      "status": "synced"
    }
  ],
  "wireguard": {
    "active": true,
    "publicKey": "...",
    "endpoint": "192.168.1.50:51820",
    "allowedIPs": "10.10.0.2/32"
  }
}
```

### Trabajo de backup

```json
{
  "id": "job_001",
  "name": "volume1/data → Casa Playa",
  "deviceId": "dev_abc123",
  "fsType": "zfs",
  "source": "volume1/data",
  "dest": "/nimos/backups/volume1",
  "schedule": "diario 02:00",
  "retention": "30d",
  "status": "ok",
  "lastRun": "2026-03-22T02:00:00Z",
  "nextRun": "2026-03-23T02:00:00Z",
  "lastSize": 245366784
}
```

### Propósitos disponibles

| Key | Descripción |
|-----|-------------|
| `backup_dest` | Este NAS recibe backups del NAS local |
| `backup_src` | Este NAS envía backups al NAS local |
| `share` | Monta carpetas remotas en Files (SSHFS/NFS) |
| `sync` | Sincronización bidireccional de carpetas |

---

## Endpoints daemon necesarios (Go)

### Dispositivos

```
GET    /api/backup/devices                    Lista dispositivos emparejados
POST   /api/backup/devices                    Añadir dispositivo (post-emparejamiento)
DELETE /api/backup/devices/:id                Desemparejar dispositivo
GET    /api/backup/devices/:id/status         Ping + estado en tiempo real
POST   /api/backup/devices/:id/purposes       Actualizar propósitos del dispositivo
```

### Trabajos de backup

```
GET    /api/backup/jobs                       Lista todos los trabajos
POST   /api/backup/jobs                       Crear nuevo trabajo
PUT    /api/backup/jobs/:id                   Editar trabajo
DELETE /api/backup/jobs/:id                   Eliminar trabajo
POST   /api/backup/run/:id                    Ejecutar trabajo manualmente
GET    /api/backup/jobs/:id/status            Estado en tiempo real (SSE recomendado)
```

### Historial

```
GET    /api/backup/history                    Historial global
GET    /api/backup/history?deviceId=:id       Historial de un dispositivo
```

### Snapshots

```
GET    /api/backup/snapshots                  Lista snapshots activos (ZFS + Btrfs)
POST   /api/backup/snapshots                  Crear snapshot manual
DELETE /api/backup/snapshots/:name            Eliminar snapshot
```

### Emparejamiento (NimLink)

```
POST   /api/backup/pair/scan                  Escanear LAN buscando NimOS (puerto 5000)
POST   /api/backup/pair/connect               Conectar a un dispositivo (addr + credenciales)
POST   /api/backup/pair/verify-2fa            Verificar código TOTP si el remoto tiene 2FA
GET    /api/backup/pair/status                Estado del proceso de emparejamiento activo
```

---

## Flujo de emparejamiento (NimLink)

```
1. Escaneo LAN
   → El daemon hace TCP connect al puerto 5000 en toda la subred
   → Solo responden los que tienen NimOS corriendo
   → Resultado: lista de { addr, name, version }

2. Selección + credenciales
   → Usuario elige dispositivo (o introduce URL manual para WAN)
   → Introduce usuario y contraseña del NAS remoto
   → Si el remoto tiene 2FA → pantalla de código TOTP

3. Autenticación mutua
   → POST https://[addr]:[port]/api/auth/login con las credenciales
   → Recibe token temporal de emparejamiento

4. Intercambio WireGuard (solo si WAN)
   → NAS-1 genera par de claves WireGuard
   → Envía su public key al NAS-2 via API autenticada
   → NAS-2 genera su par, devuelve su public key
   → Ambos configuran wg0 con los datos intercambiados
   → Se verifica la conectividad con ping por el túnel

5. Registro local
   → El dispositivo queda guardado en la DB con su configuración
   → Si es LAN: conexión directa, sin WireGuard
   → Si es WAN: todo el tráfico pasa por wg0
```

---

## Implementación Go — backup.go

### Estructura de datos sugerida

```go
type BackupDevice struct {
    ID         string      `json:"id"`
    Name       string      `json:"name"`
    Addr       string      `json:"addr"`
    Type       string      `json:"type"`        // nas | usb | server
    Purposes   []string    `json:"purposes"`
    WireGuard  *WGConfig   `json:"wireguard,omitempty"`
    CreatedAt  time.Time   `json:"createdAt"`
}

type BackupJob struct {
    ID         string    `json:"id"`
    Name       string    `json:"name"`
    DeviceID   string    `json:"deviceId"`
    FsType     string    `json:"fsType"`      // zfs | btrfs
    Source     string    `json:"source"`      // dataset o path
    Dest       string    `json:"dest"`        // path en el destino
    Schedule   string    `json:"schedule"`    // cron expression
    Retention  string    `json:"retention"`   // e.g. "30d", "12"
    Status     string    `json:"status"`      // ok | warn | error | running
    LastRun    time.Time `json:"lastRun"`
    NextRun    time.Time `json:"nextRun"`
    LastSize   int64     `json:"lastSize"`
}

type HistoryEntry struct {
    ID        string    `json:"id"`
    JobName   string    `json:"jobName"`
    DeviceID  string    `json:"deviceId"`
    Dest      string    `json:"dest"`
    OK        bool      `json:"ok"`
    Bytes     int64     `json:"bytes"`
    Duration  int       `json:"duration"`   // segundos
    Error     string    `json:"error,omitempty"`
    Time      time.Time `json:"time"`
}
```

### Ejecución ZFS send/receive

```go
// Backup incremental ZFS
// 1. Crear snapshot en origen
cmd := fmt.Sprintf("zfs snapshot %s@nimbackup-%s", source, timestamp)

// 2. Si hay snapshot anterior, enviar incremental
if lastSnap != "" {
    cmd = fmt.Sprintf(
        "zfs send -i %s@%s %s@%s | ssh %s 'zfs receive -F %s'",
        source, lastSnap, source, timestamp, remoteAddr, dest,
    )
} else {
    // Primera vez — envío completo
    cmd = fmt.Sprintf(
        "zfs send %s@%s | ssh %s 'zfs receive -F %s'",
        source, timestamp, remoteAddr, dest,
    )
}
```

### Ejecución Btrfs send/receive

```go
// Backup incremental Btrfs
// 1. Crear snapshot readonly
cmd := fmt.Sprintf(
    "btrfs subvolume snapshot -r %s %s/.snapshots/%s",
    source, source, timestamp,
)

// 2. Enviar incremental si existe snapshot anterior
if lastSnap != "" {
    cmd = fmt.Sprintf(
        "btrfs send -p %s/.snapshots/%s %s/.snapshots/%s | ssh %s 'btrfs receive %s'",
        source, lastSnap, source, timestamp, remoteAddr, dest,
    )
} else {
    cmd = fmt.Sprintf(
        "btrfs send %s/.snapshots/%s | ssh %s 'btrfs receive %s'",
        source, timestamp, remoteAddr, dest,
    )
}
```

---

## WireGuard — wireguard.go

```go
type WGConfig struct {
    Active      bool   `json:"active"`
    Interface   string `json:"interface"`    // wg0
    LocalIP     string `json:"localIP"`      // 10.10.0.1/24
    RemoteIP    string `json:"remoteIP"`     // 10.10.0.2/24
    PublicKey   string `json:"publicKey"`
    PrivateKey  string `json:"-"`            // nunca al cliente
    Endpoint    string `json:"endpoint"`     // addr:51820
    AllowedIPs  string `json:"allowedIPs"`
    ListenPort  int    `json:"listenPort"`   // 51820
}

// Generar par de claves
func generateWGKeys() (private, public string, err error) {
    privateKey, err := wgtypes.GeneratePrivateKey()
    return privateKey.String(), privateKey.PublicKey().String(), err
}

// Escribir configuración wg0
func writeWGConfig(cfg WGConfig) error {
    // Escribe en /etc/wireguard/wg0.conf
    // Ejecuta: wg-quick up wg0
}
```

---

## Escaneo LAN

```go
func scanLAN(subnet string) []DiscoveredDevice {
    // subnet: "192.168.1.0/24"
    // Hace TCP connect al puerto 5000 en paralelo (goroutines)
    // Si responde, hace GET /api/auth/status para confirmar que es NimOS
    // Devuelve: [{ addr, name, version }]
}
```

Timeout recomendado: 300ms por host, 254 goroutines en paralelo.

---

## 2FA en emparejamiento

Si el NAS remoto tiene 2FA activado, el endpoint `/api/auth/login` devuelve:

```json
{ "requires2FA": true }
```

El wizard muestra entonces el campo de código TOTP antes de continuar. El código se envía en el segundo POST:

```json
{
  "username": "admin",
  "password": "...",
  "totpCode": "123456"
}
```

---

## Pendiente

- [ ] `daemon/backup.go` — todos los endpoints
- [ ] `daemon/wireguard.go` — gestión del túnel WireGuard
- [ ] `src/lib/apps/NimLink.svelte` — wizard de emparejamiento completo
- [ ] Scheduler interno en Go para ejecutar los trabajos según cron
- [ ] SSE (Server-Sent Events) para progreso en tiempo real del backup
- [ ] Política de retención automática — eliminar snapshots antiguos según reglas
- [ ] Notificaciones cuando un backup falla
