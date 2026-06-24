<script>
  /**
   * LED · Indicador pulsante circular
   * ───────────────────────────────────
   * Uso: <LED /> · <LED variant="warn" /> · <LED variant="crit" size={10} />
   *
   * Variantes:
   *   - ok (default): verde accent, pulso lento 1.6s
   *   - warn:         ámbar, pulso medio 1.2s
   *   - crit:         rojo, pulso rápido 0.8s
   *   - off:          gris apagado, sin pulso
   *
   * Props:
   *   - variant: 'ok' | 'warn' | 'crit' | 'off'
   *   - size:    número en px (default 8)
   *   - pulse:   boolean (default true) — si false, el LED está encendido estático
   */
  export let variant = 'ok';
  export let size = 8;
  export let pulse = true;
</script>

<span
  class="led"
  class:warn={variant === 'warn'}
  class:crit={variant === 'crit'}
  class:off={variant === 'off'}
  class:static={!pulse}
  style="--led-size:{size}px"
></span>

<style>
  .led {
    display: inline-block;
    width: var(--led-size, 8px);
    height: var(--led-size, 8px);
    border-radius: 50%;
    background: var(--accent);
    box-shadow: 0 0 6px var(--accent-glow), 0 0 2px var(--accent);
    animation: led-pulse 1.6s ease-in-out infinite;
    flex-shrink: 0;
    vertical-align: middle;
  }
  .led.warn {
    background: var(--warn);
    box-shadow: 0 0 6px rgba(255, 184, 0, 0.45);
    animation: led-pulse-warn 1.2s ease-in-out infinite;
  }
  .led.crit {
    background: var(--crit);
    box-shadow: 0 0 6px rgba(255, 90, 90, 0.5);
    animation: led-pulse-crit 0.8s ease-in-out infinite;
  }
  .led.off {
    background: var(--fg-mute);
    box-shadow: none;
    animation: none;
    opacity: 0.5;
  }
  .led.static {
    animation: none;
  }
</style>
