<script>
  /**
   * Permisos de apps · matriz unificada.
   *
   * Las apps propias de NimOS persisten sus concesiones en user_app_access.
   * Las apps instaladas persisten en docker.json porque el proxy también
   * consume esa lista. Esta vista presenta ambos dominios como uno solo.
   */
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';
  import { StatCard } from '$lib/ui';

  let systemApps = [];
  let dockerApps = [];
  let users = [];
  let grants = [];
  let dockerPermissions = {};
  let loading = true;
  let savingKey = '';
  let error = '';

  $: normalUsers = users.filter((entry) => entry.role !== 'admin');
  $: configurableCount = systemApps.filter((app) => !app.adminOnly && !app.public).length + dockerApps.length;

  async function requestJSON(url, options = {}) {
    const response = await fetch(url, { ...options, headers: { ...hdrs(), ...(options.headers || {}) } });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error || 'No se pudieron cargar los permisos');
    return data;
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const [userData, appData, grantData, dockerData] = await Promise.all([
        requestJSON('/api/users'),
        requestJSON('/api/app-access/apps'),
        requestJSON('/api/app-access'),
        requestJSON('/api/docker/app-permissions'),
      ]);
      users = Array.isArray(userData) ? userData : userData.users || [];
      systemApps = appData.apps || [];
      grants = grantData.grants || [];
      dockerApps = dockerData.apps || [];
      dockerPermissions = dockerData.appPermissions || {};
    } catch (err) {
      error = err.message || 'No se pudieron cargar los permisos';
    } finally {
      loading = false;
    }
  }

  function hasSystemAccess(appId, username) {
    return grants.some((grant) => grant.appId === appId && grant.username === username);
  }

  function hasDockerAccess(appId, username) {
    return (dockerPermissions[appId] || []).includes(username);
  }

  async function toggleSystemAccess(appId, username) {
    const key = `system:${appId}:${username}`;
    if (savingKey) return;
    const enabled = hasSystemAccess(appId, username);
    const previous = grants;
    grants = enabled
      ? grants.filter((grant) => !(grant.appId === appId && grant.username === username))
      : [...grants, { appId, username, permission: 'use' }];
    savingKey = key;
    error = '';
    try {
      await requestJSON('/api/app-access', {
        method: enabled ? 'DELETE' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, appId, permission: 'use' }),
      });
    } catch (err) {
      grants = previous;
      error = err.message || 'No se pudo guardar el permiso';
    } finally {
      savingKey = '';
    }
  }

  async function toggleDockerAccess(appId, username) {
    const key = `docker:${appId}:${username}`;
    if (savingKey) return;
    const current = dockerPermissions[appId] || [];
    const next = current.includes(username)
      ? current.filter((entry) => entry !== username)
      : [...current, username];
    const previous = dockerPermissions;
    dockerPermissions = { ...dockerPermissions, [appId]: next };
    savingKey = key;
    error = '';
    try {
      await requestJSON('/api/docker/app-permissions/' + encodeURIComponent(appId), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ users: next }),
      });
    } catch (err) {
      dockerPermissions = previous;
      error = err.message || 'No se pudo guardar el permiso';
    } finally {
      savingKey = '';
    }
  }

  function initials(name) {
    return (name || '?').trim().slice(0, 2).toUpperCase();
  }

  function categoryLabel(category) {
    return category === 'system' ? 'Sistema' : 'Aplicación';
  }

  onMount(load);
</script>

