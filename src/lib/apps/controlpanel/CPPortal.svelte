<script>
  /**
   * CPPortal · Panel de Control · sección Portal · 2FA
   * ────────────────────────────────────────────────────
   * Autenticación en dos pasos (TOTP) para el login. Migrado desde
   * Settings (sección 'portal') al lenguaje visual v3.
   *
   * API:
   *   GET  /api/auth/2fa/status            → { enabled }
   *   POST /api/auth/2fa/setup             → { secret, qr(svg) }
   *   POST /api/auth/2fa/verify  { code }  → { backupCodes }
   *   POST /api/auth/2fa/disable { password }
   */
  import { onMount } from 'svelte';
  import { hdrs } from '$lib/stores/auth.js';

  let twofa = { loading: true, enabled: false };
  let twofaSetup = null;
  let twofaQrSvg = '';
  let twofaUri = '';
  let twofaCode = '';
  let twofaBackupCodes = null;
  let twofaSaving = false;
  let twofaMsg = '';
  let twofaMsgError = false;
  let showDisableConfirm = false;
  let twofaDisablePassword = '';
  let copied = false;

  async function loadTwoFA() {
    twofa.loading = true;
    try {
      const r = await fetch('/api/auth/2fa/status', { headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        twofa = { loading: false, enabled: !!d.enabled };
      } else twofa.loading = false;
    } catch { twofa.loading = false; }
  }

  async function startSetup() {
    twofaSaving = true;
    twofaMsg = '';
    try {
      const r = await fetch('/api/auth/2fa/setup', { method: 'POST', headers: hdrs() });
      if (r.ok) {
        const d = await r.json();
        twofaSetup = { secret: d.secret };
        twofaQrSvg = d.qr || '';
        twofaUri = d.uri || '';
      } else { twofaMsg = 'Error al iniciar'; twofaMsgError = true; }
    } catch { twofaMsg = 'Error de red'; twofaMsgError = true; }
    twofaSaving = false;
  }

  async function copySecret() {
    if (!twofaSetup?.secret) return;
    try {
      await navigator.clipboard.writeText(twofaSetup.secret);
      copied = true;
      setTimeout(() => (copied = false), 1800);
    } catch {}
  }

  async function verify() {
    if (!twofaCode || twofaCode.length !== 6) return;
    twofaSaving = true;
    twofaMsg = '';
    try {
      const r = await fetch('/api/auth/2fa/verify', {
        method: 'POST',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ code: twofaCode }),
      });
      if (r.ok) {
        const d = await r.json();
        twofaBackupCodes = d.backupCodes || [];
        twofa.enabled = true;
        twofaSetup = null;
        twofaQrSvg = '';
        twofaCode = '';
      } else { twofaMsg = 'Código incorrecto'; twofaMsgError = true; }
    } catch { twofaMsg = 'Error de red'; twofaMsgError = true; }
    twofaSaving = false;
  }

  async function disable() {
    twofaSaving = true;
    twofaMsg = '';
    try {
      const r = await fetch('/api/auth/2fa/disable', {
        method: 'POST',
        headers: { ...hdrs(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: twofaDisablePassword }),
      });
      if (r.ok) {
        twofa.enabled = false;
        showDisableConfirm = false;
        twofaDisablePassword = '';
      } else { twofaMsg = 'Contraseña incorrecta'; twofaMsgError = true; }
    } catch { twofaMsg = 'Error de red'; twofaMsgError = true; }
    twofaSaving = false;
  }

  onMount(loadTwoFA);
</script>

