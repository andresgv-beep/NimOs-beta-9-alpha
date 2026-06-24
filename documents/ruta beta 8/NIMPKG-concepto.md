# NimPKG — Formato de paquete nativo para NimOS

**Documento de concepto · Beta 8.1 · borrador para discusión**

> El objetivo: que instalar una app nativa de NimOS (NimTorrent y las que vengan) sea **uniforme, declarativo y atómico**. Un solo archivo firmado que NimOS abre, verifica, coloca en su sitio y registra — o no instala nada. Cero ficheros sueltos, cero apps a medias, cero basura huérfana al desinstalar.

---

## 1. El problema que resuelve

Hoy una app nativa como NimTorrent vive dispersa por el árbol: su UI en `src/lib/apps/NimTorrent.svelte`, su widget en `src/lib/widgets/Torrent.svelte`, su backend en `daemon/torrent_tmpdir.go` y `daemon/maintenance_torrent.go`, más su integración con notificaciones, permisos y carpetas. Meterla "a mano" o como un stack Docker es sucio y propenso a dejar restos (ya pasó con el ghost `nimbus` y las apps Docker zombi).

Una app Docker se resuelve sola con su `compose`. Una app **nativa** no: necesita un contrato que diga *qué* trae, *dónde* va cada cosa y *con qué módulos de NimOS se integra*. Eso es NimPKG.

### Principios de diseño

1. **Declarativo** — la app describe su intención en un manifest; NimOS la integra. La app no "hace cosas" al sistema; declara qué necesita y NimOS decide cómo y dónde.
2. **Atómico** — la instalación es una transacción: o entera, o nada. Nunca un estado intermedio corrupto.
3. **Verificable** — todo paquete va firmado (ed25519). NimOS solo instala lo que confía.
4. **Reversible** — desinstalar deshace *exactamente* lo que se instaló, sin dejar basura.
5. **Uniforme** — todas las apps siguen el mismo patrón, así el instalador es uno solo y predecible.

---

## 2. Estructura del paquete

Un `.nimpkg` es un archivo comprimido (tar+zstd o zip) con una estructura fija:

```
nimtorrent-1.4.0.nimpkg
├── manifest.json          # el contrato de la app (ver §3)
├── manifest.json.sig      # firma ed25519 del manifest
├── checksums.txt          # hash SHA-256 de cada fichero del payload
├── checksums.txt.sig      # firma de los checksums
└── payload/
    ├── ui/
    │   ├── NimTorrent.svelte      # la app
    │   └── icon.svg               # icono del launcher
    ├── widgets/
    │   └── Torrent.svelte         # widget opcional
    ├── bin/
    │   └── nimos-torrentd         # binario del daemon de la app (ARM64)
    ├── config/
    │   └── defaults.json          # config por defecto
    └── hooks/
        ├── preinstall.sh          # opcional
        └── postinstall.sh         # opcional
```

La firma cubre el **manifest** y los **checksums**; los checksums cubren cada fichero del payload. Cadena de confianza completa: firma válida → checksums auténticos → cada fichero íntegro. Si algo no cuadra, el paquete se rechaza antes de tocar el sistema.

---

## 3. El manifest — el contrato declarativo

Aquí vive la **intención** de la app. Cada app rellena lo que usa; lo que no, lo omite. NimOS lee este manifest y realiza la integración de forma uniforme y controlada.

