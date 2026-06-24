<script>
  /**
   * ServiceWebDAV · placeholder (vive bajo la barra de pestañas de CPServices)
   * WebDAV no existe aún en el backend.
   */
</script>

<div class="svc-pane">
  <div class="sp-soon">
    <div class="sp-soon-icon"></div>
    <div class="sp-soon-title">WebDAV todavía no está disponible</div>
    <div class="sp-soon-desc">
      El acceso a archivos por HTTP/WebDAV se añadirá en una versión futura. Permitirá montar
      tus carpetas compartidas desde el explorador de archivos de cualquier sistema sin software extra.
    </div>
  </div>
</div>

<style>
  .svc-pane { display: flex; flex-direction: column; }
  .sp-soon { display: flex; flex-direction: column; align-items: center; text-align: center; gap: 10px; padding: 50px 24px; }
  .sp-soon-icon { width: 36px; height: 36px; border-radius: 9px; background: var(--bg-card, #15151a); border: 1px solid var(--bd-3, #2a2a32); margin-bottom: 4px; }
  .sp-soon-title { font-size: 14px; color: var(--fg-2, #d0d0d4); font-family: var(--font-mono); }
  .sp-soon-desc { font-size: 11px; color: var(--fg-5, #5a5a62); font-family: var(--font-mono); line-height: 1.6; max-width: 420px; }
</style>
