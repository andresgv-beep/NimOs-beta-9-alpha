<script>
  /**
   * NetworkDDNS · Sub-componente del tab DDNS de NetworkApp
   * ─────────────────────────────────────────────────────────
   * Consume exclusivamente Network v4:
   *   GET    /api/v4/network/ddns           — listar
   *   POST   /api/v4/network/ddns           — crear
   *   PUT    /api/v4/network/ddns/:id       — modificar enabled/auto_update/update_interval
   *   DELETE /api/v4/network/ddns/:id       — borrar (con ?delete_secret=true)
   *   POST   /api/v4/network/ddns/:id/token — rotar token
   *
   * Solo DuckDNS por ahora. Cuando el backend implemente más providers
   * (noip, dynu, freedns, cloudflare), se añaden al PROVIDERS array.
   *
   * Fases visuales:
   *   - active        : ya hay un DDNS configurado, mostrar estado
   *   - empty         : no hay DDNS, mostrar empty state con CTA
   *   - select        : selector de proveedor (paso 1)
   *   - form          : formulario del provider seleccionado (paso 2)
   *
   * El backend NO devuelve el token (HasToken bool) — solo se envía al
   * crear o rotar. La UI nunca lo muestra ni lo solicita en GETs.
   */

  import { onMount, onDestroy } from 'svelte';
  import { token } from '$lib/stores/auth.js';
  import { Spinner } from '$lib/ui';

  // ─── Props ───
  // (ninguna por ahora — el componente es autónomo y consume su propia API)

  // ─── State ───
  let loading = true;
  let entry = null;             // ddnsView del primero/único DDNS, o null si no hay
  let phase = 'empty';          // 'active' | 'empty' | 'select' | 'form'

  // Form state
  let form = { provider: 'duckdns', domain: '', token: '' };
  let tokenVisible = false;
  let saving = false;
  let msg = '';
  let msgError = false;
  let editing = false;          // true = modal en modo editar; false = añadir
  let updatingNow = false;      // true mientras se fuerza una actualización

  // Poll
  let pollTimer = null;
  const POLL_INTERVAL_MS = 10000;  // 10s; el reconciler en backend va a 60s

  // Providers UI — añadir aquí solo cuando exista implementación backend.
  // Cada provider declara qué campos pide en su formulario.
  const PROVIDERS = [
    {
      id: 'duckdns',
      name: 'DuckDNS',
      desc: 'Gratis · token único',
      fields: 'subdominio + token'
    }
    // Cuando backend añada noip/dynu/freedns/cloudflare, descomentar aquí
    // y añadir su rama en el formulario abajo.
  ];

  // ─── Lifecycle ───
  onMount(() => {
    refresh();
    pollTimer = setInterval(refresh, POLL_INTERVAL_MS);
  });

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
  });

  // ─── API helpers ───
  function hdrs() {
    return $token ? { Authorization: `Bearer ${$token}` } : {};
  }

  function jsonHdrs() {
    return { ...hdrs(), 'Content-Type': 'application/json' };
  }

  async function refresh() {
    try {
      const r = await fetch('/api/v4/network/ddns', { headers: hdrs() });
      if (!r.ok) {
        // 401/503 — no rompemos UI, simplemente quedamos vacíos
        loading = false;
        return;
      }
      const data = await r.json();
      const list = data.ddns || [];
      // NimOS gestiona un solo dominio DDNS principal (visión actual).
      // Si en futuro hay multi-domain, esto se hace lista.
      entry = list.length > 0 ? list[0] : null;
      phase = entry ? 'active' : (phase === 'select' || phase === 'form' ? phase : 'empty');
    } catch (e) {
      // Network error — silencioso, próximo poll lo arregla
    } finally {
      loading = false;
    }
  }

  // ─── Actions ───

  function startAdd() {
    msg = '';
    msgError = false;
    editing = false;
    form = { provider: 'duckdns', domain: '', token: '' };
    phase = 'form';
  }

  function startEdit() {
    if (!entry) return;
    msg = '';
    msgError = false;
    editing = true;
    // Precargar con los datos actuales; el token no se devuelve (has_token),
    // se deja vacío y solo se envía si el usuario escribe uno nuevo.
    form = { provider: entry.provider, domain: entry.domain, token: '' };
    phase = 'form';
  }

  function selectProvider(id) {
    form.provider = id;
    phase = 'form';
  }

  function cancelForm() {
    msg = '';
    msgError = false;
    phase = entry ? 'active' : 'empty';
  }

  async function saveDdns() {
    msg = '';
    msgError = false;
    saving = true;
    try {
      if (editing && entry) {
        // ── EDITAR: PUT. Solo enviamos token si el usuario escribió uno nuevo. ──
        const body = {
          enabled: entry.enabled,
          auto_update: entry.auto_update,
          update_interval: entry.update_interval,
        };
        const newToken = form.token.trim();
        if (newToken) body.token = newToken;
        const r = await fetch(`/api/v4/network/ddns/${entry.id}`, {
          method: 'PUT',
          headers: jsonHdrs(),
          body: JSON.stringify(body),
        });
        if (r.ok) {
          await refresh();
          phase = 'active';
        } else {
          const err = await r.json().catch(() => ({ error: r.statusText }));
          msg = err.error || `Error ${r.status}`;
          msgError = true;
        }
      } else {
        // ── CREAR: POST ──
        const body = {
          provider: form.provider,
          domain: form.domain.trim(),
          token: form.token.trim(),
          enabled: true,
          auto_update: true,
          update_interval: 900
        };
        const r = await fetch('/api/v4/network/ddns', {
          method: 'POST',
          headers: jsonHdrs(),
          body: JSON.stringify(body)
        });
        if (r.status === 201) {
          msg = 'Configuración guardada · esperando primera actualización…';
          msgError = false;
          await refresh();
          phase = 'active';
        } else if (r.status === 409) {
          msg = 'Ya existe un DDNS para este dominio.';
          msgError = true;
        } else {
          const err = await r.json().catch(() => ({ error: r.statusText }));
          msg = err.error || `Error ${r.status}`;
          msgError = true;
        }
      }
    } catch (e) {
      msg = 'Error de red: ' + e.message;
      msgError = true;
    } finally {
      saving = false;
    }
  }

  async function forceUpdate() {
    if (!entry || updatingNow) return;
    updatingNow = true;
    try {
      const r = await fetch(`/api/v4/network/ddns/${entry.id}/update`, {
        method: 'POST',
        headers: hdrs(),
      });
      if (r.ok) await refresh();
    } catch (e) {
      // poll lo arreglará
    } finally {
      updatingNow = false;
    }
  }

  async function toggleAutoUpdate() {
    if (!entry) return;
    const body = {
      enabled: entry.enabled,
      auto_update: !entry.auto_update,
      update_interval: entry.update_interval
    };
    try {
      const r = await fetch(`/api/v4/network/ddns/${entry.id}`, {
        method: 'PUT',
        headers: jsonHdrs(),
        body: JSON.stringify(body)
      });
      if (r.ok) await refresh();
    } catch (e) {
      // poll lo arregla
    }
  }

  async function disableDdns() {
    if (!entry) return;
    if (!confirm('¿Desactivar DDNS? El dominio dejará de actualizarse pero la config se conserva.')) return;
    const body = {
      enabled: false,
      auto_update: entry.auto_update,
      update_interval: entry.update_interval
    };
    try {
      const r = await fetch(`/api/v4/network/ddns/${entry.id}`, {
        method: 'PUT',
        headers: jsonHdrs(),
        body: JSON.stringify(body)
      });
      if (r.ok) await refresh();
    } catch (e) {}
  }

  async function enableDdns() {
    if (!entry) return;
    const body = {
      enabled: true,
      auto_update: entry.auto_update,
      update_interval: entry.update_interval
    };
    try {
      const r = await fetch(`/api/v4/network/ddns/${entry.id}`, {
        method: 'PUT',
        headers: jsonHdrs(),
        body: JSON.stringify(body)
      });
      if (r.ok) await refresh();
    } catch (e) {}
  }

  async function deleteDdns() {
    if (!entry) return;
    if (!confirm('¿Borrar DDNS por completo? También se borrará el token cifrado.')) return;
    try {
      const r = await fetch(`/api/v4/network/ddns/${entry.id}?delete_secret=true`, {
        method: 'DELETE',
        headers: hdrs()
      });
      if (r.status === 204) {
        entry = null;
        phase = 'empty';
      }
    } catch (e) {}
  }

  // ─── Formato ───
  function fmtRelative(iso) {
    if (!iso) return '—';
    try {
      const t = new Date(iso).getTime();
      const diff = Math.floor((Date.now() - t) / 1000);
      if (diff < 60) return 'hace ' + diff + 's';
      if (diff < 3600) return 'hace ' + Math.floor(diff / 60) + ' min';
      if (diff < 86400) return 'hace ' + Math.floor(diff / 3600) + ' h';
      return 'hace ' + Math.floor(diff / 86400) + ' días';
    } catch (e) {
      return iso;
    }
  }

  // statusVariant: traduce estado del backend a clase de color del mockup
  function statusVariant(s, e) {
    if (e && !e.enabled) return 'off';
    if (e && e.last_run_result === 'failed') return 'crit';
    if (s === 'converged') return 'ok';
    if (s === 'pending') return 'warn';
    if (s === 'drifted') return 'warn';
    return 'warn';
  }

  function statusLabel(e) {
    if (!e) return '—';
    if (!e.enabled) return 'Desactivado';
    if (e.last_run_result === 'success') return 'Normal';
    if (e.last_run_result === 'failed') return 'Error última act.';
    if (e.status === 'pending') return 'Pendiente';
    return 'Esperando primera act.';
  }