```jsonc
{
  "nimpkg_version": "1",                 // versión del FORMATO (no de la app)
  "id": "nimtorrent",                    // identificador único, inmutable
  "name": "NimTorrent",
  "version": "1.4.0",                    // versión de la APP (semver)
  "author": "Andrés",
  "description": "Cliente de torrents integrado en NimOS",
  "min_nimos": "8.1.0",                  // versión mínima de NimOS requerida
  "arch": ["arm64"],                     // arquitecturas soportadas

  // ── Componentes que aporta (NimOS los coloca en su sitio) ──
  "components": {
    "ui": {
      "entry": "ui/NimTorrent.svelte",
      "icon": "ui/icon.svg",
      "launcher": true,                  // aparece en el launcher
      "window": { "width": 1100, "height": 700 }
    },
    "widget": {                          // OPCIONAL · solo si la app trae widget
      "entry": "widgets/Torrent.svelte",
      "id": "torrent",
      "default_size": "2x1",
      "poll_endpoint": "/api/torrent/stats"   // contrato del widget system
    },
    "daemon": {                          // OPCIONAL · backend nativo
      "binary": "bin/nimos-torrentd",
      "service": true,                   // se gestiona como servicio
      "ports": [9091],                   // puertos que usa (loopback por defecto)
      "health": "/api/torrent/health"
    }
  },

  // ── Notificaciones que registra (si las trae) ──
  "notifications": {
    "channels": [
      { "id": "torrent.complete", "label": "Descarga completada", "default": true },
      { "id": "torrent.error",    "label": "Error de descarga",   "default": true }
    ]
  },

  // ── Permisos que solicita (NimOS pide consentimiento del admin) ──
  "permissions": {
    "storage": {
      "pools": ["read", "write"],        // acceso a pools de almacenamiento
      "create_subvolume": true           // puede crear su subvolumen BTRFS
    },
    "user": {
      "per_user_data": true,             // datos separados por usuario
      "acl": ["downloads_dir"]           // ACLs POSIX que necesita
    },
    "network": {
      "outbound": true,                  // necesita salida a internet (peers)
      "listen_ports": [6881]             // puerto de escucha (declarado, auditable)
    },
    "system": {
      "exec": false,                     // ¿ejecuta comandos del host? (auditado)
      "privileged": false                // jamás root salvo declaración explícita
    }
  },

  // ── Conexión con módulos/recursos existentes de NimOS ──
  "integrations": {
    "pools": {
      "requires_pool": true,             // necesita un pool de storage
      "default_dir": "downloads",        // carpeta que crea/usa
      "respects_quotas": true            // honra las quotas BTRFS de NimOS
    },
    "managed_folders": true,             // se integra con Managed Folders (Fase 3)
    "shield": {
      "exposable": true                  // puede exponerse vía Caddy/NimShield
    }
  },

  // ── Hooks del ciclo de vida (opcionales) ──
  "hooks": {
    "preinstall": "hooks/preinstall.sh",
    "postinstall": "hooks/postinstall.sh",
    "preuninstall": "hooks/preuninstall.sh"
  }
}
```

### Por qué declarativo importa

- **Auditable**: el admin ve *antes de instalar* qué pide la app (qué puertos, qué pools, si ejecuta comandos). Nada oculto.
- **Uniforme**: NimOS coloca el icono, registra el widget, da de alta las notificaciones y aplica permisos *de la misma forma* para toda app. Un solo camino, probado.
- **Seguro por defecto**: lo no declarado, no se concede. `privileged: false`, `exec: false` y loopback son el default; una app que quiera más debe declararlo explícitamente y el admin aprobarlo.

---

## 4. Instalación atómica — el corazón del sistema

El requisito clave: **o se instala todo bien, o no se instala nada**. Nunca un estado intermedio con archivos sueltos. El modelo es **staging → commit → journal → rollback**, igual que una transacción de base de datos.

### Fase A — Staging (nada toca el sistema real todavía)

```
1. Verificar la firma del manifest y de los checksums (ed25519).
   → si falla: RECHAZAR. El sistema no se ha tocado.
2. Descomprimir el payload en un área temporal aislada (/tmp/nimpkg-staging/<id>).
3. Validar:
   · ¿el manifest es válido y completo?
   · ¿el hash de CADA fichero del payload coincide con checksums.txt?
   · ¿min_nimos y arch son compatibles?
   · ¿hay conflicto con una app ya instalada (mismo id/puerto/carpeta)?
   · ¿los permisos solicitados están aprobados por el admin?
4. Si CUALQUIER validación falla → borrar staging, abortar. Sistema intacto.
```

