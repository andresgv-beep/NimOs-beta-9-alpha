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
  }

  function startEditUser(u) {
    editingUser = { ...u, password: '', isNew: false };
    userMsg = '';
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
    <StatCard label="Administradores" value={adminCount} variant="info" tag="con acceso total" tagVariant="info" />
  </div>

  {#if editingUser}
    <!-- Formulario crear/editar -->
    <div class="cpu-form">
      <div class="cpu-form-title">
        {editingUser.isNew ? 'Nuevo usuario' : `Editar · ${editingUser.username}`}
      </div>

      <div class="cpu-field">
        <label class="cpu-label" for="cpu-user">Usuario</label>
        <input
          id="cpu-user"
          type="text"
          class="cpu-input"
          bind:value={editingUser.username}
          disabled={!editingUser.isNew}
          placeholder="nombre de usuario"
        />
      </div>

      <div class="cpu-field">
        <label class="cpu-label" for="cpu-pass">
          Contraseña {editingUser.isNew ? '' : '· (en blanco = no cambiar)'}
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
            Usuario
          </button>
          <button class="cpu-role" class:active={editingUser.role === 'admin'} on:click={() => editingUser.role = 'admin'}>
            Admin
          </button>
        </div>
      </div>

      {#if userMsg}
        <div class="cpu-msg" class:error={userMsgError}>{userMsg}</div>
      {/if}

      <div class="cpu-actions">
        <button class="cpu-btn primary" on:click={saveUser} disabled={savingUser}>
          {savingUser ? 'Guardando…' : 'Guardar'}
        </button>
        <button class="cpu-btn" on:click={() => editingUser = null}>Cancelar</button>
      </div>
    </div>
  {:else}
    <!-- Lista de usuarios -->
    {#if loading}
      <div class="cpu-empty">Cargando usuarios…</div>
    {:else}
      <DataTable cols="36px 1fr 90px 80px" headers={['', 'Usuario', 'Rol', '>Acciones']}>
        {#each usersList as u (u.username)}
          <div class="dt-row">
            <span class="cpu-avatar">{(u.username || '?')[0].toUpperCase()}</span>
            <span class="cpu-name">{u.username}</span>
            <span>
              <span class="cpu-badge" class:admin={u.role === 'admin'}>{u.role || 'user'}</span>
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
  {/if}
</div>

<style>
  .cp-users { display: flex; flex-direction: column; gap: 16px; max-width: 760px; }

  .cpu-stats {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 8px;
  }

  /* Lista */
  .cpu-avatar {
    width: 24px;
    height: 24px;
    border-radius: 6px;
    background: rgba(0, 255, 159, 0.1);
    color: var(--nim-green, #00ff9f);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 11px;
    font-weight: 600;
    font-family: var(--font-mono);
  }
  .cpu-name {
    color: var(--fg, #f0f0f0);
    font-size: 12px;
    font-family: var(--font-mono);
  }
  .cpu-badge {
    font-size: 9px;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    padding: 2px 7px;
    border-radius: 3px;
    border: 1px solid var(--bd-2, #20202a);
    color: var(--fg-4, #7a7a82);
  }
  .cpu-badge.admin {
    color: var(--nim-green, #00ff9f);
    border-color: rgba(0, 255, 159, 0.3);
    background: rgba(0, 255, 159, 0.06);
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
    background: var(--bg-card, #15151a);
    border-radius: 8px;
    padding: 18px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .cpu-form-title {
    font-size: 13px;
    color: var(--fg, #f0f0f0);
    font-family: var(--font-mono);
    font-weight: 600;
  }
  .cpu-field { display: flex; flex-direction: column; gap: 6px; }
  .cpu-label {
    font-size: 10px;
    color: var(--fg-4, #7a7a82);
    text-transform: uppercase;
    letter-spacing: 0.6px;
    font-family: var(--font-mono);
  }
  .cpu-input {
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    padding: 9px 12px;
    color: var(--fg, #f0f0f0);
    font-size: 13px;
    font-family: var(--font-mono);
    outline: none;
  }
  .cpu-input:focus { border-color: rgba(0, 255, 159, 0.35); }
  .cpu-input:disabled { opacity: 0.5; }

  .cpu-roles { display: flex; gap: 6px; }
  .cpu-role {
    flex: 1;
    padding: 8px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    color: var(--fg-3, #9c9ca4);
    font-size: 11px;
    font-family: var(--font-mono);
    cursor: pointer;
  }
  .cpu-role.active {
    color: var(--nim-green, #00ff9f);
    border-color: rgba(0, 255, 159, 0.35);
    background: rgba(0, 255, 159, 0.06);
  }

  .cpu-msg {
    font-size: 11px;
    color: var(--fg-3, #9c9ca4);
    font-family: var(--font-mono);
  }
  .cpu-msg.error { color: var(--st-crit, #ff5a5a); }

  .cpu-actions { display: flex; gap: 8px; }
  .cpu-btn {
    padding: 9px 16px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    color: var(--fg-3, #9c9ca4);
    font-size: 12px;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: all 0.12s;
  }
  .cpu-btn:hover:not(:disabled) { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .cpu-btn.primary {
    background: var(--nim-green, #00ff9f);
    border-color: var(--nim-green, #00ff9f);
    color: var(--bg-window, #16161a);
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
    font-family: var(--font-mono);
  }
</style>
