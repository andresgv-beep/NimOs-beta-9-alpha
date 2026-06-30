<script>
  /**
   * CPPermissions · Panel de Control · sección Permisos de apps
   * ─────────────────────────────────────────────────────────────
   * Define qué usuarios pueden usar cada app instalada (contenedores y
   * stacks). En Settings esto era solo un "coming soon"; el backend ya
   * tenía la API completa esperando UI.
   *
   * API (JSON plano):
   *   GET /api/docker/app-permissions
   *     → { users:[{username,role}], apps:[{id,name,type,image}],
   *         appPermissions:{ appId:[usernames] } }
   *   PUT /api/docker/app-permissions/:appId   body { users:[...usernames] }
   *
   * Modelo: los admin siempre tienen acceso a todo (no se listan como
   * conmutables). Para cada app, se marca qué usuarios 'user' pueden usarla.
   */
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import { StatCard } from '$lib/ui';

  let apps = [];
  let users = [];
  let appPermissions = {}; // { appId: [usernames] }
  let loading = true;
  let savingApp = null;
  let msg = '';
  let msgError = false;

  // Solo los usuarios no-admin son conmutables (admin accede a todo).
  $: normalUsers = users.filter((u) => u.role !== 'admin');

  async function load() {
    try {
      const r = await fetch('/api/docker/app-permissions', { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        apps = d.apps || [];
        users = d.users || [];
        appPermissions = d.appPermissions || {};
      }
    } catch {}
    loading = false;
  }

  function hasAccess(appId, username) {
    const list = appPermissions[appId] || [];
    return list.includes(username);
  }

  async function toggleAccess(appId, username) {
    if (savingApp) return;
    const current = appPermissions[appId] || [];
    const next = current.includes(username)
      ? current.filter((u) => u !== username)
      : [...current, username];

    // Optimista
    appPermissions = { ...appPermissions, [appId]: next };
    savingApp = appId;
    msg = '';
    try {
      const r = await fetch('/api/docker/app-permissions/' + encodeURIComponent(appId), {
        method: 'PUT',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ users: next }),
      });
      if (!r.ok) {
        const e = await r.json().catch(() => ({}));
        msg = e.error || 'Error al guardar permisos';
        msgError = true;
        await load(); // revertir al estado real
      }
    } catch {
      msg = 'Error de red';
      msgError = true;
      await load();
    }
    savingApp = null;
  }

  function appInitials(name) {
    return (name || '?').slice(0, 2).toUpperCase();
  }

  onMount(load);
</script>

<div class="cp-perms">
  <!-- Resumen -->
  <div class="cpp-stats">
    <StatCard label="Apps" value={apps.length} variant="ok" tag="instaladas" />
    <StatCard label="Usuarios" value={normalUsers.length} variant="info" tag="conmutables" tagVariant="info" />
  </div>

  {#if msg}
    <div class="cpp-msg" class:error={msgError}>{msg}</div>
  {/if}

  {#if loading}
    <div class="cpp-empty">Cargando permisos…</div>
  {:else if apps.length === 0}
    <div class="cpp-empty">No hay apps instaladas. Instala apps desde el App Store para gestionar sus permisos.</div>
  {:else if normalUsers.length === 0}
    <div class="cpp-empty">Solo hay administradores, que tienen acceso a todas las apps. Crea usuarios normales para asignar permisos.</div>
  {:else}
    <div class="cpp-note">Los administradores acceden a todas las apps. Marca qué usuarios pueden usar cada una.</div>
    <div class="cpp-list">
      {#each apps as app (app.id)}
        <div class="cpp-app" class:saving={savingApp === app.id}>
          <div class="cpp-app-id">
            <div class="cpp-app-icon">{appInitials(app.name)}</div>
            <div class="cpp-app-meta">
              <div class="cpp-app-name">{app.name}</div>
              <div class="cpp-app-type">{app.type === 'stack' ? 'stack' : 'contenedor'}</div>
            </div>
          </div>
          <div class="cpp-users">
            {#each normalUsers as u (u.username)}
              <button
                class="cpp-chip"
                class:on={hasAccess(app.id, u.username)}
                disabled={savingApp === app.id}
                on:click={() => toggleAccess(app.id, u.username)}
              >
                {u.username}
              </button>
            {/each}
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .cp-perms { display: flex; flex-direction: column; gap: 16px; }

  .cpp-stats {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 8px;
  }

  .cpp-msg { font-size: 11px; color: var(--fg-3, #9c9ca4); font-family: var(--font-mono); }
  .cpp-msg.error { color: var(--st-crit, #ff5a5a); }

  .cpp-note {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    font-family: var(--font-mono);
    line-height: 1.5;
  }

  .cpp-list { display: flex; flex-direction: column; gap: 8px; }
  .cpp-app {
    background: var(--bg-card, #15151a);
    border-radius: 8px;
    padding: 14px 16px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    transition: opacity 0.12s;
  }
  .cpp-app.saving { opacity: 0.6; }

  .cpp-app-id { display: flex; align-items: center; gap: 12px; flex-shrink: 0; min-width: 180px; }
  .cpp-app-icon {
    width: 32px;
    height: 32px;
    border-radius: 7px;
    background: rgba(0, 255, 159, 0.08);
    border: 1px solid rgba(0, 255, 159, 0.2);
    color: var(--nim-green, #00ff9f);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 11px;
    font-weight: 600;
    font-family: var(--font-mono);
  }
  .cpp-app-name {
    font-size: 13px;
    color: var(--fg, #f0f0f0);
    font-family: var(--font-mono);
    font-weight: 600;
  }
  .cpp-app-type {
    font-size: 10px;
    color: var(--fg-5, #5a5a62);
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    margin-top: 2px;
  }

  .cpp-users {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    justify-content: flex-end;
    flex: 1;
  }
  .cpp-chip {
    padding: 5px 12px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 5px;
    color: var(--fg-4, #7a7a82);
    font-size: 11px;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: all 0.12s;
  }
  .cpp-chip:hover:not(:disabled) { border-color: var(--bd-3, #2a2a32); color: var(--fg-3, #9c9ca4); }
  .cpp-chip.on {
    color: var(--nim-green, #00ff9f);
    border-color: rgba(0, 255, 159, 0.35);
    background: rgba(0, 255, 159, 0.06);
  }
  .cpp-chip:disabled { cursor: not-allowed; }

  .cpp-empty {
    padding: 24px;
    text-align: center;
    color: var(--fg-5, #5a5a62);
    font-size: 12px;
    font-family: var(--font-mono);
    line-height: 1.5;
  }
</style>
