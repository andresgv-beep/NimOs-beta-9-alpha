<script>
  /**
   * Footer · Footer denso de ventana con metadatos
   * ────────────────────────────────────────────────
   * Barra inferior de 26px con k/v pairs y separadores.
   *
   * Uso:
   *   <Footer>
   *     <FooterItem k="events" v="142" />
   *     <FooterItem k="blocked" v="1,284" />
   *     <FooterItem k="daemon" status="ok" />
   *     <div slot="right">...</div>
   *   </Footer>
   *
   * Alternativa simple con markup directo:
   *   <Footer>
   *     <span class="k">items:</span> <span class="v">142</span>
   *   </Footer>
   */
</script>

<div class="footer">
  <div class="left">
    <slot />
  </div>
  <div class="right">
    <slot name="right" />
  </div>
</div>

<style>
  .footer {
    height: 26px;
    border-top: 1px solid var(--border);
    background: var(--bg-1);
    display: flex;
    align-items: center;
    padding: 0 14px;
    font-family: var(--font-mono);
    font-size: 9px;
    color: var(--fg-mute);
    letter-spacing: 0.8px;
    flex-shrink: 0;
  }
  .left, .right {
    display: flex;
    align-items: center;
    gap: 14px;
  }
  .left { flex: 1; }
  .right { margin-left: auto; }

  :global(.footer .k)   { color: var(--fg-faint); }
  :global(.footer .v)   { color: var(--fg-dim); font-feature-settings: "tnum"; }
  :global(.footer .sep) { color: var(--fg-faint); }
</style>
