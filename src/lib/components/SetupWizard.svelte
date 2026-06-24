<script>
  /**
   * SetupWizard · Onboarding de primer arranque (5 pasos)
   * ─────────────────────────────────────────────────────
   * 1 Idioma · 2 Bienvenida · 3 Admin · 4 2FA · 5 Resumen
   *
   * Flujo de auth (ver auth.js):
   *   - Paso 3 → createAdmin(): crea admin + autentica, NO toca appState.
   *     Punto de no retorno (el backend rechaza recrear el admin).
   *   - Paso 4 → setup2fa()/verify2fa(): alta real de TOTP (requiere sesión,
   *     que createAdmin ya dejó montada).
   *   - Paso 5 → finishWizard(): appState → 'desktop'.
   *
   * "Atrás" solo en pasos 1-2. Del 3 en adelante, solo hacia delante.
   */
  import { tick } from 'svelte';
  import { createAdmin, setup2fa, verify2fa, finishWizard } from '$lib/stores/auth.js';
  import { setPref } from '$lib/stores/theme.js';
  import NimosLogo from '$lib/ui/NimosLogo.svelte';

  const TOTAL = 5;
  let step = 1;

  // ── Paso 3: admin ──────────────────────────────────────────
  let username = '';
  let password = '';
  let confirm = '';
  let adminError = '';
  let adminLoading = false;

  const RE_USER = /^[a-zA-Z0-9_]{2,32}$/;
  $: userValid = RE_USER.test(username);
  $: pwValid = password.length >= 8 && /[A-Z]/.test(password) && /[0-9]/.test(password);
  $: pwMatch = confirm.length > 0 && password === confirm;
  $: adminReady = userValid && pwValid && pwMatch && !adminLoading;

  // Fuerza (0..4) para las barras
  $: pwScore = (() => {
    let s = 0;
    if (password.length >= 8) s++;
    if (password.length >= 12) s++;
    if (/[A-Z]/.test(password) && /[a-z]/.test(password)) s++;
    if (/[0-9]/.test(password) && /[^A-Za-z0-9]/.test(password)) s++;
    return s;
  })();
  $: barCls = pwScore <= 1 ? 'fill-1' : pwScore === 2 ? 'fill-2' : 'fill-3';
  $: bars = [0, 1, 2, 3].map(i => (i < pwScore ? barCls : ''));

  async function submitAdmin() {
    adminError = '';
    if (!userValid) { adminError = 'Usuario inválido · minúsculas, números y guion bajo · 2-32'; return; }
    if (!pwValid)   { adminError = 'La contraseña no cumple los requisitos'; return; }
    if (!pwMatch)   { adminError = 'Las contraseñas no coinciden'; return; }
    adminLoading = true;
    try {
      await createAdmin(username.trim(), password); // crea admin + autentica (no toca appState)
      step = 4;                                      // punto de no retorno
    } catch (err) {
      adminError = err?.message || 'Error creando el administrador';
    } finally {
      adminLoading = false;
    }
  }

  function onAdminKey(e) {
    if (e.key === 'Enter' && adminReady) submitAdmin();
  }

  // ── Paso 4: 2FA ────────────────────────────────────────────
  let twofaOn = false;                 // estado del toggle
  let twofaPhase = 'choice';           // 'choice' | 'enroll' | 'backup'
  let twofaLoading = false;
  let twofaError = '';
  let secret = '';
  let uri = '';
  let qrSvg = '';
  let otp = ['', '', '', '', '', ''];
  let otpInputs = [];
  let backupCodes = [];
  let twofaResult = 'omitido';         // 'activado' | 'omitido'
  let copiedSecret = false;
  let copiedBackup = false;

  $: otpCode = otp.join('');
  $: otpReady = otpCode.length === 6 && !twofaLoading;

  async function toggle2fa() {
    if (twofaLoading || twofaPhase === 'backup') return;
    if (!twofaOn) {
      twofaOn = true;
      twofaError = '';
      twofaLoading = true;
      try {
        const data = await setup2fa();
        secret = data.secret || '';
        uri = data.uri || '';
        qrSvg = data.qr || '';
        twofaPhase = 'enroll';
        await tick();
        otpInputs[0]?.focus();
      } catch (err) {
        twofaError = err?.message || 'No se pudo iniciar el 2FA';
        twofaOn = false;
        twofaPhase = 'choice';
      } finally {
        twofaLoading = false;
      }
    } else {
      // Apagar → descartar enrolamiento en curso
      twofaOn = false;
      twofaPhase = 'choice';
      twofaError = '';
      otp = ['', '', '', '', '', ''];
    }
  }

  function onOtpInput(i, e) {
    const v = (e.target.value || '').replace(/\D/g, '');
    otp[i] = v.slice(-1);
    otp = [...otp];
    if (otp[i] && i < 5) otpInputs[i + 1]?.focus();
  }
  function onOtpKey(i, e) {
    if (e.key === 'Backspace' && !otp[i] && i > 0) {
      otpInputs[i - 1]?.focus();
    } else if (e.key === 'Enter' && otpReady) {
      verifyOtp();
    }
  }
  function onOtpPaste(e) {
    e.preventDefault();
    const txt = (e.clipboardData?.getData('text') || '').replace(/\D/g, '').slice(0, 6);
    if (!txt) return;
    const arr = ['', '', '', '', '', ''];
    for (let k = 0; k < txt.length; k++) arr[k] = txt[k];
    otp = arr;
    otpInputs[Math.min(txt.length, 5)]?.focus();
  }

  async function verifyOtp() {
    if (!otpReady) return;
    twofaError = '';
    twofaLoading = true;
    try {
      const data = await verify2fa(otpCode);
      backupCodes = data.backupCodes || [];
      twofaResult = 'activado';
      twofaPhase = 'backup';
    } catch (err) {
      twofaError = err?.message || 'Código inválido. Revisa que la app esté sincronizada.';
      otp = ['', '', '', '', '', ''];
      await tick();
      otpInputs[0]?.focus();
    } finally {
      twofaLoading = false;
    }
  }

  function copySecret() {
    try { navigator.clipboard?.writeText(secret); copiedSecret = true; setTimeout(() => copiedSecret = false, 1500); } catch {}
  }
  function copyBackup() {
    try { navigator.clipboard?.writeText(backupCodes.join('\n')); copiedBackup = true; setTimeout(() => copiedBackup = false, 1500); } catch {}
  }

  function continueFrom2fa() {
    if (!twofaOn) twofaResult = 'omitido';
    step = 5;
  }

  // ── Paso 5: resumen ────────────────────────────────────────
  let finishing = false;
  function finish() {
    finishing = true;
    try { setPref('lang', 'es'); } catch {}
    finishWizard(); // appState → 'desktop'
  }
