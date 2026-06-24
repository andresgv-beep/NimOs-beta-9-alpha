# NimOS Beta 9

> **NAS Interactive Machine Operating System** · versión 0.8.1-alpha
> Rewrite frontend con el nuevo lenguaje visual **Terminal v3**

---

## Qué es esto

NimOS Beta 8.1 es un rewrite del **frontend** de NimOS usando un design system nuevo (`v3 Terminal Retro`) manteniendo el mismo daemon Go + mismos endpoints API que Beta 7.

Beta 7 queda congelada como la versión de producción mientras Beta 8.1 se construye en paralelo. Cuando Beta 8.1 alcance paridad funcional, se promociona a versión estable.

## Estado actual

**✅ Listo**
- Design system v3 completo (`src/app.css`)
- Primitivas UI (`src/lib/ui/`): LED, BevelButton, KPICard, DenseTable, Sparkline, CmdOutputLog, SectionHead, Badge, KeyBind, StripeProgressBar, IconButton, TextInput, EmptyState, Spinner, Tab, Footer
- Chrome del OS (`src/lib/components/`): AppShell, WindowFrame, Taskbar, Launcher, NotificationPanel, TransferPanel, Desktop, Login, SetupWizard
- Rutas SvelteKit configuradas
- Stubs de 11 apps que renderizan con AppShell pero muestran "WIP"

**🚧 Pendiente (orden recomendado de migración)**

1. [ ] **NimHealth** — Task Manager + detalle (mockup ya diseñado)
2. [ ] **NimShield** — Panel live + historial (mockup ya diseñado)
3. [ ] **NetworkApp** — Pequeña, buen candidato para coger ritmo
4. [ ] **NimTorrent** — Backend C++ sin tocar, solo UI
5. [ ] **AppStore** — Necesaria para instalar Docker apps
6. [ ] **FileManager** — Mockup retro ya diseñado
7. [ ] **StorageApp** — Mockup retro ya diseñado (+ vertical LED bars)
8. [ ] **NimBackup** — Depende de otras apps
9. [ ] **Settings** — La más grande, al final
10. [ ] **Terminal** — Nueva en Beta 8 · presente en 8.1
11. [ ] **Notes** — Último, no crítica

## Scope Beta 8.1 v1 · decisiones de corte

**Incluido:**
- 11 apps listadas arriba
- Sistema de ventanas, taskbar, launcher, notificaciones, transferencias
- Tema único `retro-v3` con accent color customizable (verde fósforo default)
- Integración con daemon Go de Beta 7 (mismos endpoints)

**Excluido (se revisa para Beta 9):**
- Media Player (se asume Jellyfin vía Docker)
- Virtual Machines (mover a AppStore como instalación opcional)
- NimLink (reevaluar necesidad)
- Widgets de escritorio
- Modos de taskbar (dock, top, left)
- Modo móvil (`MobileApp.svelte`)
- Temas múltiples (midnight/dark/light)

Si alguna de estas necesita volver, añadirla a `src/lib/apps.js`.

---

## Estructura del proyecto

```
nimos-beta-8.1/
├── src/
│   ├── app.css                    ← Design System v3 (tokens, scanlines, animaciones)
│   ├── app.html                   ← Plantilla SvelteKit
│   │
│   ├── routes/
│   │   ├── +layout.svelte         ← Layout global con app.css
│   │   ├── +layout.js             ← prerender + ssr false
│   │   └── +page.svelte           ← Raíz: loading/wizard/login/desktop
│   │
│   └── lib/
│       ├── apps.js                ← Manifiesto de apps (APP_META, helpers)
│       ├── index.js               ← Exportaciones de biblioteca
│       │
│       ├── stores/
│       │   ├── auth.js            ← Sesión, JWT, setup, login (de Beta 7)
│       │   ├── notifications.js   ← Toasts + panel (de Beta 7)
│       │   ├── uploadTasks.js     ← Cola de transferencias (de Beta 7)
│       │   ├── windows.js         ← Ventanas (de Beta 7, sin cambios)
│       │   └── theme.js           ← Prefs (simplificado: solo retro)
│       │
│       ├── ui/                    ← Primitivas v3 (16 componentes)
│       │   ├── LED.svelte
│       │   ├── KeyBind.svelte
│       │   ├── Badge.svelte
│       │   ├── BevelButton.svelte
│       │   ├── IconButton.svelte
│       │   ├── TextInput.svelte
│       │   ├── Sparkline.svelte
│       │   ├── KPICard.svelte
│       │   ├── SectionHead.svelte
│       │   ├── DenseTable.svelte
│       │   ├── StripeProgressBar.svelte
│       │   ├── CmdOutputLog.svelte
│       │   ├── EmptyState.svelte
│       │   ├── Spinner.svelte
│       │   ├── Tab.svelte
│       │   ├── Footer.svelte
│       │   └── index.js
│       │
│       ├── components/            ← Chrome del OS
│       │   ├── AppShell.svelte    ← Envoltorio estándar de apps
│       │   ├── WindowFrame.svelte ← Marco de ventana (drag/resize/maximize)
│       │   ├── Taskbar.svelte     ← Barra inferior con launcher + systray
│       │   ├── Launcher.svelte    ← Popover grid con categorías
│       │   ├── NotificationPanel.svelte  ← Panel de la campana
│       │   ├── TransferPanel.svelte      ← Panel de transferencias
│       │   ├── Desktop.svelte     ← Contenedor raíz
│       │   ├── Login.svelte       ← Pantalla de auth
│       │   └── SetupWizard.svelte ← Primer arranque
│       │
│       └── apps/                  ← STUBS pendientes de migrar
│           ├── FileManager.svelte
│           ├── Settings.svelte
│           ├── StorageApp.svelte
│           ├── NetworkApp.svelte
│           ├── NimTorrent.svelte
│           ├── AppStore.svelte
│           ├── NimBackup.svelte
│           ├── NimHealth.svelte
│           ├── NimShield.svelte
│           ├── Terminal.svelte
│           ├── Notes.svelte
│           └── WebApp.svelte      ← Wrapper iframe para Docker apps
│
├── static/
│   ├── icons/                     ← PNGs 3D de apps (copiados de Beta 7)
│   └── wallpapers/
│
└── scripts/                       ← Instaladores (copiados de Beta 7)
```