En toda la Fase A no se ha escrito **nada** en el sistema real. Un paquete corrupto, mal firmado o conflictivo se detiene aquí sin consecuencias.

### Fase B — Commit (colocar, registrando cada paso en un journal)

```
Se abre un JOURNAL de instalación (fila en SQLite, transaccional):
  install_journal(id, version, state='installing', steps=[])

Por cada acción, se registra ANTES de hacerla:
  1. Colocar UI         → src/lib/apps/NimTorrent.svelte      [journal: +file]
  2. Colocar icono      → (registro del launcher)             [journal: +launcher]
  3. Colocar widget     → src/lib/widgets/Torrent.svelte      [journal: +widget]
  4. Colocar binario    → /opt/nimos/apps/nimtorrent/bin/     [journal: +file]
  5. Crear carpetas/pool→ subvolumen + quota + ACL            [journal: +subvol]
  6. Registrar notifs   → canales en la DB                    [journal: +notif]
  7. Aplicar permisos   → según manifest                      [journal: +perms]
  8. Registrar la app   → docker_apps/native_apps en DB       [journal: +dbrow]
  9. postinstall hook   → (en sandbox, sin privilegios)       [journal: +hook]

Colocación de ficheros con os.Root (como ya hace el módulo Files) →
imposible que un fichero del paquete escape de su destino (anti path traversal).
```

### Fase C — Resultado

```
ÉXITO → journal.state = 'installed', se conserva como registro de qué se puso
        (lo necesita la desinstalación). La app está viva.

FALLO a mitad del commit (disco lleno, permiso, corte de luz…):
  → ROLLBACK guiado por el journal: deshacer en ORDEN INVERSO exactamente
    los pasos ya aplicados. Borra ficheros puestos, elimina el subvolumen,
    quita los canales de notificación, revierte la fila de DB.
  → El sistema vuelve al estado previo. Cero archivos sueltos.
```

### El caso difícil: corte de luz a mitad

```
Al arrancar, NimOS revisa install_journal por entradas en state='installing'
(instalaciones que nunca llegaron a 'installed'):
  → son instalaciones incompletas → ejecuta su ROLLBACK automáticamente.
  → NimOS arranca siempre en un estado consistente. Nunca apps a medias.
```

Esto es lo que separa un gestor de paquetes serio de un "descomprime y reza".

---

## 5. Desinstalación — sin basura huérfana

La desinstalación lee el **journal/manifest** de lo que se instaló y deshace *exactamente* eso:

```
1. preuninstall hook (opcional)
2. Parar el daemon de la app si lo tiene
3. Recorrer el journal en orden inverso:
   · quitar ficheros (UI, icono, widget, binario)
   · des-registrar widget y canales de notificación
   · revertir permisos/ACLs
   · borrar la fila de la DB
4. DATOS DEL USUARIO: preguntar. "¿Conservar las descargas/datos?"
   → conservar (default seguro) o borrar el subvolumen/pool.
5. Borrar el registro del journal.
```

Esto mata directamente el problema del ghost `nimbus`: nunca queda un usuario, carpeta o puerto huérfano, porque NimOS sabe *exactamente* qué puso.

---

## 6. Actualizaciones

```
app v1.4.0 → v1.5.0:
  1. Staging + verificación del paquete nuevo (igual que una instalación).
  2. Diff de manifests: ¿cambian permisos? ¿puertos nuevos? → re-aprobación
     del admin solo si pide MÁS de lo que ya tenía.
  3. Instalación atómica de la versión nueva (mismo journal/rollback).
  4. PRESERVAR datos del usuario y config (no se tocan; el payload reemplaza
     solo binario/UI/widget, nunca los datos).
  5. Hook de migración opcional (migrate.sh) para cambios de esquema de la app.
  6. Si falla → rollback a v1.4.0, datos intactos.
```

