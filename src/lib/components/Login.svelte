<script>
  /**
   * Login · Pantalla de acceso v3 (card glass centrado)
   * ────────────────────────────────────────────────────
   * Rediseñado a partir de login-mockup.html. Estados:
   *   · login    → usuario + contraseña
   *   · loading  → inputs deshabilitados + spinner
   *   · 2FA      → código TOTP grande centrado + cancelar
   *   · error    → banner ERR bajo los campos
   *
   * Lógica real preservada: login() del store de auth, flujo
   * requires2FA, autocomplete correcto para gestores de contraseñas.
   */
  import { login } from '$lib/stores/auth.js';
  import NimosLogo from '$lib/ui/NimosLogo.svelte';

  let username = '';
  let password = '';
  let totpCode = '';
  let error = '';
  let loading = false;
  let needs2FA = false;

  async function handleSubmit(e) {
    e.preventDefault();
    if (needs2FA ? !totpCode : (!username || !password)) return;

    error = '';
    loading = true;

    try {
      const result = await login(username, password, totpCode);
      if (result?.requires2FA) {
        needs2FA = true;
        loading = false;
        return;
      }
      // Login ok → la página se recarga desde el store
    } catch (err) {
      error = err.message || 'Error de autenticación';
      loading = false;
    }
  }

  function cancel2FA() {
    needs2FA = false;
    totpCode = '';
    error = '';
    loading = false;
  }

  // Solo dígitos en el TOTP
  function onTotpInput(e) {
    totpCode = e.target.value.replace(/\D/g, '').slice(0, 6);
    e.target.value = totpCode;
  }
</script>

