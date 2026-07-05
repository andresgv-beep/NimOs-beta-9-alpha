# DESIGN — Nimos Library

**Estado:** Borrador de diseño (v0.3) · pre-código · LISTO PARA FASE 0
**Autor:** Andrés + Claude (co-dev)
**Contexto:** NimOS Beta 9 alpha · app diferenciadora del ecosistema
**Fecha:** 2026-07 (sesión de diseño)

---

## Changelog

### v0.3 — Coherencia interna (segundo pase de review)
Correcciones de coherencia (no de arquitectura). El documento se cierra aquí:
seguir puliendo el papel renta menos que ejecutar Fase 0.
- **Shim ya NO es "opcional" (§2):** en v0.2 pasó a validar auth, reescribir links
  y servir `/api/article`. Etiqueta "opcional v1" del diagrama corregida →
  **obligatorio, corazón del diseño.**
- **D6 ampliada — auth POR CANAL (§6/D6):** el token solo existe vía panel NimOS
  (`WebApp.svelte`). La pestaña directa (§5.2) NO tiene token. Decisión escrita:
  *GET sin token en LAN, POST exigen token; token vía cookie/postMessage, NUNCA en
  URL.* Cada canal (panel / pestaña LAN / pestaña pública) es un contexto de auth
  distinto.
- **Spike CSS ampliado a 3 ejes (§4.6):** las decisiones "reescritura de links" y
  "técnica de render" están ACOPLADAS (si es iframe, los links viven dentro del
  documento del iframe). El spike valida por técnica: (1) aislamiento CSS,
  (2) interceptación de clicks, (3) scroll/atrás-adelante.
- **Full-text como endpoint con coste (§6):** `GET /fulltext` dispara Xapian sobre
  90 GB → DoS barato sin POST. Añadido a candidatos de rate-limit (v1).

### v0.2 — Revisión de seguridad y render (correcciones tras review)
Incorpora una revisión que cazó dos agujeros **bloqueantes** y varios ajustes:
- **[BLOQUEANTE] Auth (nueva §6 + D6):** v0.1 no tenía NADA de autenticación
  mientras contemplaba exposición pública (Caddy) + endpoints `POST`. Riesgo real:
  cualquiera en internet podría ordenar descargas de 90 GB o leer/escribir notas.
  Añadida sección de seguridad y decisión D6.
- **[BLOQUEANTE] Spike de aislamiento CSS (§4.6 + Fase 1):** el HTML del ZIM trae
  CSS agresivo propio. Cómo se renderiza (innerHTML vs iframe vs Shadow DOM) es la
  decisión más delicada del lector y puede invalidar el diseño. Ahora es riesgo
  destacado + spike obligatorio en Fase 1.
- **Reescritura de links → al SHIM (resuelve contradicción §4.5 vs API):** en v0.1
  el frontend Y el shim reescribían links. Se centraliza en el **shim**; el
  frontend solo intercepta clicks para el routing. Todos los clientes (incl. app
  de escritorio) heredan el arreglo.
- **`/api/image` → passthrough directo:** proxear cada imagen por el shim duplica
  I/O (se nota en Pi). Ahora `/content/*` va por reverse proxy puro (streaming); el
  shim solo procesa artículos/catálogo.
- **D2 cerrada con contundencia:** NUNCA compartir el fichero SQLite con NimOS. Si
  NimOS necesita datos, los pide por API REST (aplicando el principio rector de §2).
- **Menores:** endpoint `POST /api/history` añadido; checklist de Fase 0 con `curl`
  real de `/search?format=xml` y `/suggest`; aviso de iframes anidados.

### v0.1 — Diseño inicial
Investigación kiwix-serve, arquitectura headless, fases, frontera GPL.

---

## 0. Resumen ejecutivo (TL;DR)

**Nimos Library** es un gestor de conocimiento offline con UI moderna, alternativa
a Kiwix. No es "otro lector de ZIM": es un **navegador de colecciones** con
pestañas, favoritos, notas, historial e índice de artículo, con estética NimOS.

**Decisiones cerradas en esta sesión:**

1. **Motor:** NO reinventamos el parseo ZIM. Usamos **kiwix-serve** (motor probado,
   imagen Docker oficial multi-arch amd64/arm64) como backend headless. En v2
   opcional se puede migrar a un engine propio con `python-libzim` sin tocar la UI.
2. **Frontend:** **Svelte** (mismo stack que NimOS). Justificado por el estado rico
   (pestañas, sidebar reactivo, notas), no por inercia. El minimalismo — no el
   vanilla — es lo que da robustez, y eso se mantiene en Svelte.
3. **Arquitectura headless:** el motor NUNCA sabe quién lo usa. UI, panel NimOS,
   CLI y futura app de escritorio son todos **clientes de una API**.
4. **Distribución triple, un solo desarrollo:**
   - Ventana dentro de NimOS (estilo Game Panel · iframe con `WebApp.svelte`).
   - Pestaña de navegador (cualquier dispositivo · público vía Caddy+DuckDNS).
   - App de escritorio nativa Linux/Windows (futuro · Wails/Tauri envolviendo la web).