</script>

<div class="setup-screen">
  <div class="corner tl"><span class="corner-led"></span><span>FIRST BOOT</span></div>
  <div class="corner tr">v8.1</div>
  <div class="corner br">STEP {step}/{TOTAL}</div>

  <div class="wrap">
    <!-- Marca -->
    <div class="brand">
      <div class="logo"><NimosLogo size={44} showText={false} variant="white" /></div>
      <div class="brand-text">
        <div class="brand-name">NimOS</div>
        <div class="brand-sub">your <span class="accent">NAS</span> for developers</div>
      </div>
    </div>

    <!-- Stepper -->
    <div class="stepper">
      {#each [1, 2, 3, 4, 5] as n, idx}
        <div class="step" class:active={step === n} class:done={step > n}>
          <span class="step-num">{step > n ? '✓' : n}</span>
        </div>
        {#if idx < 4}
          <span class="step-line" class:done={step > n}></span>
        {/if}
      {/each}
    </div>

    <div class="card">

      <!-- ══ PASO 1 · IDIOMA ══ -->
      {#if step === 1}
        <div class="card-head">
          <div class="card-title">
            <span class="card-title-icon">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="6.5"/><path d="M1.5 8h13M8 1.5c1.8 2 1.8 11 0 13M8 1.5c-1.8 2-1.8 11 0 13"/></svg>
            </span>
            <span>Elige tu idioma</span>
          </div>
          <div class="card-desc">Selecciona el idioma de la interfaz de NimOS.</div>
        </div>

        <div class="lang-list">
          <div class="lang active">
            <div class="lang-code-box">ES</div>
            <div class="lang-body">
              <span class="lang-name">Español</span>
              <span class="lang-meta">es_ES · idioma del sistema</span>
            </div>
            <div class="lang-check"></div>
          </div>
          <div class="lang soon">
            <div class="lang-code-box">EN</div>
            <div class="lang-body">
              <span class="lang-name">English</span>
              <span class="lang-meta">en_US · system language</span>
            </div>
            <span class="lang-soon-badge">beta 9</span>
          </div>
        </div>

        <div class="i18n-hint">
          <svg class="i18n-hint-ico" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="6.5"/><path d="M8 7.5v3.5M8 5h.01"/></svg>
          <span class="i18n-hint-text">
            Beta 8.1 está disponible en <span class="accent">español</span>.
            El <span class="accent">inglés</span> y más idiomas llegarán vía contribuciones de la comunidad.
          </span>
        </div>

        <div class="actions">
          <button class="btn btn-primary" on:click={() => step = 2}>
            Continuar
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8h10M9 4l4 4-4 4"/></svg>
          </button>
        </div>

      <!-- ══ PASO 2 · BIENVENIDA ══ -->
      {:else if step === 2}
        <div class="card-head">
          <div class="card-title">
            <span class="card-title-icon">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2 8l6-5.5L14 8M3.5 7v6.5h9V7"/></svg>
            </span>
            <span>Bienvenido a NimOS</span>
          </div>
          <div class="card-desc">El sistema operativo para tu NAS. <strong style="color:var(--fg-2)">Comprende lo que ocurre. Mantén el control.</strong></div>
        </div>

        <div class="features">
          <div class="feat">
            <div class="feat-ico"><svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2 9l3-1 1.5 3 2-6 1.5 4 2-1h2"/></svg></div>
            <div class="feat-body">
              <div class="feat-name">NimHealth · Task Manager profesional</div>
              <div class="feat-desc">Logs por proceso, start/stop/restart granular, gráficos en tiempo real.</div>
            </div>
          </div>
          <div class="feat">
            <div class="feat-ico"><svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="8" cy="4" rx="5" ry="1.5"/><path d="M3 4v8c0 0.8 2.2 1.5 5 1.5s5-0.7 5-1.5V4"/></svg></div>
            <div class="feat-body">
              <div class="feat-name">Storage · BTRFS denso con info real</div>
              <div class="feat-desc">SMART, scrub, snapshots, multi-pool. Todo a la vista, sin abstracciones.</div>
            </div>
          </div>
          <div class="feat">
            <div class="feat-ico"><svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M8 1.5L2.5 4v4c0 3.5 2.5 5.5 5.5 6.5 3-1 5.5-3 5.5-6.5V4z"/></svg></div>
            <div class="feat-body">
              <div class="feat-name">NimShield · Seguridad transparente</div>
              <div class="feat-desc">Honeypots, blocklist, detección XSS/SQLi. Auditable, sin firewall opaco.</div>
            </div>
          </div>
        </div>

        <div class="actions">
          <button class="btn btn-ghost" on:click={() => step = 1}>← Atrás</button>
          <button class="btn btn-primary" on:click={() => step = 3}>
            Comenzar configuración
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8h10M9 4l4 4-4 4"/></svg>
          </button>
        </div>

      <!-- ══ PASO 3 · ADMIN ══ -->
      {:else if step === 3}
        <div class="card-head">
          <div class="card-title">
            <span class="card-title-icon">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="5" r="3"/><path d="M2.5 14c0-3 2.5-4.5 5.5-4.5s5.5 1.5 5.5 4.5"/></svg>
            </span>
            <span>Crea tu administrador</span>
          </div>
          <div class="card-desc">Esta cuenta tiene control total del sistema. Se crea una sola vez.</div>
        </div>

        <div class="field">
          <div class="field-label"><span class="field-label-text">Nombre de usuario<span class="field-label-req">*</span></span></div>
          <input class="field-input" type="text" bind:value={username} on:keydown={onAdminKey} placeholder="ej: admin" autocomplete="username" spellcheck="false" />
          <div class="field-hint" class:ok={userValid}>minúsculas, números y guion bajo · 2-32 caracteres</div>
        </div>

        <div class="field">
          <div class="field-label"><span class="field-label-text">Contraseña<span class="field-label-req">*</span></span></div>
          <input class="field-input" type="password" bind:value={password} on:keydown={onAdminKey} placeholder="Mínimo 8 caracteres" autocomplete="new-password" />
          <div class="pw-strength">
            {#each bars as b}
              <div class="pw-bar {b}"></div>
            {/each}
          </div>
          <div class="field-hint" class:ok={pwValid}>Mínimo 8 · una mayúscula · un número</div>
        </div>

        <div class="field">
          <div class="field-label"><span class="field-label-text">Confirmar contraseña<span class="field-label-req">*</span></span></div>
          <input class="field-input" type="password" bind:value={confirm} on:keydown={onAdminKey} autocomplete="new-password" />
          <div class="field-hint" class:ok={pwMatch} class:warn={confirm.length > 0 && !pwMatch}>
            {confirm.length === 0 ? 'Repite la contraseña' : (pwMatch ? 'Las contraseñas coinciden' : 'Las contraseñas no coinciden')}
          </div>
        </div>

        {#if adminError}<div class="alert">{adminError}</div>{/if}

        <div class="actions">
          <button class="btn btn-primary" on:click={submitAdmin} disabled={!adminReady}>
            {adminLoading ? 'Creando…' : 'Continuar'}
            {#if !adminLoading}<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8h10M9 4l4 4-4 4"/></svg>{/if}
          </button>
        </div>

      <!-- ══ PASO 4 · 2FA ══ -->
      {:else if step === 4}
        <div class="card-head">
          <div class="card-title">
            <span class="card-title-icon">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M8 1.5L2.5 4v4c0 3.5 2.5 5.5 5.5 6.5 3-1 5.5-3 5.5-6.5V4z"/><path d="M6 8l1.5 1.5L10 7"/></svg>
            </span>
            <span>Autenticación de dos factores</span>
          </div>
          <div class="card-desc">Recomendado para administradores. Necesitarás una app como Google Authenticator, Authy o 1Password.</div>
        </div>

        <div class="opt-toggle">
          <div class="opt-toggle-body">
            <span class="opt-toggle-name">Activar 2FA TOTP</span>
            <span class="opt-toggle-desc">{twofaPhase === 'backup' ? 'Activado · guarda tus códigos abajo' : 'Puedes activarlo o desactivarlo después en Ajustes'}</span>
          </div>
          <div class="switch" class:off={!twofaOn} role="switch" tabindex="0" aria-checked={twofaOn}
               on:click={toggle2fa} on:keydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), toggle2fa())}></div>
        </div>

        {#if twofaPhase === 'enroll'}
          <div class="qr-section">
            <div class="qr-box">
              {#if qrSvg}
                {@html qrSvg}
              {:else}
                <div class="qr-fallback">
                  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><rect x="1.5" y="1.5" width="5" height="5" rx="1"/><rect x="9.5" y="1.5" width="5" height="5" rx="1"/><rect x="1.5" y="9.5" width="5" height="5" rx="1"/><path d="M9.5 9.5h2v2M14.5 9.5v5M9.5 14.5h2"/></svg>
                  <span>Introduce la clave manualmente ↓</span>
                </div>
              {/if}
            </div>
            <div class="qr-meta">
              <div class="qr-step"><span class="qr-step-num">1</span><span class="qr-step-text">Abre tu app de autenticación en el móvil.</span></div>
              <div class="qr-step"><span class="qr-step-num">2</span><span class="qr-step-text">{qrSvg ? 'Escanea el QR o introduce la clave manualmente.' : 'Introduce la clave de abajo manualmente.'}</span></div>
              <div class="qr-step"><span class="qr-step-num">3</span><span class="qr-step-text">Verifica con el código de <span class="accent">6 dígitos</span> que genere.</span></div>
            </div>
          </div>

          <div class="secret-row">
            <span class="secret-lbl">SECRET:</span>
            <span class="secret-val">{secret}</span>
            <span class="secret-copy" role="button" tabindex="0" on:click={copySecret} on:keydown={(e) => e.key === 'Enter' && copySecret()}>{copiedSecret ? '✓ COPIADO' : 'COPIAR'}</span>
          </div>

          <span class="otp-label">Verificar código del autenticador</span>
          <div class="otp-grid">
            {#each otp as d, i}
              <input class="otp-digit" maxlength="1" inputmode="numeric" autocomplete="off"
                     bind:this={otpInputs[i]} value={d}
                     on:input={(e) => onOtpInput(i, e)} on:keydown={(e) => onOtpKey(i, e)} on:paste={onOtpPaste} />
            {/each}
          </div>
          <div class="otp-hint">EL CÓDIGO ROTA CADA 30 SEGUNDOS</div>
          {#if twofaError}<div class="alert">{twofaError}</div>{/if}

        {:else if twofaPhase === 'backup'}
          <div class="backup">
            <div class="backup-head">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M8 1.5L2.5 4v4c0 3.5 2.5 5.5 5.5 6.5 3-1 5.5-3 5.5-6.5V4z"/></svg>
              <span>Códigos de recuperación</span>
            </div>
            <div class="backup-warn">Guárdalos en un lugar seguro. Cada uno sirve una vez si pierdes el acceso al autenticador. <strong>No se volverán a mostrar.</strong></div>
            <div class="backup-grid">
              {#each backupCodes as c}<span class="backup-code">{c}</span>{/each}
            </div>
            <span class="secret-copy backup-copy" role="button" tabindex="0" on:click={copyBackup} on:keydown={(e) => e.key === 'Enter' && copyBackup()}>{copiedBackup ? '✓ COPIADOS' : 'COPIAR TODOS'}</span>
          </div>
        {:else}
          <div class="twofa-hint">El 2FA añade una capa extra de seguridad al inicio de sesión. Actívalo arriba o continúa y configúralo más tarde.</div>
          {#if twofaError}<div class="alert">{twofaError}</div>{/if}
        {/if}

        <div class="actions">
          {#if twofaPhase === 'enroll'}
            <button class="btn btn-primary" on:click={verifyOtp} disabled={!otpReady}>
              {twofaLoading ? 'Verificando…' : 'Verificar y continuar'}
              {#if !twofaLoading}<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8h10M9 4l4 4-4 4"/></svg>{/if}
            </button>
          {:else if twofaPhase === 'backup'}
            <button class="btn btn-primary" on:click={() => step = 5}>
              Continuar
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8h10M9 4l4 4-4 4"/></svg>
            </button>
          {:else}
            <button class="btn btn-primary" on:click={continueFrom2fa} disabled={twofaLoading}>
              {twofaLoading ? 'Generando…' : 'Continuar'}
              {#if !twofaLoading}<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8h10M9 4l4 4-4 4"/></svg>{/if}
            </button>
          {/if}
        </div>

      <!-- ══ PASO 5 · RESUMEN ══ -->
      {:else if step === 5}
        <div class="card-head">
          <div class="card-title">
            <span class="card-title-icon">
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="6.5"/><path d="M5 8l2 2 4-4.5"/></svg>
            </span>
            <span>Todo listo</span>
          </div>
          <div class="card-desc">Revisa la configuración y arranca NimOS.</div>
        </div>

        <div class="summary">
          <div class="summary-title">Resumen</div>
          <div class="summary-row"><span class="summary-lbl">Idioma</span><span class="summary-val">Español · es</span></div>
          <div class="summary-row"><span class="summary-lbl">Usuario admin</span><span class="summary-val">{username}</span></div>
          <div class="summary-row"><span class="summary-lbl">2FA TOTP</span><span class="summary-val" class:ok={twofaResult === 'activado'}>{twofaResult === 'activado' ? 'Activado' : 'Omitido'}</span></div>
        </div>

        <div class="actions">
          <button class="btn btn-primary" on:click={finish} disabled={finishing}>
            {finishing ? 'Arrancando…' : 'Iniciar NimOS'}
            {#if !finishing}<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8h10M9 4l4 4-4 4"/></svg>{/if}
          </button>
        </div>
      {/if}

    </div>

    <div class="sysinfo">
      <span>NimOS BETA 8.1 · SETUP WIZARD</span>
      <span>FIRST BOOT</span>
    </div>
  </div>
</div>

<style>
  .setup-screen{position:fixed;inset:0;overflow-y:auto;display:flex;padding:20px;
    background:radial-gradient(ellipse at top, rgba(0,255,159,0.04) 0%, transparent 50%), #0a0a0c;
    color:var(--fg);font-family:ui-sans-serif,system-ui,-apple-system,sans-serif;z-index:1}
  .setup-screen::before{content:'';position:fixed;inset:0;pointer-events:none;
    background-image:linear-gradient(rgba(255,255,255,0.012) 1px, transparent 1px),linear-gradient(90deg, rgba(255,255,255,0.012) 1px, transparent 1px);
    background-size:40px 40px}

  .corner{position:fixed;font-family:ui-monospace,monospace;font-size:9px;color:var(--fg-5);letter-spacing:0.8px;text-transform:uppercase;z-index:2}
  .corner.tl{top:20px;left:20px;display:flex;align-items:center;gap:8px}
  .corner.tr{top:20px;right:20px}
  .corner.br{bottom:20px;right:20px}
  .corner-led{width:6px;height:6px;border-radius:1.5px;background:var(--st-warn);box-shadow:0 0 5px var(--st-warn);animation:pulse 2s ease-in-out infinite}
  @keyframes pulse{0%,100%{opacity:1}50%{opacity:0.5}}

  .wrap{position:relative;z-index:1;width:100%;max-width:540px;margin:auto;display:flex;flex-direction:column;gap:24px}

  .brand{display:flex;flex-direction:column;align-items:center;gap:10px}
  .logo{display:flex}
  .brand-text{display:flex;flex-direction:column;align-items:center;gap:4px}
  .brand-name{font-size:18px;font-weight:600}
  .brand-sub{font-family:ui-monospace,monospace;font-size:10px;color:var(--fg-4);letter-spacing:0.4px}
  .brand-sub .accent{color:var(--nim-green)}

  .stepper{display:flex;align-items:center;justify-content:center;gap:6px;padding:8px 0}
  .step{display:flex;align-items:center;gap:6px}
  .step-num{width:20px;height:20px;border-radius:5px;background:var(--bg-card);border:1px solid var(--bd-3);font-family:ui-monospace,monospace;font-size:10px;color:var(--fg-4);display:flex;align-items:center;justify-content:center;font-weight:600}
  .step.active .step-num{background:var(--nim-green);color:var(--bg-window);border-color:var(--nim-green)}
  .step.done .step-num{background:rgba(0,255,159,0.15);color:var(--nim-green);border-color:rgba(0,255,159,0.3)}
  .step-line{width:24px;height:1px;background:var(--bd-3)}
  .step-line.done{background:rgba(0,255,159,0.3)}

  .card{background:var(--bg-card);border:1px solid var(--bd-2);border-radius:14px;padding:24px 24px 20px;box-shadow:0 20px 60px rgba(0,0,0,0.4)}
  .card-head{margin-bottom:16px;padding-bottom:14px;border-bottom:1px solid var(--bd-2)}
  .card-title{font-size:16px;font-weight:500;display:flex;align-items:center;gap:9px}
  .card-title-icon{width:24px;height:24px;border-radius:6px;background:var(--bg-inner);display:flex;align-items:center;justify-content:center;color:var(--nim-green);flex-shrink:0}
  .card-title-icon svg{width:13px;height:13px}
  .card-desc{font-size:11px;color:var(--fg-4);line-height:1.5;margin-top:8px}

  /* Idioma */
  .lang-list{display:flex;flex-direction:column;gap:6px}
  .lang{display:flex;align-items:center;gap:14px;padding:14px 16px;background:var(--bg-inner);border:1px solid var(--bd-3);border-radius:8px;cursor:pointer;transition:border-color 0.15s,background 0.15s}
  .lang.active{border-color:var(--nim-green);background:rgba(0,255,159,0.04)}
  .lang-code-box{width:38px;height:38px;border-radius:6px;background:var(--bg-card);display:flex;align-items:center;justify-content:center;font-family:ui-monospace,monospace;font-size:12px;font-weight:700;color:var(--fg-3);flex-shrink:0;border:1px solid var(--bd-2);letter-spacing:0.4px}
  .lang.active .lang-code-box{color:var(--nim-green);border-color:rgba(0,255,159,0.25);background:rgba(0,255,159,0.04)}
  .lang-body{display:flex;flex-direction:column;gap:2px;flex:1;min-width:0}
  .lang-name{font-size:13px;color:var(--fg);font-weight:500}
  .lang-meta{font-family:ui-monospace,monospace;font-size:10px;color:var(--fg-5);letter-spacing:0.3px}
  .lang-check{width:16px;height:16px;border-radius:4px;border:1px solid var(--bd-3);background:var(--bg-card);display:flex;align-items:center;justify-content:center;color:var(--bg-window);flex-shrink:0}
  .lang.active .lang-check{background:var(--nim-green);border-color:var(--nim-green)}
  .lang.active .lang-check::after{content:'✓';font-size:10px;font-weight:700}
  .lang.soon{opacity:0.5;cursor:not-allowed}
  .lang-soon-badge{font-family:ui-monospace,monospace;font-size:8px;font-weight:700;letter-spacing:0.6px;text-transform:uppercase;color:var(--st-info);border:1px solid rgba(77,184,255,0.3);border-radius:3px;padding:2px 5px;flex-shrink:0}
  .i18n-hint{margin-top:14px;padding:10px 12px;background:var(--bg-inner);border:1px dashed var(--bd-3);border-radius:6px;display:flex;align-items:flex-start;gap:9px}
  .i18n-hint-ico{flex-shrink:0;color:var(--st-info);width:14px;height:14px;margin-top:1px}
  .i18n-hint-text{font-size:10px;color:var(--fg-4);line-height:1.5}
  .i18n-hint-text .accent{color:var(--fg-2)}

  /* Features (welcome) */
  .features{display:flex;flex-direction:column;gap:10px}
  .feat{display:flex;align-items:flex-start;gap:12px;padding:10px 12px;background:var(--bg-inner);border:1px solid var(--bd);border-radius:8px}
  .feat-ico{width:28px;height:28px;border-radius:6px;background:var(--bg-card);display:flex;align-items:center;justify-content:center;color:var(--nim-green);flex-shrink:0}
  .feat-ico svg{width:14px;height:14px}
  .feat-body{display:flex;flex-direction:column;gap:2px;flex:1}
  .feat-name{font-size:12px;color:var(--fg);font-weight:500}
  .feat-desc{font-size:10px;color:var(--fg-4);line-height:1.4}

  /* Campos (admin) */
  .field{display:flex;flex-direction:column;gap:6px;margin-bottom:14px}
  .field-label{display:flex;align-items:center;justify-content:space-between}
  .field-label-text{font-family:ui-monospace,monospace;font-size:9px;color:var(--fg-4);letter-spacing:0.6px;text-transform:uppercase;font-weight:500}
  .field-label-req{color:var(--st-crit);margin-left:3px}
  .field-input{background:var(--bg-inner);border:1px solid var(--bd-3);color:var(--fg);padding:10px 12px;border-radius:6px;font-size:13px;font-family:ui-monospace,monospace;outline:none;width:100%}
  .field-input:focus{border-color:var(--ui-select, #7a9eb1);background:#0d0d11}
  .field-input::placeholder{color:var(--fg-5)}
  .field-hint{font-size:10px;color:var(--fg-5);font-family:ui-monospace,monospace;letter-spacing:0.3px;margin-top:2px}
  .field-hint.ok{color:var(--st-ok)}
  .field-hint.warn{color:var(--st-warn)}
  .pw-strength{display:flex;gap:3px;margin-top:6px}
  .pw-bar{flex:1;height:3px;border-radius:1px;background:var(--bd-3)}
  .pw-bar.fill-1{background:var(--st-crit)}
  .pw-bar.fill-2{background:var(--st-warn)}
  .pw-bar.fill-3{background:var(--st-ok)}

  /* 2FA */
  .opt-toggle{display:flex;align-items:center;justify-content:space-between;padding:12px 14px;background:var(--bg-inner);border:1px solid var(--bd);border-radius:8px;margin-bottom:18px}
  .opt-toggle-body{display:flex;flex-direction:column;gap:2px}
  .opt-toggle-name{font-size:12px;color:var(--fg);font-weight:500}
  .opt-toggle-desc{font-size:10px;color:var(--fg-4)}
  .switch{width:28px;height:16px;background:var(--nim-green);border-radius:3px;position:relative;cursor:pointer;flex-shrink:0;transition:background 0.15s}
  .switch::after{content:'';position:absolute;top:2px;right:2px;width:12px;height:12px;background:var(--bg-window);border-radius:2px;transition:right 0.15s,left 0.15s}
  .switch.off{background:var(--bd-3)}
  .switch.off::after{right:14px}

  .twofa-hint{font-size:11px;color:var(--fg-4);line-height:1.5;padding:2px 0 4px}

  .qr-section{display:flex;gap:18px;margin-bottom:16px}
  .qr-box{background:#fff;border-radius:10px;padding:12px;display:flex;align-items:center;justify-content:center;flex-shrink:0;border:1px solid var(--bd-2);width:174px;height:174px}
  .qr-box :global(svg){display:block;width:150px;height:150px}
  .qr-fallback{display:flex;flex-direction:column;align-items:center;gap:8px;color:#555;font-family:ui-monospace,monospace;font-size:9px;text-align:center;line-height:1.4}
  .qr-fallback svg{width:42px;height:42px}
  .qr-meta{flex:1;display:flex;flex-direction:column;gap:10px;min-width:0;justify-content:center}
  .qr-step{display:flex;gap:9px;align-items:flex-start}
  .qr-step-num{width:18px;height:18px;border-radius:4px;background:var(--bg-inner);font-family:ui-monospace,monospace;font-size:9px;color:var(--fg-3);display:flex;align-items:center;justify-content:center;flex-shrink:0;border:1px solid var(--bd-3);font-weight:700}
  .qr-step-text{font-size:11px;color:var(--fg-2);line-height:1.45}
  .qr-step-text .accent{color:var(--nim-green);font-family:ui-monospace,monospace}

  .secret-row{display:flex;align-items:center;gap:8px;background:var(--bg-inner);border:1px dashed var(--bd-3);border-radius:6px;padding:9px 12px;margin-bottom:16px}
  .secret-lbl{font-family:ui-monospace,monospace;font-size:9px;color:var(--fg-5);letter-spacing:0.6px;flex-shrink:0}
  .secret-val{flex:1;font-family:ui-monospace,monospace;font-size:11px;color:var(--fg-2);letter-spacing:0.4px;word-break:break-all}
  .secret-copy{font-family:ui-monospace,monospace;font-size:9px;color:var(--ui-select, #7a9eb1);cursor:pointer;letter-spacing:0.4px;padding:3px 7px;border:1px solid var(--bd-3);border-radius:4px;flex-shrink:0;white-space:nowrap}
  .secret-copy:hover{background:rgba(122,158,177,0.05)}

  .otp-label{font-family:ui-monospace,monospace;font-size:9px;color:var(--fg-4);letter-spacing:0.6px;text-transform:uppercase;font-weight:500;margin-bottom:8px;display:block}
  .otp-grid{display:grid;grid-template-columns:repeat(6,1fr);gap:6px;margin-bottom:6px}
  .otp-digit{background:var(--bg-inner);border:1px solid var(--bd-3);border-radius:6px;color:var(--fg);font-family:ui-monospace,monospace;font-size:18px;font-weight:600;text-align:center;padding:10px 0;outline:none;min-width:0}
  .otp-digit:focus{border-color:var(--nim-green)}
  .otp-hint{font-family:ui-monospace,monospace;font-size:9px;color:var(--fg-5);letter-spacing:0.4px;text-align:center;margin-top:4px}

  .backup{background:var(--bg-inner);border:1px solid rgba(0,255,159,0.2);border-radius:8px;padding:14px;margin-bottom:4px}
  .backup-head{display:flex;align-items:center;gap:8px;font-size:12px;font-weight:600;color:var(--nim-green);margin-bottom:8px}
  .backup-head svg{width:14px;height:14px}
  .backup-warn{font-size:10px;color:var(--fg-4);line-height:1.5;margin-bottom:12px}
  .backup-warn strong{color:var(--st-warn)}
  .backup-grid{display:grid;grid-template-columns:repeat(2,1fr);gap:6px;margin-bottom:12px}
  .backup-code{font-family:ui-monospace,monospace;font-size:12px;color:var(--fg-2);background:var(--bg-card);border:1px solid var(--bd-2);border-radius:5px;padding:7px 10px;text-align:center;letter-spacing:1px}
  .backup-copy{display:inline-block}

  .alert{background:rgba(255,90,90,0.08);border:1px solid rgba(255,90,90,0.3);color:var(--st-crit);font-size:11px;border-radius:6px;padding:9px 12px;margin-bottom:6px;line-height:1.4}

  .actions{display:flex;gap:10px;margin-top:18px;padding-top:18px;border-top:1px solid var(--bd-2)}
  .btn{padding:11px 14px;border-radius:7px;font-family:ui-monospace,monospace;font-size:11px;font-weight:600;letter-spacing:0.7px;text-transform:uppercase;cursor:pointer;border:1px solid;display:flex;align-items:center;justify-content:center;gap:8px}
  .btn-primary{background:var(--nim-green);color:var(--bg-window);border-color:var(--nim-green);flex:1}
  .btn-primary:hover{background:#00e08b}
  .btn-primary:disabled{opacity:0.45;cursor:not-allowed}
  .btn-primary svg{width:13px;height:13px}
  .btn-ghost{background:transparent;color:var(--fg-3);border-color:var(--bd-3);flex:0 0 90px}
  .btn-ghost:hover{color:var(--fg);border-color:#4a4a52}

  /* Resumen */
  .summary{background:var(--bg-inner);border:1px solid var(--bd);border-radius:8px;padding:12px 14px}
  .summary-title{font-family:ui-monospace,monospace;font-size:9px;color:var(--fg-4);letter-spacing:0.6px;text-transform:uppercase;font-weight:500;margin-bottom:8px}
  .summary-row{display:flex;justify-content:space-between;align-items:center;padding:5px 0;font-family:ui-monospace,monospace;font-size:11px}
  .summary-row + .summary-row{border-top:1px solid var(--bd)}
  .summary-lbl{color:var(--fg-4);letter-spacing:0.3px}
  .summary-val{color:var(--fg-2)}
  .summary-val.ok{color:var(--st-ok);display:flex;align-items:center;gap:5px}
  .summary-val.ok::before{content:'';width:5px;height:5px;background:var(--st-ok);border-radius:1.5px;box-shadow:0 0 3px var(--st-ok)}

  .sysinfo{display:flex;justify-content:space-between;font-family:ui-monospace,monospace;font-size:9px;color:var(--fg-5);letter-spacing:0.4px;padding:0 4px}

  @media (max-width:480px){
    .qr-section{flex-direction:column;align-items:center}
    .qr-meta{width:100%}
  }
</style>