---

## Design System v3 · filosofía

**Terminal retro moderno · sysadmin pro**

- **Tipografía**: Inter (UI) + JetBrains Mono (datos, paths, números)
- **Acento**: `#00ff9f` verde fósforo (personalizable vía `prefs.accentColor`)
- **Paleta**: 4 niveles de bg + 4 de fg + 2 de borde (ver `app.css`)
- **Bevel clip-path**: sistema D con tres tamaños (sm 6px, md 10px, lg 12px)
- **Scanlines**: overlay global sutil permanente
- **Sin border-radius** (bordes rectos)
- **Shadow**: hard `4px 4px` (no blur)
- **Feature settings**: `tnum` siempre activo en números tabulares

### Tokens principales (`app.css`)

```css
--bg: #0a0a0a;      --fg: #e8e8e8;
--bg-1: #141414;    --fg-dim: #888;
--bg-2: #1c1c1c;    --fg-mute: #555;
--bg-3: #242424;    --fg-faint: #333;

--border: #2a2a2a;        --border-bright: #3a3a3a;

--accent: #00ff9f;        --accent-dim: rgba(0,255,159,0.12);
--warn: #ffb800;          --crit: #ff5a5a;
--info: #4db8ff;          --magenta: #e873ff;  --orange: #ff8c3f;

--bev-sm: 6px;            --bev-md: 10px;      --bev-lg: 12px;
--taskbar-height: 52px;   --titlebar-height: 32px;
```

---

## Cómo migrar una app de Beta 7

Plantilla mental para cada app:

1. **Abre el archivo original** en `../NimOs-beta-7-main/src/lib/apps/<App>.svelte`
2. **Copia el bloque `<script>`** completo al stub de Beta 8.1. Este bloque tiene toda la lógica: fetch, stores, handlers, estado reactivo. No se toca.
3. **Reescribe el template HTML** usando:
   - `AppShell` para el chrome
   - Primitivas `$lib/ui` en vez de CSS ad-hoc (BevelButton en vez de `.btn`, KPICard en vez de cards custom, DenseTable en vez de `.file-grid`, etc.)
   - SectionHead para títulos de sección
4. **Elimina el bloque `<style>`** del componente. El design system v3 provee todo vía primitivas + tokens globales.
5. **Comprueba que los endpoints API siguen los mismos** que en Beta 7. El daemon Go no cambia.
6. **Verifica mockups** si existen en la conversación de diseño (muchas apps tienen mockup retro ya validado).

### Ejemplo mínimo

```svelte
<script>
  // ⬇️ Copiar tal cual del original
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';

  let services = [];
  onMount(async () => {
    const r = await fetch('/api/services', { headers: hdrs() });
    services = (await r.json()).services || [];
  });
</script>

<!-- ⬇️ Reescribir con primitivas v3 -->
<script>
  import AppShell from '$lib/components/AppShell.svelte';
  import { DenseTable, LED, SectionHead } from '$lib/ui';
</script>

<AppShell title="Services" headerIcon="⎈" pathSegments={['services']}>
  <div style="padding:16px 20px">
    <SectionHead count="· {services.length}">Services</SectionHead>
    <DenseTable columns="40px 1fr 100px" headers={[{label:'#'},{label:'Name'},{label:'Status'}]}>
      {#each services as s, i}
        <div class="tr-row">
          <div class="tr-ln">{String(i+1).padStart(2,'0')}</div>
          <div>{s.name}</div>
          <div><LED variant={s.status === 'running' ? 'ok' : 'off'} size={6} /> {s.status}</div>
        </div>
      {/each}
    </DenseTable>
  </div>
</AppShell>
```

---

## Dev

```bash
npm install
npm run dev         # arranca en localhost:5173 con proxy a daemon en :5000
npm run build       # genera /dist para producción
```

El daemon Go se arranca desde Beta 7 normalmente; Beta 8.1 solo sirve el frontend.

---

## Qué se puede borrar si no lo necesitas

Estos archivos/carpetas están en el repo pero son opcionales según tu flujo:

- `scripts/` — si usas otra forma de deploy distinta al `install.sh`
- `static/wallpapers/*` — si prefieres gestionarlos por otro lado
- Stubs de `src/lib/apps/*.svelte` que no vayas a usar en Beta 8.1 v1 (ej. si decides posponer Notes o Terminal, borra sus stubs)

---

## Créditos

Rewrite diseñado y construido por Andrés con Claude (Opus) como co-developer de arquitectura, refinando el lenguaje visual iterativamente a través de mockups HTML antes de llevar a código Svelte.

Base heredada: NimOS Beta 7 (Go daemon + SvelteKit + Design System v2).