<div class="login-screen">
  <div class="login-wrap">

    <div class="login-card">

      <!-- ═══ BRAND ═══ -->
      <div class="brand">
        <div class="brand-logo">
          <NimosLogo size={56} showText={false} />
        </div>
        <div class="brand-title">NimOS</div>
        <div class="brand-tagline">your <span class="nas">NAS</span> for developers</div>
      </div>

      <!-- ═══ FORM · cambia según estado ═══ -->
      {#if !needs2FA}
        <form class="form-state" on:submit={handleSubmit}>
          <div class="section-label">iniciar sesión</div>

          <div class="field">
            <label class="field-label" for="user">
              <span>usuario</span>
              <span class="kb">U</span>
            </label>
            <!-- svelte-ignore a11y_autofocus -->
            <input
              type="text"
              id="user"
              bind:value={username}
              placeholder="admin"
              autocomplete="username"
              disabled={loading}
              autofocus
              required
            />
          </div>

          <div class="field">
            <label class="field-label" for="pass">
              <span>contraseña</span>
              <span class="kb">P</span>
            </label>
            <input
              type="password"
              id="pass"
              bind:value={password}
              placeholder="••••••••"
              autocomplete="current-password"
              disabled={loading}
              required
            />
          </div>

          {#if error}
            <div class="error">
              <span class="err-tag">err</span>
              <span>{error}</span>
            </div>
          {/if}

          <button class="btn-submit" type="submit" disabled={loading}>
            {#if loading}
              <span class="spinner"></span>
              Autenticando…
            {:else}
              Entrar
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                <path d="M5 12h14"/>
                <path d="M12 5l7 7-7 7"/>
              </svg>
            {/if}
          </button>
        </form>
      {:else}
        <form class="form-state" on:submit={handleSubmit}>
          <div class="section-label">verificación 2FA</div>

          <div class="totp-hint">
            Introduce el código de 6 dígitos de tu app autenticadora <span class="app">(Authy, Google Authenticator, 1Password…)</span>
          </div>

          <div class="field">
            <label class="field-label" for="totp">
              <span>código TOTP</span>
              <span class="kb">2</span>
            </label>
            <!-- svelte-ignore a11y_autofocus -->
            <input
              type="text"
              id="totp"
              class="totp-input"
              value={totpCode}
              on:input={onTotpInput}
              placeholder="••••••"
              maxlength="6"
              inputmode="numeric"
              autocomplete="one-time-code"
              disabled={loading}
              autofocus
              required
            />
          </div>

          {#if error}
            <div class="error">
              <span class="err-tag">err</span>
              <span>{error}</span>
            </div>
          {/if}

          <button class="btn-submit" type="submit" disabled={loading}>
            {#if loading}
              <span class="spinner"></span>
              Verificando…
            {:else}
              Verificar
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="20 6 9 17 4 12"/>
              </svg>
            {/if}
          </button>

          <button class="btn-back" type="button" on:click={cancel2FA} disabled={loading}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <polyline points="15 18 9 12 15 6"/>
            </svg>
            Cancelar
          </button>
        </form>
      {/if}

      <!-- ═══ FOOTER ═══ -->
      <div class="card-footer">
        <div class="item">
          <span class="daemon-led"></span>
          <span class="k">daemon</span>
          <span class="v">running</span>
        </div>
        <div class="item">
          <span class="k">build</span>
          <span class="v">0.8.1-alpha</span>
        </div>
      </div>

    </div>

  </div>
</div>

<style>
  .login-screen {
    width: 100%;
    height: 100vh;
    height: 100dvh; /* dynamic viewport: evita saltos con las barras del navegador móvil */
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 20px;
    padding-top: max(20px, env(safe-area-inset-top));
    padding-bottom: max(20px, env(safe-area-inset-bottom));
    overflow: hidden;
    font-family: var(--font-sans, ui-sans-serif, system-ui, sans-serif);
    background:
      /* Glow verde en esquinas */
      radial-gradient(ellipse 800px 600px at 20% 30%, rgba(0, 80, 50, 0.12) 0%, transparent 65%),
      radial-gradient(ellipse 600px 400px at 80% 70%, rgba(0, 100, 70, 0.06) 0%, transparent 60%),
      /* Grid muy sutil */
      linear-gradient(rgba(0, 255, 159, 0.012) 1px, transparent 1px) 0 0 / 24px 24px,
      linear-gradient(90deg, rgba(0, 255, 159, 0.012) 1px, transparent 1px) 0 0 / 24px 24px,
      #050507;
  }

  .login-wrap {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 20px;
    width: 100%;
    max-width: 380px;
  }

  .login-card {
    width: 100%;
    background: rgba(22, 22, 26, 0.85);
    backdrop-filter: blur(24px) saturate(1.4);
    -webkit-backdrop-filter: blur(24px) saturate(1.4);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 14px;
    padding: 32px 32px 26px;
    box-shadow:
      0 24px 60px rgba(0, 0, 0, 0.5),
      0 0 0 1px rgba(0, 0, 0, 0.4),
      inset 0 1px 0 rgba(255, 255, 255, 0.04);
    position: relative;
    overflow: hidden;
  }

  /* ─── Brand ─── */
  .brand {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 14px;
    margin-bottom: 26px;
  }
  .brand-logo {
    width: 56px;
    height: 56px;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .brand-title {
    font-size: 20px;
    font-weight: 700;
    color: var(--ink);
    letter-spacing: -0.3px;
  }
  .brand-tagline {
    font-size: 11px;
    color: var(--ink-faint);
    letter-spacing: 0.3px;
    margin-top: -8px;
  }
  .brand-tagline .nas {
    color: var(--signal);
    font-weight: 600;
  }

  /* ─── Section label ─── */
  .section-label {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 10px;
    color: var(--ink-trace);
    text-transform: uppercase;
    letter-spacing: 1.5px;
    font-weight: 600;
    margin-bottom: 16px;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .section-label::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--bd-2, #20202a);
  }

  /* ─── Fields ─── */
  .field {
    margin-bottom: 12px;
  }
  .field-label {
    display: flex;
    justify-content: space-between;
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 10px;
    color: var(--ink-faint);
    text-transform: uppercase;
    letter-spacing: 0.8px;
    margin-bottom: 6px;
    font-weight: 500;
  }
  .field-label .kb {
    font-size: 9px;
    color: var(--ink-trace);
    padding: 1px 5px;
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 3px;
  }
  .field input {
    width: 100%;
    background: var(--bg-inner, #101015);
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 7px;
    padding: 10px 12px;
    color: var(--ink);
    font-family: inherit;
    font-size: 13px;
    outline: none;
    transition: border-color 0.12s, background 0.12s;
  }
  .field input::placeholder {
    color: var(--ink-trace);
  }
  .field input:focus {
    border-color: rgba(0, 255, 159, 0.35);
    background: rgba(0, 255, 159, 0.03);
  }
  .field input:disabled {
    opacity: 0.6;
  }

  /* ─── TOTP grande centrado ─── */
  .totp-input {
    font-family: var(--font-mono, ui-monospace, monospace) !important;
    text-align: center;
    font-size: 22px !important;
    letter-spacing: 8px;
    padding: 14px 12px !important;
    font-variant-numeric: tabular-nums;
  }
  .totp-hint {
    font-size: 11px;
    color: var(--ink-faint);
    text-align: center;
    margin-bottom: 16px;
    line-height: 1.5;
  }
  .totp-hint .app {
    color: var(--ink-dim);
    font-weight: 500;
  }

  /* ─── Error ─── */
  .error {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 9px 12px;
    background: rgba(255, 90, 90, 0.08);
    border: 1px solid rgba(255, 90, 90, 0.25);
    border-radius: 6px;
    margin-bottom: 12px;
    font-size: 12px;
    color: var(--ink-dim);
  }
  .err-tag {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 9px;
    color: var(--crit);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.8px;
    padding: 1px 6px;
    border: 1px solid rgba(255, 90, 90, 0.4);
    border-radius: 3px;
    flex-shrink: 0;
  }

  /* ─── Submit ─── */
  .btn-submit {
    width: 100%;
    padding: 11px 14px;
    background: var(--signal);
    color: var(--bg-window, #16161a);
    border: none;
    border-radius: 8px;
    font-family: inherit;
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    transition: filter 0.12s;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    margin-top: 6px;
  }
  .btn-submit:hover:not(:disabled) {
    filter: brightness(1.08);
  }
  .btn-submit:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .btn-submit svg {
    width: 13px;
    height: 13px;
  }

  .spinner {
    width: 12px;
    height: 12px;
    border-radius: 50%;
    border: 1.5px solid rgba(22, 22, 26, 0.3);
    border-top-color: var(--bg-window, #16161a);
    animation: spin 0.6s linear infinite;
  }
  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  /* ─── Back (cancelar 2FA) ─── */
  .btn-back {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 12px;
    background: transparent;
    border: 1px solid var(--bd-2, #20202a);
    border-radius: 7px;
    color: var(--ink-mute);
    font-family: inherit;
    font-size: 12px;
    cursor: pointer;
    width: 100%;
    justify-content: center;
    margin-top: 8px;
    transition: color 0.12s, border-color 0.12s;
  }
  .btn-back:hover:not(:disabled) {
    color: var(--ink);
    border-color: var(--line-bright);
  }
  .btn-back:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-back svg { width: 12px; height: 12px; }

  /* ─── Footer del card ─── */
  .card-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 22px;
    padding-top: 16px;
    border-top: 1px solid var(--bd-2, #20202a);
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: 10px;
    color: var(--ink-trace);
    letter-spacing: 0.3px;
  }
  .card-footer .item {
    display: flex;
    align-items: center;
    gap: 5px;
  }
  .card-footer .k {
    text-transform: uppercase;
    letter-spacing: 0.8px;
    font-weight: 600;
  }
  .card-footer .v {
    color: var(--ink-mute);
  }
  .daemon-led {
    width: 6px;
    height: 6px;
    border-radius: 1.5px;
    background: var(--signal);
    box-shadow: 0 0 4px rgba(0, 255, 159, 0.5);
    animation: pulse 2.5s ease-in-out infinite;
  }
  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50%      { opacity: 0.6; }
  }

  /* ─── Transición entre estados ─── */
  .form-state {
    animation: slideIn 0.3s cubic-bezier(0.16, 1, 0.3, 1);
  }
  @keyframes slideIn {
    from { opacity: 0; transform: translateY(8px); }
    to   { opacity: 1; transform: translateY(0); }
  }
</style>
