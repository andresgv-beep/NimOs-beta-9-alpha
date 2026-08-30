<script>
  /**
   * CPUsers · Panel de Control · sección Usuarios
   * ───────────────────────────────────────────────
   * Gestión de cuentas del sistema: listar, crear, editar, eliminar.
   * Migrado desde Settings (sección 'users') al lenguaje visual v3.
   *
   * API:
   *   GET    /api/users
   *   POST   /api/users                  { username, password, role }
   *   PUT    /api/users/:username         { role, password? }
   *   DELETE /api/users/:username
   */
  import { onMount } from 'svelte';
  import { user, hdrs } from '$lib/stores/auth.js';
  import { DataTable, StatCard } from '$lib/ui';
  import WizardFrame from '$lib/ui/WizardFrame.svelte';

  let usersList = [];
  let editingUser = null;
  let userMsg = '';
  let userMsgError = false;
  let savingUser = false;
  let loading = true;

  async function loadUsers() {
    try {
      const r = await fetch('/api/users', { headers: hdrs() });
      if (r.ok) usersList = await r.json();
    } catch {}
    loading = false;
  }

  function startNewUser() {
    editingUser = { username: '', password: '', role: 'user', isNew: true };
    userMsg = '';
    userMsgError = false;
  }

  function startEditUser(u) {
    editingUser = { ...u, password: '', isNew: false };
    userMsg = '';
    userMsgError = false;
  }

  async function saveUser() {
    if (savingUser) return;
    savingUser = true;
    userMsg = '';
    try {
      const url = editingUser.isNew
        ? '/api/users'
        : '/api/users/' + encodeURIComponent(editingUser.username);
      const method = editingUser.isNew ? 'POST' : 'PUT';
      const body = { username: editingUser.username, role: editingUser.role };
      if (editingUser.password) body.password = editingUser.password;
      const r = await fetch(url, {
        method,
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (r.ok) {
        editingUser = null;
        await loadUsers();
      } else {
        const e = await r.json().catch(() => ({}));
        userMsg = e.error || 'Error al guardar';
        userMsgError = true;
      }
    } catch {
      userMsg = 'Error de red';
      userMsgError = true;
    }
    savingUser = false;
  }

  async function deleteUser(username) {
    if (!confirm(`¿Eliminar usuario "${username}"?`)) return;
    try {
      await fetch('/api/users/' + encodeURIComponent(username), {
        method: 'DELETE',
        headers: hdrs(),
      });
      await loadUsers();
    } catch {}
  }

  $: adminCount = usersList.filter((u) => u.role === 'admin').length;

  onMount(loadUsers);
</script>

<div class="cp-users">
  <!-- Resumen -->
  <div class="cpu-stats">
    <StatCard label="Usuarios" value={usersList.length} variant="ok" tag="cuentas" />
    <StatCard label="Administradores" value={adminCount} variant="info" tag="del panel" tagVariant="info" />
  </div>

  <!-- La lista permanece visible mientras el editor se abre como diálogo. -->
  {#if loading}
      <div class="cpu-empty">Cargando usuarios…</div>
  {:else}
      <DataTable cols="36px 1fr 90px 80px" headers={['', 'Usuario', 'Rol', '>Acciones']}>
        {#each usersList as u (u.username)}
          <div class="dt-row">
            <span class="cpu-avatar">{(u.username || '?')[0].toUpperCase()}</span>
            <span class="cpu-name">{u.username}</span>
            <span>
              <span class="cpu-badge" class:admin={u.role === 'admin'}>
                {u.role === 'admin' ? 'Administrador' : 'Usuario'}
              </span>
            </span>
            <div class="cpu-row-actions">
              <button class="cpu-icon" on:click={() => startEditUser(u)} title="Editar">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4z"/>
                </svg>
              </button>
              {#if u.username !== $user?.username}
                <button class="cpu-icon danger" on:click={() => deleteUser(u.username)} title="Eliminar">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="3 6 5 6 21 6"/>
                    <path d="M19 6l-1 14H6L5 6"/>
                    <path d="M10 11v6M14 11v6"/>
                  </svg>
                </button>
              {/if}
            </div>
          </div>
        {/each}
      </DataTable>

      <button class="cpu-btn primary cpu-add" on:click={startNewUser}>+ Nuevo usuario</button>
  {/if}
</div>

{#if editingUser}
  <WizardFrame
    open={true}
    title={editingUser.isNew ? 'Crear usuario' : `Editar usuario · ${editingUser.username}`}
    currentStep={1}
    totalSteps={1}
    canAdvance={!savingUser}
    canGoBack={false}
    nextLabel={savingUser ? 'Guardando…' : editingUser.isNew ? 'Crear usuario' : 'Guardar cambios'}
    width={520}
    on:next={saveUser}
    on:cancel={() => editingUser = null}
  >
    <div class="cpu-form">
      <p class="cpu-form-intro">
        {editingUser.isNew
          ? 'Crea una cuenta para NimOS y SMB. Después podrás darle acceso a cada carpeta compartida.'
          : 'Actualiza esta cuenta. La contraseña se aplica también a SMB; déjala vacía para conservar la actual.'}
      </p>

      <div class="cpu-field">
        <label class="cpu-label" for="cpu-user">Usuario</label>
        <input
          id="cpu-user"
          type="text"
          class="cpu-input"
          bind:value={editingUser.username}
          disabled={!editingUser.isNew}
          placeholder="Nombre de usuario"
        />
      </div>

      <div class="cpu-field">
        <label class="cpu-label" for="cpu-pass">
          Contraseña {editingUser.isNew ? '' : '(opcional)'}
        </label>
        <input
          id="cpu-pass"
          type="password"
          class="cpu-input"
          bind:value={editingUser.password}
          placeholder="••••••••"
          autocomplete="new-password"
        />
      </div>

      <div class="cpu-field">
        <span class="cpu-label">Rol</span>
        <div class="cpu-roles">
          <button class="cpu-role" class:active={editingUser.role === 'user'} on:click={() => editingUser.role = 'user'}>
            <span class="cpu-role-name">Usuario</span>
            <span class="cpu-role-desc">Acceso estándar al panel</span>
          </button>
          <button class="cpu-role" class:active={editingUser.role === 'admin'} on:click={() => editingUser.role = 'admin'}>
            <span class="cpu-role-name">Administrador</span>
            <span class="cpu-role-desc">Administra NimOS; las carpetas se autorizan aparte</span>
          </button>
        </div>
      </div>

      {#if userMsg}
        <div class="cpu-msg" class:error={userMsgError}>{userMsg}</div>
      {/if}
    </div>
  </WizardFrame>
{/if}

<style>
  .cp-users { display: flex; flex-direction: column; gap: 16px; }

  .cpu-stats {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 8px;
  }

  /* Lista */
  .cpu-avatar {
    width: 24px;
    height: 24px;
    border-radius: 4px;
    background: rgba(91, 143, 249, 0.12);
    color: var(--signal, #5b8ff9);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 11px;
    font-weight: 600;
    font-family: var(--font-sans);
  }
  .cpu-name {
    color: var(--fg, #f0f0f0);
    font-size: 12px;
    font-family: var(--font-sans);
  }
  .cpu-badge {
    font-size: 10px;
    font-family: var(--font-sans);
    font-weight: 600;
    padding: 2px 7px;
    border-radius: 3px;
    border: 1px solid var(--bd-2, #20202a);
    color: var(--fg-4, #7a7a82);
  }
  .cpu-badge.admin {
    color: var(--signal, #5b8ff9);
    border-color: rgba(91, 143, 249, 0.35);
    background: rgba(91, 143, 249, 0.08);
  }
  .cpu-row-actions { display: flex; gap: 4px; justify-content: flex-end; }
  .cpu-icon {
    width: 26px;
    height: 26px;
    background: transparent;
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 4px;
    color: var(--fg-3, #9c9ca4);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0;
  }
  .cpu-icon:hover { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .cpu-icon.danger:hover { color: var(--st-crit, #ff5a5a); border-color: rgba(255, 90, 90, 0.3); }
  .cpu-icon svg { width: 12px; height: 12px; pointer-events: none; }

  /* Formulario */
  .cpu-form {
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .cpu-form-intro {
    margin: 0;
    color: var(--ink-dim);
    font-size: 13px;
    line-height: 1.55;
  }
  .cpu-field { display: flex; flex-direction: column; gap: 6px; }
  .cpu-label {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    font-family: var(--font-sans);
    font-weight: 600;
  }
  .cpu-input {
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 4px;
    padding: 9px 12px;
    color: var(--fg, #f0f0f0);
    font-size: 13px;
    font-family: var(--font-sans);
    outline: none;
  }
  .cpu-input:focus { border-color: rgba(91, 143, 249, 0.55); }
  .cpu-input:disabled { opacity: 0.5; }

  .cpu-roles { display: grid; grid-template-columns: repeat(2, 1fr); gap: 8px; }
  .cpu-role {
    padding: 11px 12px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 4px;
    color: var(--fg-3, #9c9ca4);
    font-size: 12px;
    font-family: var(--font-sans);
    cursor: pointer;
    text-align: left;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .cpu-role.active {
    color: var(--signal, #5b8ff9);
    border-color: rgba(91, 143, 249, 0.4);
    background: rgba(91, 143, 249, 0.09);
  }
  .cpu-role-name { color: var(--ink); font-weight: 600; }
  .cpu-role.active .cpu-role-name { color: var(--signal, #5b8ff9); }
  .cpu-role-desc { color: var(--ink-mute); font-size: 11px; }

  .cpu-msg {
    font-size: 11px;
    color: var(--fg-3, #9c9ca4);
    font-family: var(--font-sans);
  }
  .cpu-msg.error { color: var(--st-crit, #ff5a5a); }

  .cpu-btn {
    padding: 9px 16px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 4px;
    color: var(--fg-3, #9c9ca4);
    font-size: 12px;
    font-family: var(--font-sans);
    cursor: pointer;
    transition: all 0.12s;
  }
  .cpu-btn:hover:not(:disabled) { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .cpu-btn.primary {
    background: var(--signal, #5b8ff9);
    border-color: var(--signal, #5b8ff9);
    color: white;
    font-weight: 600;
  }
  .cpu-btn.primary:hover:not(:disabled) { filter: brightness(1.08); }
  .cpu-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .cpu-add { align-self: flex-start; }

  .cpu-empty {
    padding: 24px;
    text-align: center;
    color: var(--fg-5, #5a5a62);
    font-size: 12px;
    font-family: var(--font-sans);
  }
</style>