La regla de oro: **el código se reemplaza, los datos del usuario nunca se tocan** salvo migración explícita y reversible.

---

## 7. Seguridad — guardarraíles

| Riesgo | Mitigación |
|---|---|
| Paquete falso / manipulado | Firma ed25519 verificada **antes** de desempaquetar |
| Fichero corrupto en tránsito | Checksum SHA-256 por fichero, firmado |
| Path traversal al colocar | `os.Root` para toda escritura (ya usado en Files) |
| App pide privilegios ocultos | Permisos **declarados** en manifest + aprobación del admin |
| App ejecuta comandos del host | `exec`/`privileged` declarados y auditados; default `false` |
| Instalación a medias | Transacción atómica con journal + rollback |
| Basura al desinstalar | Desinstalación guiada por journal |
| Clave de firma comprometida | Privada en máquina aislada (Pi dedicada); pública embebida en NimOS |

**Principio rector**: lo no declarado, no se concede. El default de todo permiso es el más restrictivo (loopback, sin exec, sin privilegios, datos por usuario).

---

## 8. NimTorrent como caso de referencia

Para validar el formato, NimTorrent empaquetado sería:

```
nimtorrent-1.4.0.nimpkg
  manifest.json    → id=nimtorrent, ui+widget+daemon, notifs complete/error,
                     permisos: storage(pool rw), network(outbound, :6881),
                     integrations: requires_pool, default_dir=downloads
  payload/ui/NimTorrent.svelte   (existe hoy)
  payload/widgets/Torrent.svelte (existe hoy)
  payload/bin/nimos-torrentd     (el daemon de torrent)
  payload/config/defaults.json
```

Si el formato hace que NimTorrent "quepa" limpio, sirve para las apps grandes que vienen.

---

## 9. Plan por fases (propuesto)

```
FASE 0 · Ladrillo de firma (ed25519)
   · gen-keys, firmar, verificar. Reutilizable para blocklist Y nimpkg.

FASE 1 · Formato + manifest + empaquetador
   · spec del manifest, validador, herramienta que crea un .nimpkg.
   · Caso de prueba: empaquetar NimTorrent.

FASE 2 · Instalador atómico (staging + commit + journal + rollback)
   · El corazón. Instalar NimTorrent desde su .nimpkg en hardware.

FASE 3 · Desinstalación + actualización
   · Reverso limpio + upgrade preservando datos.

FASE 4 · Recuperación al arranque
   · Detectar instalaciones incompletas (state='installing') y limpiarlas.

FASE 5 · AppStore consume .nimpkg
   · La tienda instala apps nativas firmadas con un click.
```

Cada fase se valida en la Pi antes de la siguiente — la misma disciplina que dejó NimShield redondo.

---

## 10. Preguntas abiertas (para decidir)

1. **Compresión**: ¿tar+zstd (mejor ratio, dependencia) o zip (estándar, en Go nativo)?
2. **Ubicación de apps nativas**: ¿`/opt/nimos/apps/<id>/` para binarios + la UI inyectada en el bundle de SvelteKit? ¿O las apps nativas se sirven dinámicamente?
3. **UI dinámica**: hoy las apps Svelte se compilan con el frontend. Una app instalada *después* necesita que NimOS sirva su `.svelte` sin recompilar todo → decisión técnica importante (¿componentes cargados dinámicamente? ¿bundle por app?).
4. **Hooks**: ¿se permiten scripts (`postinstall.sh`) o solo acciones declarativas predefinidas (más seguro, menos flexible)?
5. **Firma**: ¿una sola clave (la tuya) o soporte para múltiples editores de confianza a futuro?

La #3 es la más gorda técnicamente y conviene resolverla pronto, porque condiciona todo el formato de la parte UI.
```