5. **Licencia:** frontera GPL física. kiwix-serve (GPL) aislado en su contenedor;
   la UI habla por HTTP, no linka → sin contaminación por linkado.

**Regla de oro del proyecto:** construir por **fases**, v0.1 mínimo y verificable.
WebSocket, plugins y cache avanzada son **v2**, no v1.

**⛔ Dos bloqueantes a cerrar ANTES de picar código (ver §6 y §10):**
1. **Auth (D6):** definir el modelo de autenticación. Postura por defecto: *v0.1
   solo LAN, sin exponer; exposición pública exige auth del shim.*
2. **Spike de aislamiento CSS:** decidir cómo se renderiza el artículo (innerHTML
   vs iframe vs Shadow DOM) con un artículo real de Wikipedia. Es el corazón del
   lector y una incógnita que puede condicionar el diseño de la UI.

---

## 1. Investigación — kiwix-serve como motor

### 1.1. Por qué kiwix-serve y no engine propio (todavía)

| Criterio | kiwix-serve (envolver) | Engine propio (python-libzim) |
|---|---|---|
| Parseo ZIM | ✅ ya hecho, probado | ⚠️ lo escribes tú sobre libzim |
| Reescritura de links HTML | ✅ ya resuelto | 🔴 el punto que más duele |
| Búsqueda full-text (Xapian) | ✅ integrado | ⚠️ lo enganchas tú |
| Tiempo a v0.1 | días | 3-5 semanas |
| Abstracción multi-fuente | ⚠️ forzada (piensa en ZIM) | ✅ limpia |
| Multi-arch amd64/arm64 | ✅ imagen oficial | ✅ wheels oficiales |

**Conclusión:** empezar envolviendo kiwix-serve (rápido, robusto ya). El diseño
headless permite migrar a engine propio en v2 **sin rehacer la UI** (habla por REST).

### 1.2. Imagen Docker oficial

```
ghcr.io/kiwix/kiwix-serve:3.8.2
```
- Multi-arch confirmado: `linux/amd64`, `linux/arm64`, `linux/arm/v7`, `linux/386`.
- Puerto interno por defecto: **8080** (configurable con env `PORT`).
- Volumen de datos: `/data` (donde viven los `.zim`).
- Uso básico:
  ```bash
  docker run -v /ruta/zim:/data -p 8080:8080 ghcr.io/kiwix/kiwix-serve '*.zim'
  # o con biblioteca XML:
  docker run -v /ruta/zim:/data -p 8080:8080 ghcr.io/kiwix/kiwix-serve --library /data/library.xml
  ```
- **Recarga de biblioteca en caliente:** señal `SIGHUP` al proceso (o flag
  `--monitorLibrary`/`-M`). Clave para añadir/quitar colecciones sin reiniciar.

### 1.3. API HTTP de kiwix-serve (lo que consumirá nuestra UI)

> Base URL del motor: `http://<kiwix-serve>:8080`

#### Contenido (artículos, imágenes, recursos)
| Endpoint | Qué hace | Formato |
|---|---|---|
| `GET /content/{ZIM}/{PATH}` | Sirve una entrada (artículo, imagen, CSS…) del ZIM | HTML/binario |
| `GET /raw/{ZIM}/content/{PATH}` | Igual pero SIN procesado servidor (público, estable) | HTML/binario |
| `GET /raw/{ZIM}/meta/{METAID}` | Metadato del ZIM (título, descripción, favicon…) | según meta |
| `GET /ROOT/{ZIM}/...` (legacy) | Esquema antiguo, redirige a `/content/...` | — |

- `{ZIM}` = *book name* (ej. `wikipedia_es_all_nopic`).
- `{PATH}` = ruta interna del artículo (ej. `A/Saturno` o `Saturno` según ZIM).
- La **entrada principal** de un ZIM se obtiene del metadato / OPDS (campo main entry).

#### Búsqueda y sugerencias
| Endpoint | Qué hace | Formato | ⚠️ |
|---|---|---|---|
| `GET /suggest?content={ZIM}&term={q}` | Autocompletado por título (índice de títulos) | **JSON** ✅ | Ideal para la barra |
| `GET /search?books.name={ZIM}&pattern={q}` | Búsqueda full-text (Xapian) | **HTML** ⚠️ | Ver nota abajo |
| `GET /search?...&format=xml` | Búsqueda en formato OpenSearch | XML | Alternativa parseable |

