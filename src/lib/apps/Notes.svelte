<script>
  /**
   * Notes · Stub temporal de migración
   * ──────────────────────────────────────────
   * Esta app está pendiente de portar desde Beta 7 al lenguaje v3.
   * Ver README.md para el plan y orden de migración.
   *
   * Código original: NimOs-beta-7-main/src/lib/apps/Notes.svelte
   */
  import AppShell from '$lib/components/AppShell.svelte';
  import SectionHead from '$lib/ui/SectionHead.svelte';

  let active = 'overview';
</script>

<AppShell
  appId="notes"
  title="Notes"
  headerIcon="✎"
  pathSegments={['notes']}
  sections={[
    {
      label: 'General',
      items: [
        { id: 'overview', label: 'Resumen', keyHint: 'R' },
      ],
    },
  ]}
  bind:active
>
  <div class="stub">
    <SectionHead>Notes · estado de migración</SectionHead>

    <div class="stub-body">
      <div class="stub-head">
        <span class="stub-tag">WIP</span>
        <span class="stub-title">Pendiente de migrar</span>
      </div>
      <p>
        Esta app existe como versión funcional en <b>Beta 7</b> pero aún no
        ha sido portada al lenguaje visual v3 de NimOS.
      </p>
      <p>
        El backend (daemon Go) sigue siendo el mismo: todas las APIs
        están operativas. Solo falta la capa de presentación.
      </p>
      <div class="roadmap">
        <div class="rm-line">
          <span class="rm-tag">ref</span>
          <span>NimOs-beta-7-main/src/lib/apps/Notes.svelte</span>
        </div>
        <div class="rm-line">
          <span class="rm-tag">next</span>
          <span>Portar template HTML · mantener &lt;script&gt; como base</span>
        </div>
      </div>
    </div>
  </div>
</AppShell>

<style>
  .stub {
    padding: 24px 28px;
  }
  .stub-body {
    margin-top: 18px;
    padding: 20px 24px;
    background: var(--bg-1);
    border: 1px solid var(--border);
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--fg-dim);
    line-height: 1.7;
    max-width: 640px;
  }
  .stub-head {
    display: flex;
    align-items: center;
    gap: 10px;
    padding-bottom: 12px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 14px;
  }
  .stub-tag {
    background: var(--warn);
    color: var(--bg);
    padding: 2px 8px;
    font-size: 9px;
    font-weight: 700;
    letter-spacing: 1.5px;
  }
  .stub-title {
    font-size: 12px;
    color: var(--fg);
    font-weight: 600;
    letter-spacing: 0.5px;
  }
  p { margin: 10px 0; }
  p b { color: var(--accent); }

  .roadmap {
    margin-top: 16px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .rm-line {
    display: flex;
    gap: 10px;
    align-items: center;
    font-size: 10px;
    color: var(--fg-mute);
  }
  .rm-tag {
    font-size: 8px;
    border: 1px solid var(--border);
    padding: 1px 5px;
    color: var(--accent);
    letter-spacing: 1px;
    text-transform: uppercase;
  }
</style>