<div class="cp-portal">
  {#if twofa.loading}
    <div class="cpp-empty">Cargando…</div>

  {:else if twofaBackupCodes}
    <!-- Éxito · códigos de recuperación -->
    <div class="tf-success">
      <div class="tf-success-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <polyline points="20 6 9 17 4 12"/>
        </svg>
      </div>
      <div class="tf-success-title">2FA activado correctamente</div>
      <p class="tf-success-desc">
        Guarda estos códigos de recuperación en un lugar seguro. Son de un solo uso y te permitirán
        acceder si pierdes tu dispositivo.
      </p>
      <div class="tf-backup-grid">
        {#each twofaBackupCodes as code}
          <div class="tf-backup-code">{code}</div>
        {/each}
      </div>
      <button class="tf-btn" on:click={() => twofaBackupCodes = null}>Ya los he guardado</button>
    </div>

  {:else if twofaSetup}
    <!-- Setup · QR + código -->
    <div class="tf-form">
      <div class="tf-form-title">Configurar 2FA</div>
      <p class="tf-step">1 · Añade la cuenta a Google Authenticator, Authy u otra app TOTP</p>
      {#if twofaQrSvg}
        <div class="tf-qr">{@html twofaQrSvg}</div>
        <p class="tf-hint">Escanea el QR, o usa la clave manual de abajo.</p>
      {:else}
        <div class="tf-noqr">
          <span class="tf-noqr-tag">QR no disponible</span>
          Introduce esta clave manualmente en tu app de autenticación.
        </div>
      {/if}
      <div class="tf-field">
        <span class="tf-label">Clave manual</span>
        <div class="tf-secret-row">
          <code class="tf-secret">{twofaSetup.secret}</code>
          <button class="tf-copy" on:click={copySecret}>{copied ? '✓ copiada' : 'Copiar'}</button>
        </div>
      </div>
      <p class="tf-step">2 · Introduce el código de 6 dígitos</p>
      <div class="tf-row">
        <input
          class="tf-input tf-code"
          type="text"
          placeholder="000000"
          maxlength="6"
          bind:value={twofaCode}
          on:input={() => twofaCode = twofaCode.replace(/\D/g, '')}
        />
        <button class="tf-btn primary" on:click={verify} disabled={twofaSaving || twofaCode.length !== 6}>
          {twofaSaving ? 'Verificando…' : 'Verificar'}
        </button>
        <button class="tf-btn" on:click={() => { twofaSetup = null; twofaCode = ''; }}>Cancelar</button>
      </div>
      {#if twofaMsg}<div class="tf-msg" class:error={twofaMsgError}>{twofaMsg}</div>{/if}
    </div>

  {:else if twofa.enabled}
    <!-- Estado activado -->
    <div class="tf-status enabled">
      <div class="tf-status-icon enabled">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
        </svg>
      </div>
      <div class="tf-status-info">
        <div class="tf-status-title">2FA activado</div>
        <div class="tf-status-desc">Tu cuenta está protegida con autenticación en dos pasos.</div>
      </div>
      <div class="tf-badge enabled">Activo</div>
    </div>

    {#if !showDisableConfirm}
      <button class="tf-btn danger" on:click={() => showDisableConfirm = true}>Desactivar 2FA</button>
    {:else}
      <div class="tf-form">
        <div class="tf-form-title">Confirma tu contraseña para desactivar 2FA</div>
        <div class="tf-row">
          <input class="tf-input" type="password" placeholder="Contraseña actual" bind:value={twofaDisablePassword} autocomplete="current-password" />
          <button class="tf-btn danger" on:click={disable} disabled={twofaSaving}>
            {twofaSaving ? '…' : 'Desactivar'}
          </button>
          <button class="tf-btn" on:click={() => { showDisableConfirm = false; twofaDisablePassword = ''; }}>Cancelar</button>
        </div>
        {#if twofaMsg}<div class="tf-msg" class:error={twofaMsgError}>{twofaMsg}</div>{/if}
      </div>
    {/if}

  {:else}
    <!-- Estado desactivado -->
    <div class="tf-status">
      <div class="tf-status-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/>
        </svg>
      </div>
      <div class="tf-status-info">
        <div class="tf-status-title">2FA desactivado</div>
        <div class="tf-status-desc">Añade una capa extra de seguridad con Google Authenticator u otra app TOTP compatible.</div>
      </div>
      <div class="tf-badge">Inactivo</div>
    </div>
    <button class="tf-btn primary" on:click={startSetup} disabled={twofaSaving}>
      {twofaSaving ? 'Configurando…' : 'Configurar 2FA'}
    </button>
  {/if}
</div>

<style>
  .cp-portal { display: flex; flex-direction: column; gap: 14px; }

  .cpp-empty {
    padding: 24px;
    text-align: center;
    color: var(--fg-5, #5a5a62);
    font-size: 12px;
    font-family: var(--font-mono);
  }

  /* Estado (activado/desactivado) */
  .tf-status {
    background: var(--bg-card, #15151a);
    border-radius: 8px;
    padding: 16px;
    display: flex;
    align-items: center;
    gap: 14px;
  }
  .tf-status.enabled { border-left: 2px solid var(--st-ok, #00ff9f); }
  .tf-status-icon {
    width: 38px;
    height: 38px;
    border-radius: 8px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--fg-4, #7a7a82);
    flex-shrink: 0;
  }
  .tf-status-icon.enabled {
    color: var(--nim-green, #00ff9f);
    border-color: rgba(0, 255, 159, 0.3);
    background: rgba(0, 255, 159, 0.06);
  }
  .tf-status-icon svg { width: 18px; height: 18px; }
  .tf-status-info { flex: 1; min-width: 0; }
  .tf-status-title {
    font-size: 13px;
    color: var(--fg, #f0f0f0);
    font-family: var(--font-mono);
    font-weight: 600;
  }
  .tf-status-desc {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    margin-top: 3px;
    line-height: 1.4;
  }
  .tf-badge {
    font-size: 9px;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.8px;
    padding: 3px 9px;
    border-radius: 3px;
    border: 1px solid var(--bd-2, #20202a);
    color: var(--fg-5, #5a5a62);
    flex-shrink: 0;
  }
  .tf-badge.enabled {
    color: var(--nim-green, #00ff9f);
    border-color: rgba(0, 255, 159, 0.3);
    background: rgba(0, 255, 159, 0.06);
  }

  /* Formulario (setup / disable) */
  .tf-form {
    background: var(--bg-card, #15151a);
    border-radius: 8px;
    padding: 18px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .tf-form-title {
    font-size: 13px;
    color: var(--fg, #f0f0f0);
    font-family: var(--font-mono);
    font-weight: 600;
  }
  .tf-step {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    font-family: var(--font-mono);
  }
  .tf-qr {
    align-self: flex-start;
    background: #fff;
    padding: 10px;
    border-radius: 8px;
    line-height: 0;
  }
  .tf-qr :global(svg) { width: 160px; height: 160px; display: block; }
  .tf-field { display: flex; flex-direction: column; gap: 6px; }
  .tf-label {
    font-size: 10px;
    color: var(--fg-4, #7a7a82);
    text-transform: uppercase;
    letter-spacing: 0.6px;
    font-family: var(--font-mono);
  }
  .tf-secret {
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    padding: 9px 12px;
    color: var(--nim-green, #00ff9f);
    font-family: var(--font-mono);
    font-size: 12px;
    letter-spacing: 1px;
    word-break: break-all;
    flex: 1;
  }
  .tf-secret-row { display: flex; gap: 8px; align-items: stretch; }
  .tf-copy {
    flex-shrink: 0;
    padding: 0 14px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    color: var(--fg-3, #9c9ca4);
    font-size: 11px;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: all 0.12s;
  }
  .tf-copy:hover { color: var(--nim-green, #00ff9f); border-color: rgba(0, 255, 159, 0.35); }
  .tf-hint {
    font-size: 10px;
    color: var(--fg-5, #5a5a62);
    font-family: var(--font-mono);
  }
  .tf-noqr {
    background: rgba(255, 200, 87, 0.06);
    border: 1px solid rgba(255, 200, 87, 0.2);
    border-radius: 8px;
    padding: 12px 14px;
    font-size: 11px;
    color: var(--fg-3, #9c9ca4);
    font-family: var(--font-mono);
    line-height: 1.5;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .tf-noqr-tag {
    font-size: 9px;
    text-transform: uppercase;
    letter-spacing: 0.8px;
    color: var(--st-warn, #ffc857);
    align-self: flex-start;
  }
  .tf-row { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
  .tf-input {
    flex: 1;
    min-width: 140px;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 6px;
    padding: 9px 12px;
    color: var(--fg, #f0f0f0);
    font-size: 13px;
    font-family: var(--font-mono);
    outline: none;
  }
  .tf-input:focus { border-color: rgba(0, 255, 159, 0.35); }
  .tf-code {
    flex: 0 0 130px;
    letter-spacing: 6px;
    text-align: center;
    font-size: 16px;
  }

  .tf-msg { font-size: 11px; color: var(--fg-3, #9c9ca4); font-family: var(--font-mono); }
  .tf-msg.error { color: var(--st-crit, #ff5a5a); }

  /* Éxito · backup codes */
  .tf-success {
    background: var(--bg-card, #15151a);
    border-radius: 8px;
    padding: 22px;
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    gap: 8px;
  }
  .tf-success-icon {
    width: 44px;
    height: 44px;
    border-radius: 50%;
    background: rgba(0, 255, 159, 0.1);
    border: 1px solid rgba(0, 255, 159, 0.3);
    color: var(--nim-green, #00ff9f);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .tf-success-icon svg { width: 22px; height: 22px; }
  .tf-success-title {
    font-size: 14px;
    color: var(--fg, #f0f0f0);
    font-family: var(--font-mono);
    font-weight: 600;
  }
  .tf-success-desc {
    font-size: 11px;
    color: var(--fg-4, #7a7a82);
    line-height: 1.5;
    max-width: 420px;
  }
  .tf-backup-grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 6px;
    width: 100%;
    max-width: 320px;
    margin: 8px 0;
  }
  .tf-backup-code {
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 5px;
    padding: 8px;
    font-family: var(--font-mono);
    font-size: 13px;
    color: var(--fg-2, #d0d0d4);
    letter-spacing: 1px;
  }

  /* Botones */
  .tf-btn {
    align-self: flex-start;
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
  .tf-btn:hover:not(:disabled) { color: var(--fg, #f0f0f0); border-color: var(--bd-3, #2a2a32); }
  .tf-btn.primary {
    background: var(--nim-green, #00ff9f);
    border-color: var(--nim-green, #00ff9f);
    color: var(--bg-window, #16161a);
    font-weight: 600;
  }
  .tf-btn.primary:hover:not(:disabled) { filter: brightness(1.08); }
  .tf-btn.danger { color: var(--st-crit, #ff5a5a); border-color: rgba(255, 90, 90, 0.3); }
  .tf-btn.danger:hover:not(:disabled) { background: rgba(255, 90, 90, 0.06); border-color: rgba(255, 90, 90, 0.5); }
  .tf-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