**⚠️ NOTA CRÍTICA DE DISEÑO (búsqueda):** el `/search` devuelve **HTML** (una
página con enlaces + snippets), NO JSON estructurado. Esto es una limitación
conocida de kiwix-serve (issue #368). Tres opciones para la UI:
1. **Usar `/suggest` (JSON)** para el autocompletado instantáneo de la barra
   (cubre el 80% del uso: "escribo Satur → me sugiere Saturno").
2. Para búsqueda full-text con snippets, pedir `/search?...&format=xml` y
   **parsear el XML** en el backend-shim (no en el frontend).
3. Alternativa v2: engine propio con `python-libzim` → búsqueda Xapian nativa
   devuelta como JSON limpio.
   → **v0.1 usa `/suggest` (JSON); el full-text con snippets se pospone o se
   hace vía el shim parseando XML.**

#### Catálogo de biblioteca (qué colecciones hay)
| Endpoint | Qué hace | Formato |
|---|---|---|
| `GET /catalog/v2/root.xml` | Raíz del catálogo OPDS (v1.2) | XML (OPDS) |
| `GET /catalog/v2/entries` | Lista de ZIMs (paginada, filtrable) | XML (OPDS) |
| `GET /catalog/v2/categories` | Categorías de los ZIM | XML (OPDS) |
| `GET /catalog/v2/languages` | Idiomas disponibles | XML (OPDS) |
| `GET /catalog/search` | Filtra la biblioteca | XML (OPDS) |

- El catálogo es **XML (OPDS)**, no JSON → el shim lo parsea y lo re-expone como
  JSON limpio a nuestra UI.
- Filtros soportados: `lang`, `category`, `name`, paginación (`start`, `count`;
  por defecto 10 entradas).

#### Descarga de nuevas colecciones (catálogo remoto de Kiwix)
- Catálogo público OPDS: `https://library.kiwix.org/catalog/v2/entries`
- Descargas directas de `.zim`: `https://download.kiwix.org/zim/...`
- kiwix-serve puede auto-descargar con env `DOWNLOAD=<url>.zim` al arrancar
  (útil, pero para NimOS gestionamos las descargas nosotros → ver §5).

### 1.4. Consideraciones de rendimiento (honestas)

- Wikipedia completa ≈ **90+ GB** de `.zim`. Búsqueda Xapian tira de I/O + RAM.
- **En x86 (tu sobremesa): vuela.** En **Pi (ARM64): funciona, pero la búsqueda
  full-text en ZIMs enormes puede ir lenta.** Es límite de hardware, no de código.
- Mitigaciones de diseño: `/suggest` (títulos) es barato y rápido incluso en Pi;
  reservar el full-text pesado para cuando el usuario lo pide explícitamente;
  cache de sugerencias en el shim (v2).

### 1.5. Licencia — frontera GPL

- **libzim / kiwix-serve:** GPLv2/v3. Regla: **lo que LINKA hereda GPL.**
- Diseño que aísla el riesgo: kiwix-serve corre en **su propio contenedor/proceso**.
  Nuestra UI y el shim se comunican por **HTTP** (no linkan libzim).
- El **shim** (si usa python-libzim en v2) sería GPL → va en su propio binario/contenedor.
- El **frontend Svelte** habla por REST → no contaminado por linkado directo.
- **Nimos Library será open source** (coherente con NimOS), así que no hay conflicto
  ideológico; la separación es por **higiene de arquitectura y claridad legal**.

---

## 2. Arquitectura — headless, el motor nunca sabe quién lo usa

```
                    ┌─────────────────────────────────────────────┐
                    │  MOTOR (headless · GPL · sin interfaz)        │
                    │                                               │
                    │  kiwix-serve  ──►  lee .zim, busca, sirve HTML│
                    │      │                                        │
                    │  ┌───┴──────────────────────────────────┐    │
                    │  │ SHIM / library-api (OBLIGATORIO):       │    │
                    │  │ valida auth, reescribe links, normaliza│    │
                    │  │ OPDS/XML→JSON, gestiona colecciones,   │    │
                    │  │ notas, favoritos, descargas            │    │
                    │  └───────────────────────────────────────┘    │
                    └───────────────────────┬─────────────────────┘
                                            │  API REST (JSON)
              ┌─────────────────────────────┼─────────────────────────────┐
              │                             │                             │
     ┌────────┴─────────┐        ┌──────────┴──────────┐       ┌──────────┴─────────┐
     │  UI Web (Svelte)  │        │  Panel NimOS         │       │  CLI / API directa │
     │  el mockup bonito │        │  (iframe en ventana  │       │  scripts, automat. │
     │  pestañas, notas  │        │   estilo Game Panel) │       │                    │
     └───────────────────┘        └─────────────────────┘       └────────────────────┘
              │                             │
              └──────────── misma web ──────┘
                    (la ventana NimOS carga la MISMA URL que la pestaña)
```

**Principio rector:** el motor expone datos; los clientes deciden la presentación.
Cambiar kiwix-serve por otro backend en el futuro = la UI ni se entera (habla REST).

### 2.1. Componentes y responsabilidades

| Componente | Responsabilidad | Lenguaje/stack | Licencia |
|---|---|---|---|
| **kiwix-serve** | Parseo ZIM, búsqueda Xapian, servir contenido | C++ (ya hecho) | GPL |
| **library-api (shim)** | **OBLIGATORIO:** valida auth, reescribe links, normaliza JSON, OPDS→JSON, colecciones, notas, favoritos, descargas | Go o Python | GPL si usa libzim; MIT/propio si solo proxya kiwix-serve por HTTP |
| **UI Svelte** | Toda la interfaz: pestañas, sidebar, lector, notas | SvelteKit | Propia (open source) |

> **Decisión pendiente (§7-D1):** ¿el shim es Go (stack NimOS, solo proxya
> kiwix-serve por HTTP → NO linka libzim → licencia libre) o Python (permite migrar
> a python-libzim en v2 → GPL)? Recomendación v1: **shim en Go que solo habla HTTP
> con kiwix-serve** — mantiene stack unificado y licencia limpia. Si en v2 se quiere
> engine propio, se cambia el shim, no la UI.

### 2.2. Layout de disco (colecciones + estado)

```
/libraries/            # los .zim (colecciones fuente ZIM)
    wikipedia_es.zim
    wikipedia_en.zim
    stackoverflow.zim
/cache/                # artículos calientes, respuestas cacheadas (v2)
/index/                # índices Xapian (los genera/usa kiwix-serve)
/config/               # configuración de la app, library.xml de kiwix-serve
/thumbnails/           # miniaturas/faviconos de colección
/library.db            # SQLite: registro de colecciones + notas + favoritos + historial
```

- `/library.db` es **nuestra** capa de gestión (notas, favoritos, etiquetas,
  historial, recientes). NO la toca kiwix-serve.
- En la **versión NimOS nativa**, `/library.db` puede integrarse con el SQLite
  central de NimOS; en la **versión contenedor genérica**, es una DB propia del
  contenedor (cada versión coherente con su entorno).

---

## 3. Concepto de producto — "Colecciones", no "abrir un ZIM"

**Reencuadre clave (donde superamos a Kiwix):** el usuario piensa en
**colecciones** (Wikipedia ES, Arch Wiki, Docker Docs), NUNCA en "archivos .zim".
La *fuente* de una colección es un detalle de implementación oculto.

```
Colección: "Wikipedia ES"
    fuente: zim        ← hoy
Colección: "Mis apuntes"
    fuente: markdown   ← posible mañana
Colección: "Manuales PDF"
    fuente: pdf        ← posible mañana
```

La UI trata todas igual. Esto abre la puerta a que Nimos Library sea un gestor de
conocimiento **general**, no solo un lector ZIM. La abstracción de fuente vive en
el shim; la UI solo conoce "colecciones" con una API uniforme.

---

## 4. Funciones de la UI — "como un navegador moderno"

Kiwix se centra en *leer*. Nimos Library es un **navegador de conocimiento**.

### 4.1. Layout general

```
┌───────────────────────────────────────────────────────────────┐
│ ← → ⟳ 🔍 Buscar cualquier cosa...                 ⭐ ⚙️ 👤      │
│  [ Inicio ✕ ][ Wikipedia: Portada ✕ ][ Saturno ✕ ][ + ]        │ ← PESTAÑAS
├───────────────┬───────────────────────────────────────────────┤
│ 📚 Biblioteca │                                    │  Índice   │
│  Inicio       │   Saturno                          │  Inicio   │
│  Favoritos    │   ────────                         │  General  │
│  Reciente     │   [imagen]                         │  Anillos  │
│  Historial    │                                    │  Lunas    │
│  Descargas    │   Saturno es el sexto planeta...   │  Explor.  │
│  Notas        │                                    │  Véase    │
│  Etiquetas    │   Características generales         │           │
│ ───────────── │   ...                              │ [img dest]│
│ BIBLIOTECAS   │                                    │           │
│  Wikipedia ES │                                    │           │
│  Wikipedia EN │                                    │           │
│  Arch Wiki    │                                    │           │
│  ...          │                                    │           │
│ ───────────── │                                    │           │
│ [████░] 40%   │  ‹ ant.  [vista] [vista] [vista]  sig. ›       │
└───────────────┴───────────────────────────────────────────────┘
```

### 4.2. Funciones v0.1 (mínimo viable)
- [x] **Pestañas** (múltiples, desde el día 1 — nacen con el diseño de estado).
- [x] Navegación: atrás/adelante/recargar/inicio dentro de una pestaña.
- [x] **Sidebar de bibliotecas** con estado (nombre, fecha, tamaño).
- [x] Lector de artículo (renderiza el HTML de kiwix-serve reescrito).
- [x] **Barra de búsqueda con autocompletado** (`/suggest`, JSON).
- [x] Medidor de almacenamiento (real del pool en versión NimOS).

### 4.3. Funciones v1 (producto redondo)
- [ ] Índice lateral del artículo (parseado de los `<h2>/<h3>` del HTML).
- [ ] **Favoritos** (persisten en `/library.db`).
- [ ] **Notas** por artículo (persisten en `/library.db`).
- [ ] **Historial** y **Recientes**.
- [ ] **Etiquetas**.
- [ ] Navegación "artículo anterior/siguiente".
- [ ] Búsqueda full-text con snippets (vía shim parseando `/search?format=xml`).
- [ ] **Descargas de colecciones** (del catálogo Kiwix, gestionadas por NimOS).

### 4.4. Funciones v2 (avanzado — NO en v1)
- [ ] WebSocket para eventos en tiempo real (progreso de descarga/indexado).
      → v1 usa **polling** (patrón operationId, como el async de updates de apps).
- [ ] Sistema de **plugins**.
- [ ] Cache avanzada de artículos calientes.
- [ ] Colecciones multi-fuente (markdown, pdf, html) → engine propio python-libzim.
- [ ] Modo escritorio nativo (Wails/Tauri).

### 4.5. Reto técnico A — reescritura de links (la hace el SHIM)

El HTML dentro de un ZIM tiene enlaces **relativos** (`../A/Otro_articulo`,
imágenes `I/foto.jpg`). Hay que **reescribirlos**. 

**Decisión (v0.2): la reescritura la hace el SHIM, no el frontend.** El shim
devuelve HTML ya "domesticado" en `GET /api/article`:
- Links a otros artículos → reescritos a un esquema que el frontend reconoce para
  routing interno (ej. `data-lib`/`data-path`), SIN recargar la ventana.
- Imágenes/recursos → reescritos a la URL absoluta de contenido del motor
  (passthrough `/content/{ZIM}/...`).

**El frontend solo intercepta los clicks** (delegación de eventos): lee el destino
del `<a>` reescrito y navega por su router (pestaña actual/nueva). No manipula el
HTML.

**Por qué en el shim y no en el frontend:** así **todos los clientes** — UI web,
panel NimOS y la futura app de escritorio — heredan el arreglo gratis, sin
reimplementarlo. Centraliza la lógica frágil en un solo sitio.
> kiwix-serve ya sirve contenido con URLs coherentes bajo `/content/{ZIM}/`, lo
> que simplifica la reescritura respecto a parsear el ZIM crudo.

### 4.6. Reto técnico B — aislamiento de CSS (⛔ SPIKE BLOQUEANTE, ver §10)

El HTML de un artículo **trae su propio CSS** (Wikipedia usa hojas de estilo
agresivas, embebidas en el ZIM). *Cómo* se inyecta ese HTML en la UI es la
decisión más delicada del lector:

| Técnica | Aislamiento CSS | Interceptar clicks | Notas |
|---|---|---|---|
| `innerHTML` en Svelte | ❌ el CSS del ZIM y el de NimOS se pelean en ambas direcciones | ✅ fácil (mismo DOM) | Rompe estética o rompe artículo |
| `<iframe>` | ✅ perfecto | ⚠️ requiere **same-origin** | El same-origin refuerza el shim-como-proxy (todo bajo un origen) |
| Shadow DOM | 🟡 término medio | ✅ | Rarezas propias |

**Esto NO se decide en el documento — se decide con un spike en Fase 1**, y el
criterio son TRES ejes por técnica (no solo CSS), porque **la reescritura de links
(§4.5) y la técnica de render están ACOPLADAS**: si el spike elige iframe, los
links reescritos con `data-lib`/`data-path` viven *dentro* del documento del
iframe → la delegación de eventos del frontend no los ve directamente (haría falta
inyectar un script en el HTML del artículo, o acceso same-origin al DOM del
iframe). Con innerHTML/Shadow DOM el mecanismo de interceptación es otro.

**Criterio del spike — por cada técnica, validar con un artículo real de Wikipedia:**
1. **Aislamiento de CSS** (que no se peleen los estilos en ninguna dirección).
2. **Interceptación de clicks** (que el routing interno funcione con los links
   reescritos por el shim — el eje acoplado que no hay que olvidar).
3. **Scroll y atrás/adelante** dentro de la pestaña (que la navegación se sienta
   nativa).

Elegir por CSS y descubrir *después* que los clicks no se interceptan bien sería
repetir el spike. El resultado condiciona la arquitectura del lector → **bloqueante
antes de construir la UI final.**

---

## 5. Distribución — un desarrollo, cuatro formas de consumo

### 5.1. Panel NimOS (iframe estilo Game Panel) ✅ patrón YA existe

NimOS Beta 9 ya tiene **`WebApp.svelte`**: *"wrapper para apps Docker embebidas
vía iframe, carga iframe hacia el puerto del contenedor con el token de sesión"*,
e incluye botón "**Abrir en pestaña nueva**" (`window.open(iframeSrc, '_blank')`).
Y **`launchApp.js`** ya distingue `openMode: 'internal'` (ventana iframe) vs
`'external'` (pestaña/URL).

→ **Nimos Library se instala desde el AppStore como cualquier app** (entrada de
catálogo + icono en el cajón). Al abrirla, `WebApp.svelte` la muestra en ventana
NimOS vía iframe. El 90% de la fontanería ya está hecha. Reutilización, no invención.

```
Usuario pulsa icono "Nimos Library" en el cajón de apps
        │
        ▼
launchApp.js  →  openMode: 'internal'
        │
        ▼
WebApp.svelte  →  <iframe src="http://<nas>:<puerto-library>">
                   (dentro de WindowFrame · estética NimOS)
        │
        └── botón ↗ "Abrir en pestaña"  →  window.open(url, '_blank')
```

### 5.2. Pestaña de navegador ✅

- Misma web, servida por el contenedor. Accesible por `http://<nas>:<puerto>`.
- Pública opcional vía **Caddy + DuckDNS** (diferenciador NimOS) con HTTPS.
- Funciona en cualquier navegador/dispositivo de la red o de internet.

### 5.3. App de escritorio nativa Linux/Windows (FUTURO — v2)

- **No requiere decisión hoy.** La app de escritorio es una **cáscara** que
  envuelve la misma web:
  - **Wails** (Go + webview) → encaja con el stack Go de NimOS. Recomendado.
  - **Tauri** (Rust + webview) → alternativa, binarios pequeños.
- Resultado: un ejecutable `.exe` / `.AppImage` / `.dmg` con su icono, que abre
  una ventana nativa cargando la web local. "Un navegador de una sola pestaña
  fijada a Nimos Library."
- **Cero rework:** la web Svelte es el núcleo; la app de escritorio se añade sin
  tocar la lógica. Puede apuntar a un kiwix-serve local o remoto.

### 5.4. CLI / API directa

- El shim expone la API REST → automatizable (scripts, otras integraciones NimOS).
- Ej: NimHealth podría reportar "3 colecciones desactualizadas", o un job podría
  descargar la última Wikipedia automáticamente.

### 5.5. Empaquetado Docker

**v0 (validación):** kiwix-serve tal cual en el catálogo NimOS (entrada de
catalog.json + icono), como Downtify/Forgejo/WordPress. Wikipedia offline en
minutos, sin construir nada. Sirve para aprender el comportamiento del motor.

**v1 (producto):** DOS contenedores:
```
Contenedor 1: ghcr.io/kiwix/kiwix-serve:3.8.2   (motor · no lo tocamos)
Contenedor 2: nimos-library-ui                   (shim + UI Svelte · lo construimos)
```
- La UI (SvelteKit → estáticos) + shim en una imagen. Dockerfile propio (~pocas
  líneas: base, copiar build, arrancar). Se explica paso a paso al llegar la fase.
- Ambos en un `compose` (patrón multi-contenedor tipo WordPress que ya hicimos).

---

## 6. Seguridad y autenticación (⛔ BLOQUEANTE · D6)

**El agujero de v0.1:** el diseño contemplaba exposición pública (Caddy+DuckDNS,
§5.2) junto a endpoints `POST` (`/downloads`, `/notes`) **sin autenticación**.
Consecuencias directas:
- Cualquiera en internet podría lanzar `POST /api/downloads` → descargar ZIMs de
  **90 GB** hasta llenar el disco (DoS por almacenamiento).
- `POST /api/notes` / `POST /api/favorites` → las notas personales serían
  **públicas y escribibles** por cualquiera.

**Modelo de seguridad (v0.3) — auth POR CANAL:**

El token de sesión solo existe cuando entras por el **panel NimOS** (lo inyecta
`WebApp.svelte`). La **pestaña de navegador directa** (§5.2) NO pasa por NimOS →
no tiene token. Por tanto la auth se define **por canal de distribución**, no como
una regla única:

| Canal | Contexto | Regla v0.1 |
|---|---|---|
| Panel NimOS (iframe) | Token de sesión NimOS presente | GET y POST validados con el token |
| Pestaña navegador · LAN | Sin token | **GET funcionan** (leer Wikipedia en LAN no es sensible); **POST exigen token** → notas/descargas solo vía panel NimOS o login propio del shim |
| Pestaña navegador · pública (v1) | Sin token | NADA sin auth propia del shim (punto 3) · NO exponer hasta implementarla |

1. **Postura por defecto — v0.1 solo LAN, sin exponer** públicamente hasta que la
   auth pública (punto 3) esté implementada.

2. **Transporte del token (IMPORTANTE):** el token de `WebApp.svelte` viaja por
   **cookie o `postMessage`**, **NUNCA en la URL del iframe** (`?token=...` acaba
   en historiales, logs y referers). El spike de iframes anidados (§4.6/Fase 1)
   verifica esto de paso.

3. **Exposición pública (v1, requisito para abrir a internet):**
   - Auth propia del shim (sesión/API key o login propio) ANTES de permitir Caddy
     público.
   - **Lectura vs escritura separadas:** los `GET` de contenido pueden ser públicos
     conscientemente; los `POST` (descargas, notas, favoritos) **siempre** tras auth.
   - **Rate-limiting** (encaja con NimShield) en TODOS los endpoints con coste:
     `POST /api/downloads` **y** `GET /api/libraries/{id}/fulltext` — este último
     dispara Xapian sobre 90 GB, es un DoS barato incluso **sin** POST.

4. **Passthrough de contenido:** el reverse proxy de `/content/*` debe respetar la
   misma frontera de auth que el canal, o limitarse a lectura pública consciente.

**D6 queda abierta** para el mecanismo exacto de auth pública (sesión NimOS
reutilizada vs login/API key propia del shim), pero las posturas **"v0.1 solo LAN
+ POST exigen token + token nunca en URL"** son firmes y bloqueantes.

---

## 7. API REST del shim (contrato que consume la UI)

> Diseño objetivo. El shim traduce kiwix-serve (HTML/XML/OPDS) → JSON limpio y
> añade la capa de gestión (notas, favoritos, colecciones).

```
# Colecciones
GET  /api/libraries                 → lista de colecciones [{id,name,source,size,date,lang,icon}]
GET  /api/libraries/{id}            → detalle de una colección (+ entrada principal)
GET  /api/libraries/{id}/search?q=  → sugerencias (title index · JSON)  [usa /suggest]
GET  /api/libraries/{id}/fulltext?q=→ full-text con snippets (v1 · parsea /search?format=xml)

# Contenido
GET  /api/article?lib={id}&path=    → HTML del artículo (links reescritos POR EL SHIM, §4.5)
GET  /api/toc?lib={id}&path=        → índice del artículo (h2/h3 extraídos) (v1)
# Imágenes/recursos: NO pasan por el shim. Passthrough directo por reverse proxy:
#   GET /content/{ZIM}/{PATH}  → streaming directo a kiwix-serve (sin tocar bytes)
#   Evita duplicar I/O en la Pi. El shim solo procesa artículos/catálogo.

# Gestión (persistido en /library.db · TODOS requieren auth · ver §6)
GET  /api/recent                    → artículos recientes
GET  /api/history                   → historial
POST /api/history                   → registrar vista de artículo (escritura de historial)
POST /api/favorites                 → añadir/quitar favorito
GET  /api/favorites                 → lista de favoritos
POST /api/notes                     → crear/editar nota
GET  /api/notes                     → lista de notas
GET  /api/tags , POST /api/tags     → etiquetas (v1)

# Descargas (v1)
GET  /api/catalog/remote?q=         → catálogo remoto Kiwix (OPDS→JSON)
POST /api/downloads                 → iniciar descarga de un .zim  → {operationId}
GET  /api/downloads/{opId}          → estado/progreso (polling, patrón NimOS async)

# Sistema
GET  /api/storage                   → uso de almacenamiento (real del pool en NimOS)
GET  /api/health                    → estado del motor y del shim
```

**Nota de coherencia con NimOS:** las descargas usan el **mismo patrón async con
`operationId` + polling** que implementamos para el update de apps (barra por
fases). Nada de WebSocket en v1.

---

## 8. Decisiones abiertas (a cerrar antes de codificar)

- **D1 — Lenguaje del shim:** Go (stack NimOS, solo proxya kiwix-serve por HTTP,
  licencia limpia) **[recomendado v1]** vs Python (permite migrar a python-libzim
  en v2). → *Propuesta: Go en v1.*
- **D2 — DB de notas/favoritos: CERRADA (v0.2).** SQLite **propio** de la app, con
  esquema portable. **NUNCA se comparte el fichero SQLite con NimOS** — dos apps
  escribiendo el mismo SQLite = locks, migraciones acopladas, versiones que
  divergen. Si NimOS necesita datos de la Library (ej. "3 colecciones
  desactualizadas"), **los pide por la API REST** (aplicando el principio rector de
  §2: los clientes hablan con el motor por API, no comparten estado). La versión
  NimOS puede ubicar el fichero en el pool, pero sigue siendo **suyo**, exclusivo.
- **D3 — Búsqueda full-text en v0.1:** ¿solo `/suggest` (JSON, rápido) o también
  full-text parseando XML desde el minuto uno? → *Propuesta: v0.1 solo suggest;
  full-text en v1.*
- **D4 — Descargas de .zim:** ¿las gestiona el shim (integrado, con progreso NimOS)
  o se delega al FileManager de NimOS? → *Propuesta: shim, para progreso unificado.*
- **D5 — Nombre/branding e icono** de la app en el catálogo.
- **D6 — Auth (⛔ BLOQUEANTE, ver §6):** mecanismo de autenticación para exposición
  pública (reutilizar sesión NimOS vs API key propia del shim). **Postura firme e
  innegociable:** v0.1 solo LAN + el shim valida el token de NimOS en todos los
  `POST`. El mecanismo de auth pública se decide antes de abrir a internet, no antes.

---

## 9. Plan por fases

### Fase 0 — Validación (días · sin código nuevo)
- Añadir **kiwix-serve** al catálogo NimOS (entrada catalog.json + icono), como
  Downtify/Forgejo/WordPress.
- Objetivo: Wikipedia offline corriendo en Pi/x86. Aprender el comportamiento del
  motor **usándolo**. Verificar descargas de .zim, almacenamiento, arranque.
- **Checklist de verificación con `curl` real** (todo §1.3 es de documentación, no
  de práctica — esto lo aterriza):
  - `curl 'http://<kiwix>:8080/suggest?content=<ZIM>&term=satur'` → confirmar JSON.
  - `curl 'http://<kiwix>:8080/search?books.name=<ZIM>&pattern=saturno&format=xml'`
    → confirmar que 3.8.2 soporta `format=xml` como se describe.
  - `curl 'http://<kiwix>:8080/content/<ZIM>/A/Saturno'` → ver el HTML crudo y su
    CSS embebido (insumo directo para el spike de §4.6).
  - `curl 'http://<kiwix>:8080/catalog/v2/entries'` → estructura OPDS real.
- Riesgo: nulo. Curro: mínimo (patrón de catálogo ya dominado).

### Fase 1 — Esqueleto (el mockup andando)
- **⛔ SPIKE BLOQUEANTE PRIMERO — render del artículo (§4.6):** renderizar un
  artículo real de Wikipedia con innerHTML / iframe / Shadow DOM y **elegir la
  técnica** validando los TRES ejes (aislamiento CSS + interceptación de clicks +
  scroll/atrás-adelante), porque render y reescritura de links están acoplados.
  Condiciona toda la UI.
- **⛔ Auth mínima (§6):** el shim valida el token de sesión de NimOS en todos los
  endpoints. Sin esto, no se avanza a exposición.
- Shim mínimo (D1): `/api/libraries`, `/api/article` (con links reescritos),
  `/api/libraries/{id}/search` (suggest) + passthrough `/content/*` para imágenes.
- UI Svelte: **pestañas + sidebar de bibliotecas + lector + búsqueda por suggest**.
- Frontend: interceptar clicks en links reescritos por el shim (routing interno).
- Empaquetado: contenedor UI+shim + compose con kiwix-serve.
- Integración NimOS: entrada de catálogo con `openMode: internal` → `WebApp.svelte`.
  **Probar pronto los iframes anidados** (WebApp iframe → artículo iframe si el
  spike elige iframe): focus/scroll dan sorpresas.
- **Verificable:** navegar Wikipedia con la estética del mockup, en ventana NimOS
  y en pestaña, con la técnica de render elegida y auth de token funcionando.

### Fase 2 — Producto redondo
- Favoritos, notas, historial, recientes, etiquetas (`/library.db`).
- Índice de artículo (TOC), navegación anterior/siguiente.
- Full-text con snippets (parseo XML).
- Descargas de colecciones (catálogo remoto + progreso async con operationId).
- Medidor de almacenamiento real del pool.

### Fase 3 — Expansión (opcional/futuro)
- App de escritorio nativa (Wails/Tauri).
- Engine propio python-libzim (si se quiere abstracción multi-fuente pura).
- Colecciones markdown/pdf/html.
- WebSocket, plugins, cache avanzada.

---

## 10. Riesgos y mitigaciones (honestos)

| Riesgo | Impacto | Mitigación |
|---|---|---|
| **⛔ Sin auth + exposición pública** | Cualquiera ordena descargas de 90 GB o lee/escribe notas | §6: v0.1 solo LAN, shim valida token, auth propia antes de Caddy público, rate-limit en downloads |
| **⛔ Aislamiento de CSS del artículo** | El lector puede ser inviable con la técnica equivocada | §4.6: spike bloqueante en Fase 1 (innerHTML vs iframe vs Shadow DOM) con artículo real |
| `/search` da HTML, no JSON | Búsqueda full-text incómoda | v0.1 usa `/suggest` (JSON); full-text vía XML en shim; engine propio en v2 |
| Rendimiento búsqueda en Pi | Lento en ZIMs enormes | suggest es barato; full-text bajo demanda; cache v2 |
| Reescritura de links HTML | Es "la parte que duele" | kiwix-serve ya sirve URLs coherentes bajo /content; interceptar clicks |
| Sobre-ingeniería (WebSocket, plugins) | No terminar nunca | Diseño completo pero build por fases; v1 mínimo, polling no WS |
| Licencia GPL | Confusión legal | Frontera física: kiwix-serve aislado, UI habla HTTP, todo open source |
| Tamaño de .zim (90 GB Wikipedia) | Almacenamiento | Es esperado; medidor de storage; gestión de descargas |

---

## 11. Próximo paso inmediato

**Fase 0:** preparar la entrada de catálogo de **kiwix-serve** para NimOS
(catalog.json + icono), desplegarla, y tener Wikipedia offline corriendo. Ejecutar
la **checklist de `curl`** de §9 para validar la API en la práctica (no en
documentación) y capturar un artículo real con su CSS → insumo del spike.

Antes de la **Fase 1**, cerrar los **dos bloqueantes**:
1. **Spike de aislamiento CSS** (§4.6) — decidir técnica de render con Wikipedia real.
2. **Auth mínima** (§6/D6) — el shim valida el token de NimOS.

Con eso resuelto, arrancar Fase 1 (shim mínimo + UI Svelte con pestañas).

> **Nota de proceso:** la v0.2 incorpora una revisión que cazó los dos bloqueantes
> (auth y aislamiento CSS) ausentes en v0.1. Verificar antes de construir es más
> barato que rehacer después — el mismo principio que aplicamos al código.