<div class="cp-perms">
  <div class="cpp-stats">
    <StatCard label="Apps registradas" value={systemApps.length + dockerApps.length} variant="ok" tag="en NimOS" />
    <StatCard label="Permisos configurables" value={configurableCount} variant="info" tag="por usuario" tagVariant="info" />
  </div>

  <div class="cpp-intro">
    <div>
      <div class="cpp-intro-title">Acceso de usuarios</div>
      <div class="cpp-intro-text">Los administradores siempre tienen acceso completo. Activa las apps que podrá abrir cada usuario estándar.</div>
    </div>
    <div class="cpp-user-count">{normalUsers.length} usuario{normalUsers.length === 1 ? '' : 's'} estándar</div>
  </div>

  {#if error}
    <div class="cpp-alert">
      <span>{error}</span>
      <button on:click={load}>Reintentar</button>
    </div>
  {/if}

  {#if loading}
    <div class="cpp-empty">Cargando permisos…</div>
  {:else}
    <section class="cpp-section">
      <div class="cpp-section-head">
        <div>
          <h3>Apps de NimOS</h3>
          <p>Herramientas integradas en el escritorio y servicios del sistema.</p>
        </div>
        <span>{systemApps.length}</span>
      </div>

      <div class="cpp-list">
        {#each systemApps as app (app.id)}
          <div class="cpp-app">
            <div class="cpp-app-id">
              <div class="cpp-app-icon">{initials(app.name)}</div>
              <div class="cpp-app-meta">
                <div class="cpp-app-name">{app.name}</div>
                <div class="cpp-app-type">{categoryLabel(app.category)}</div>
              </div>
            </div>

            <div class="cpp-access">
              {#if app.adminOnly}
                <span class="cpp-policy restricted">Solo administradores</span>
              {:else if app.public}
                <span class="cpp-policy public">Todos los usuarios</span>
              {:else if normalUsers.length === 0}
                <span class="cpp-no-users">Crea un usuario estándar para asignar acceso</span>
              {:else}
                {#each normalUsers as account (account.username)}
                  <button
                    class="cpp-user"
                    class:on={hasSystemAccess(app.id, account.username)}
                    aria-pressed={hasSystemAccess(app.id, account.username)}
                    disabled={savingKey !== ''}
                    on:click={() => toggleSystemAccess(app.id, account.username)}
                  >
                    <span class="cpp-user-check"></span>
                    {account.username}
                  </button>
                {/each}
              {/if}
            </div>
          </div>
        {/each}
      </div>
    </section>

    <section class="cpp-section">
      <div class="cpp-section-head">
        <div>
          <h3>Apps instaladas</h3>
          <p>Contenedores y stacks instalados desde App Store.</p>
        </div>
        <span>{dockerApps.length}</span>
      </div>

      {#if dockerApps.length === 0}
        <div class="cpp-empty compact">Todavía no hay apps instaladas.</div>
      {:else}
        <div class="cpp-list">
          {#each dockerApps as app (app.id)}
            <div class="cpp-app">
              <div class="cpp-app-id">
                <div class="cpp-app-icon installed">{initials(app.name)}</div>
                <div class="cpp-app-meta">
                  <div class="cpp-app-name">{app.name}</div>
                  <div class="cpp-app-type">{app.type === 'stack' ? 'Stack' : 'Contenedor'}</div>
                </div>
              </div>

              <div class="cpp-access">
                {#if normalUsers.length === 0}
                  <span class="cpp-no-users">Crea un usuario estándar para asignar acceso</span>
                {:else}
                  {#each normalUsers as account (account.username)}
                    <button
                      class="cpp-user"
                      class:on={hasDockerAccess(app.id, account.username)}
                      aria-pressed={hasDockerAccess(app.id, account.username)}
                      disabled={savingKey !== ''}
                      on:click={() => toggleDockerAccess(app.id, account.username)}
                    >
                      <span class="cpp-user-check"></span>
                      {account.username}
                    </button>
                  {/each}
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</div>

<style>
  .cp-perms { display: flex; flex-direction: column; gap: 18px; }
  .cpp-stats { display: grid; grid-template-columns: repeat(2, 1fr); gap: 8px; }

  .cpp-intro {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
    padding: 14px 16px;
    background: var(--bg-card);
    border: 1px solid var(--line);
    border-radius: 8px;
  }
  .cpp-intro-title { color: var(--ink); font-size: 13px; font-weight: 650; }
  .cpp-intro-text { color: var(--ink-mute); font-size: 12px; line-height: 1.5; margin-top: 3px; }
  .cpp-user-count { color: var(--ink-dim); font-size: 11px; white-space: nowrap; }

  .cpp-alert {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 12px;
    color: var(--crit);
    background: rgba(248, 113, 113, 0.06);
    border-left: 3px solid var(--crit);
    border-radius: 4px;
    font-size: 12px;
  }
  .cpp-alert button { background: transparent; border: 0; color: var(--ink); cursor: pointer; font: inherit; }

  .cpp-section { display: flex; flex-direction: column; gap: 10px; }
  .cpp-section-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; }
  .cpp-section-head h3 { margin: 0; color: var(--ink); font-size: 14px; font-weight: 650; }
  .cpp-section-head p { margin: 3px 0 0; color: var(--ink-mute); font-size: 11px; }
  .cpp-section-head > span { color: var(--ink-mute); font-size: 11px; }

  .cpp-list { border: 1px solid var(--line); border-radius: 8px; overflow: hidden; background: var(--bg-card); }
  .cpp-app {
    min-height: 62px;
    padding: 12px 14px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
  }
  .cpp-app + .cpp-app { border-top: 1px solid var(--line); }
  .cpp-app-id { display: flex; align-items: center; gap: 11px; min-width: 210px; }
  .cpp-app-icon {
    width: 34px;
    height: 34px;
    border-radius: 6px;
    background: var(--bg-inner);
    border: 1px solid var(--line);
    color: var(--ink-dim);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 10px;
    font-weight: 700;
  }
  .cpp-app-icon.installed { color: var(--signal); background: rgba(91, 143, 249, 0.08); }
  .cpp-app-name { color: var(--ink); font-size: 13px; font-weight: 600; }
  .cpp-app-type { color: var(--ink-mute); font-size: 11px; margin-top: 2px; }

  .cpp-access { display: flex; flex-wrap: wrap; justify-content: flex-end; align-items: center; gap: 6px; flex: 1; }
  .cpp-user {
    min-width: 94px;
    padding: 7px 10px;
    display: inline-flex;
    align-items: center;
    gap: 7px;
    background: var(--bg-inner);
    border: 1px solid var(--line);
    border-radius: 5px;
    color: var(--ink-mute);
    font-family: var(--font-sans);
    font-size: 11px;
    cursor: pointer;
  }
  .cpp-user:hover:not(:disabled) { color: var(--ink); border-color: var(--line-bright); }
  .cpp-user.on { color: var(--signal); border-color: rgba(91, 143, 249, 0.42); background: rgba(91, 143, 249, 0.08); }
  .cpp-user:disabled { cursor: wait; opacity: 0.58; }
  .cpp-user-check { width: 7px; height: 7px; border-radius: 2px; background: var(--ink-trace); }
  .cpp-user.on .cpp-user-check { background: var(--signal); }

  .cpp-policy { padding: 5px 9px; border-radius: 4px; font-size: 11px; border: 1px solid var(--line); }
  .cpp-policy.restricted { color: var(--ink-mute); }
  .cpp-policy.public { color: var(--signal); border-color: rgba(91, 143, 249, 0.28); background: rgba(91, 143, 249, 0.06); }
  .cpp-no-users { color: var(--ink-trace); font-size: 11px; }
  .cpp-empty { padding: 32px 20px; text-align: center; color: var(--ink-mute); font-size: 12px; border: 1px solid var(--line); border-radius: 8px; }
  .cpp-empty.compact { padding: 22px; background: var(--bg-card); }

  @media (max-width: 760px) {
    .cpp-stats { grid-template-columns: 1fr; }
    .cpp-intro, .cpp-app { align-items: flex-start; flex-direction: column; }
    .cpp-user-count { white-space: normal; }
    .cpp-access { justify-content: flex-start; width: 100%; }
  }
</style>