</script>

{#if loading}
  <div class="ddns-loading">
    <Spinner label="Cargando DDNS…" />
  </div>
{:else}

  <!-- ═══ HEADER de sección ═══ -->
  <div class="ddns-sec-head">
    <div class="ddns-sec-title">
      Dynamic DNS
      <span class="ddns-sec-sub">· acceso por nombre de dominio</span>
    </div>
    {#if phase === 'active' && entry}
      <button class="ddns-btn-add" on:click={startEdit}>
        <svg viewBox="0 0 24 24"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 0 1 3 3L12 15l-4 1 1-4z"/></svg>
        Editar
      </button>
    {:else if phase === 'empty'}
      <button class="ddns-btn-add" on:click={startAdd}>
        <svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
        Añadir
      </button>
    {/if}
  </div>

  <!-- ═══ CARD ACTIVO ═══ -->
  {#if phase === 'active' && entry}
    <div class="ddns-card">
      <div class="ddns-head">
        <div class="ddns-icon">
          <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
        </div>
        <div class="ddns-id">
          <div class="ddns-domain">{entry.domain}</div>
          <div class="ddns-provider">
            <span class="prov-tag">{entry.provider}</span>
          </div>
        </div>
        <div class="ddns-status">
          <span class="st-led {statusVariant(entry.status, entry)}"></span>
          <span class="st-text {statusVariant(entry.status, entry)}">{statusLabel(entry)}</span>
        </div>
      </div>

      <div class="ddns-meta">
        <div class="meta-cell">
          <div class="meta-k">IP detectada</div>
          <div class="meta-v accent">{entry.last_ip || '—'}</div>
        </div>
        <div class="meta-cell">
          <div class="meta-k">Última actualización</div>
          <div class="meta-v">{fmtRelative(entry.last_run_at)}</div>
        </div>
        <div class="meta-cell">
          <div class="meta-k">Intervalo</div>
          <div class="meta-v">{entry.update_interval}s</div>
        </div>
      </div>

      <div class="ddns-actions">
        <div class="auto-toggle"
             class:on={entry.auto_update}
             on:click={toggleAutoUpdate}
             role="button"
             tabindex="0"
             on:keydown={(e) => e.key === 'Enter' && toggleAutoUpdate()}>
          <span class="toggle-sw"></span>
          <span>Auto-actualización</span>
        </div>
        <div class="act-spacer"></div>
        {#if entry.enabled}
          <button class="act-btn" on:click={forceUpdate} disabled={updatingNow}>
            <svg viewBox="0 0 24 24" class:spinning={updatingNow}><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>
            {updatingNow ? 'Actualizando…' : 'Actualizar ahora'}
          </button>
        {/if}
        {#if entry.enabled}
          <button class="act-btn" on:click={disableDdns}>
            <svg viewBox="0 0 24 24"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
            Desactivar
          </button>
        {:else}
          <button class="act-btn" on:click={enableDdns}>
            <svg viewBox="0 0 24 24"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/></svg>
            Activar
          </button>
        {/if}
        <button class="act-btn" on:click={startEdit}>
          <svg viewBox="0 0 24 24"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 0 1 3 3L12 15l-4 1 1-4z"/></svg>
          Editar
        </button>
        <button class="act-btn danger" on:click={deleteDdns}>
          <svg viewBox="0 0 24 24"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>
          Eliminar
        </button>
      </div>
    </div>
  {/if}

  <!-- ═══ EMPTY ═══ -->
  {#if phase === 'empty'}
    <div class="ddns-empty">
      <div class="ddns-empty-icon">
        <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
      </div>
      <div class="ddns-empty-title">Sin dominios DDNS</div>
      <div class="ddns-empty-desc">Añade un proveedor para acceder a tu NAS por un nombre de dominio fijo aunque cambie tu IP pública.</div>
    </div>
  {/if}

  <!-- ═══ MODAL (select + form) ═══ -->
  {#if phase === 'select' || phase === 'form'}
    <div class="modal-backdrop" on:click|self={cancelForm} role="presentation">
      <div class="modal">
        <div class="modal-head">
          <div class="modal-icon">
            <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>
          </div>
          <div class="modal-title">{editing ? 'Editar DDNS' : 'Añadir DDNS'}</div>
          <button class="modal-close" on:click={cancelForm}>✕</button>
        </div>

        <div class="modal-body">
          <div class="enable-row">
            <div class="enable-check">
              <svg viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg>
            </div>
            <div class="enable-text">Habilita DDNS para acceder a tu NAS a través de un nombre de host registrado, aunque tu IP pública cambie.</div>
          </div>

          <div class="mfield">
            <div class="mfield-label">Proveedor de servicios</div>
            {#if editing}
              <div class="mfield-static">{form.provider}</div>
            {:else}
              <select class="mfield-input" bind:value={form.provider}>
                {#each PROVIDERS as prov}
                  <option value={prov.id}>{prov.name}</option>
                {/each}
              </select>
            {/if}
          </div>

          <div class="mfield">
            <div class="mfield-label">Dominio / Nombre de host</div>
            <input class="mfield-input" type="text" bind:value={form.domain} placeholder="miservidor.duckdns.org" />
          </div>

          <div class="mfield">
            <div class="mfield-label">Token</div>
            <div class="mfield-token">
              <input class="mfield-input" type={tokenVisible ? 'text' : 'password'} bind:value={form.token} placeholder={editing && entry?.has_token ? '•••••••• (sin cambios)' : 'Token del proveedor'} />
              <button class="token-eye" on:click={() => tokenVisible = !tokenVisible} title={tokenVisible ? 'Ocultar' : 'Mostrar'}>
                {tokenVisible ? '◉' : '○'}
              </button>
            </div>
            {#if editing && entry?.has_token}
              <div class="mfield-hint">Deja vacío para conservar el token actual.</div>
            {/if}
          </div>

          {#if editing && entry}
            <div class="mfield">
              <div class="mfield-label">Dirección externa (IPv4)</div>
              <div class="mfield-static">Automático ({entry.last_ip || 'detectando…'})</div>
            </div>
            <div class="mfield" style="margin-bottom:0">
              <div class="mfield-label">Estado</div>
              <div class="ddns-status" style="padding:6px 0">
                <span class="st-led {statusVariant(entry.status, entry)}"></span>
                <span class="st-text {statusVariant(entry.status, entry)}">{statusLabel(entry)}</span>
              </div>
            </div>
          {/if}

          {#if msg}
            <div class="modal-msg" class:error={msgError}>{msg}</div>
          {/if}
        </div>

        <div class="modal-foot">
          <button class="mbtn" on:click={cancelForm}>Cancelar</button>
          <div class="spacer"></div>
          <button
            class="mbtn primary"
            on:click={saveDdns}
            disabled={saving || !form.domain || (!editing && !form.token)}
          >
            {saving ? 'Guardando…' : 'Guardar'}
          </button>
        </div>
      </div>
    </div>
  {/if}

{/if}

<style>
  /* ═══ NetworkDDNS · cards + modal estilo NimOS Beta 8.1 ═══ */
  .ddns-loading { padding: 40px; display: flex; justify-content: center; }

  /* Header de sección */
  .ddns-sec-head {
    display: flex; align-items: center; justify-content: space-between;
    margin-bottom: 16px;
  }
  .ddns-sec-title {
    font-size: 14px; font-weight: 600; color: var(--ink, #f0f0f0);
    display: flex; align-items: baseline; gap: 8px;
  }
  .ddns-sec-sub {
    font-size: 11px; color: var(--ink-mute, #7a7a82); font-weight: 400;
    font-family: var(--font-mono, ui-monospace, monospace);
  }
  .ddns-btn-add {
    display: inline-flex; align-items: center; gap: 7px;
    padding: 8px 14px; border: 1px solid rgba(0,255,159,0.3); border-radius: 7px;
    background: rgba(0,255,159,0.06); color: var(--signal, #00ff9f);
    font-family: var(--font-mono, ui-monospace, monospace); font-size: 11px; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.5px; cursor: pointer;
  }
  .ddns-btn-add:hover { background: rgba(0,255,159,0.1); }
  .ddns-btn-add svg { width: 13px; height: 13px; stroke: currentColor; stroke-width: 2.5; fill: none; stroke-linecap: round; stroke-linejoin: round; }

  /* ═══ CARD ═══ */
  .ddns-card {
    background: var(--bg-card, #15151a);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 12px; overflow: hidden;
  }
  .ddns-head {
    display: flex; align-items: center; gap: 14px; padding: 15px 16px;
  }
  .ddns-icon {
    width: 38px; height: 38px; border-radius: 9px;
    background: rgba(77,184,255,0.10); color: var(--st-info, #4db8ff);
    display: flex; align-items: center; justify-content: center; flex-shrink: 0;
  }
  .ddns-icon svg { width: 19px; height: 19px; stroke: currentColor; stroke-width: 1.7; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .ddns-id { flex: 1; min-width: 0; }
  .ddns-domain {
    font-family: var(--font-mono, ui-monospace, monospace); font-size: 14px; font-weight: 600;
    color: var(--ink, #f0f0f0); letter-spacing: -0.2px;
    white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  }
  .ddns-provider { font-size: 11px; color: var(--ink-mute, #7a7a82); margin-top: 3px; display: flex; align-items: center; gap: 6px; }
  .prov-tag {
    font-family: var(--font-mono, ui-monospace, monospace); font-size: 9px;
    padding: 1px 6px; border-radius: 3px;
    background: var(--bg-inner, #101015); border: 1px solid var(--bd-2, #20202a);
    color: var(--ink-dim, #9c9ca4); letter-spacing: 0.3px;
  }
  .ddns-status { display: flex; align-items: center; gap: 7px; font-size: 12px; }
  .st-led { width: 8px; height: 8px; border-radius: 2px; flex-shrink: 0; }
  .st-led.ok { background: var(--st-ok, #00ff9f); box-shadow: 0 0 5px rgba(0,255,159,0.4); }
  .st-led.warn { background: var(--st-warn, #ffc857); }
  .st-led.crit { background: var(--st-crit, #ff5a5a); }
  .st-led.off { background: var(--ink-trace, #5a5a62); }
  .st-text.ok { color: var(--st-ok, #00ff9f); }
  .st-text.warn { color: var(--st-warn, #ffc857); }
  .st-text.crit { color: var(--st-crit, #ff5a5a); }
  .st-text.off { color: var(--ink-trace, #5a5a62); }

  /* meta 3 columnas */
  .ddns-meta {
    display: grid; grid-template-columns: 1fr 1fr 1fr;
    border-top: 1px solid var(--bd-2, #20202a);
    background: var(--bg-window, #16161a);
  }
  .meta-cell { padding: 11px 16px; }
  .meta-cell + .meta-cell { border-left: 1px solid var(--bd-2, #20202a); }
  .meta-k {
    font-size: 9px; color: var(--ink-trace, #5a5a62); text-transform: uppercase;
    letter-spacing: 0.8px; font-weight: 600; margin-bottom: 4px;
  }
  .meta-v { font-size: 12px; color: var(--ink-2, #d0d0d4); font-family: var(--font-mono, ui-monospace, monospace); }
  .meta-v.accent { color: var(--st-info, #4db8ff); }

  /* actions */
  .ddns-actions { display: flex; gap: 6px; padding: 11px 16px; border-top: 1px solid var(--bd-2, #20202a); align-items: center; }
  .act-btn {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 7px 12px; border: 1px solid var(--bd-2, #20202a); border-radius: 6px;
    background: transparent; color: var(--ink-dim, #9c9ca4); font-size: 11px;
    font-weight: 500; cursor: pointer; font-family: var(--font-sans, system-ui, sans-serif);
  }
  .act-btn:hover { color: var(--ink, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .act-btn svg { width: 12px; height: 12px; stroke: currentColor; stroke-width: 2; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .act-btn.danger:hover { color: var(--st-crit, #ff5a5a); border-color: rgba(255,90,90,0.3); }
  .act-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .act-btn svg.spinning { animation: ddns-spin 0.8s linear infinite; }
  @keyframes ddns-spin { to { transform: rotate(360deg); } }
  .act-spacer { flex: 1; }

  .auto-toggle { display: inline-flex; align-items: center; gap: 8px; font-size: 11px; color: var(--ink-dim, #9c9ca4); cursor: pointer; }
  .toggle-sw {
    width: 30px; height: 17px; background: var(--bd-3, #2a2a32);
    border-radius: 4px; position: relative; transition: background 0.15s;
  }
  .toggle-sw::after {
    content: ''; position: absolute; top: 2px; left: 2px;
    width: 13px; height: 13px; background: var(--ink-mute, #7a7a82);
    border-radius: 3px; transition: left 0.15s, background 0.15s;
  }
  .auto-toggle.on .toggle-sw { background: var(--signal, #00ff9f); }
  .auto-toggle.on .toggle-sw::after { left: 15px; background: var(--bg-window, #16161a); }

  /* ═══ EMPTY ═══ */
  .ddns-empty { text-align: center; padding: 50px 20px; }
  .ddns-empty-icon { width: 48px; height: 48px; margin: 0 auto 14px; color: var(--ink-trace, #5a5a62); opacity: 0.5; }
  .ddns-empty-icon svg { width: 100%; height: 100%; stroke: currentColor; stroke-width: 1.5; fill: none; }
  .ddns-empty-title { font-size: 14px; color: var(--ink-dim, #9c9ca4); margin-bottom: 6px; }
  .ddns-empty-desc { font-size: 12px; color: var(--ink-mute, #7a7a82); max-width: 380px; margin: 0 auto; line-height: 1.5; }

  /* ═══ MODAL ═══ */
  .modal-backdrop {
    position: fixed; inset: 0; background: rgba(0,0,0,0.6);
    backdrop-filter: blur(3px); display: flex; align-items: center;
    justify-content: center; z-index: 500;
  }
  .modal {
    width: 480px; max-width: calc(100vw - 40px);
    background: var(--bg-window, #16161a); border: 1px solid var(--bd-2, #20202a);
    border-radius: 14px; overflow: hidden; box-shadow: 0 24px 70px rgba(0,0,0,0.55);
  }
  .modal-head { display: flex; align-items: center; gap: 12px; padding: 16px 20px; border-bottom: 1px solid var(--bd-2, #20202a); }
  .modal-icon {
    width: 32px; height: 32px; border-radius: 8px;
    background: rgba(77,184,255,0.10); color: var(--st-info, #4db8ff);
    display: flex; align-items: center; justify-content: center; flex-shrink: 0;
  }
  .modal-icon svg { width: 16px; height: 16px; stroke: currentColor; stroke-width: 1.8; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .modal-title { flex: 1; font-size: 14px; font-weight: 600; color: var(--ink, #f0f0f0); }
  .modal-close {
    width: 28px; height: 28px; border-radius: 6px; border: none;
    background: transparent; color: var(--ink-mute, #7a7a82); cursor: pointer; font-size: 16px;
  }
  .modal-close:hover { color: var(--ink, #f0f0f0); background: rgba(255,255,255,0.04); }
  .modal-body { padding: 20px; }

  .enable-row {
    display: flex; gap: 11px; padding: 12px 14px; margin-bottom: 18px;
    background: rgba(0,255,159,0.04); border: 1px solid rgba(0,255,159,0.18);
    border-radius: 8px;
  }
  .enable-check {
    width: 18px; height: 18px; border-radius: 4px; background: var(--signal, #00ff9f);
    display: flex; align-items: center; justify-content: center;
    color: var(--bg-window, #16161a); flex-shrink: 0; margin-top: 1px;
  }
  .enable-check svg { width: 11px; height: 11px; stroke: currentColor; stroke-width: 3; fill: none; stroke-linecap: round; stroke-linejoin: round; }
  .enable-text { font-size: 12px; color: var(--ink-2, #d0d0d4); line-height: 1.5; }

  .mfield { margin-bottom: 14px; }
  .mfield-label {
    font-size: 10px; color: var(--ink-mute, #7a7a82); text-transform: uppercase;
    letter-spacing: 1px; font-weight: 600; margin-bottom: 7px;
  }
  .mfield-static { font-size: 13px; color: var(--ink, #f0f0f0); font-family: var(--font-mono, ui-monospace, monospace); padding: 9px 0; }
  .mfield-input {
    width: 100%; background: var(--bg-inner, #101015); border: 1px solid var(--bd-2, #20202a);
    border-radius: 7px; padding: 10px 12px; color: var(--ink, #f0f0f0);
    font-family: var(--font-mono, ui-monospace, monospace); font-size: 13px; outline: none;
    transition: border-color 0.12s;
  }
  .mfield-input:focus { border-color: rgba(0,255,159,0.35); }
  .mfield-input::placeholder { color: var(--ink-trace, #5a5a62); }
  .mfield-token { display: flex; gap: 8px; align-items: center; }
  .mfield-token .mfield-input { flex: 1; }
  .token-eye {
    width: 38px; height: 38px; border: 1px solid var(--bd-2, #20202a); border-radius: 7px;
    background: var(--bg-inner, #101015); color: var(--ink-mute, #7a7a82); cursor: pointer; flex-shrink: 0;
  }
  .token-eye:hover { color: var(--ink, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .mfield-hint { font-size: 11px; color: var(--ink-trace, #5a5a62); margin-top: 6px; }

  .modal-msg { font-size: 12px; color: var(--ink-dim, #9c9ca4); font-family: var(--font-mono, ui-monospace, monospace); margin-top: 12px; }
  .modal-msg.error { color: var(--st-crit, #ff5a5a); }

  .modal-foot { display: flex; gap: 8px; padding: 14px 20px; border-top: 1px solid var(--bd-2, #20202a); }
  .modal-foot .spacer { flex: 1; }
  .mbtn {
    padding: 9px 18px; border-radius: 7px; font-size: 12px; font-weight: 600; cursor: pointer;
    border: 1px solid var(--bd-2, #20202a); background: transparent; color: var(--ink-dim, #9c9ca4);
    font-family: var(--font-sans, system-ui, sans-serif);
  }
  .mbtn:hover { color: var(--ink, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .mbtn.primary { background: var(--signal, #00ff9f); color: var(--bg-window, #16161a); border-color: var(--signal, #00ff9f); }
  .mbtn.primary:hover:not(:disabled) { filter: brightness(1.08); }
  .mbtn:disabled { opacity: 0.4; cursor: not-allowed; }
</style>
