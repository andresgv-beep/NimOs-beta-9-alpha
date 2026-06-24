<script>
  /**
   * NetworkKPIs · Banner de 4 KPIs de la sección Exposición.
   * ─────────────────────────────────────────────────────────
   * Expuestas · Caddy · Certificados · Dominio
   *
   * Deriva los valores de las props (apps + certs + config).
   *
   * Props:
   *   · apps   — array de apps expuestas
   *   · certs  — snapshot del observer { reachable, certs:[...] } | null
   *   · config — { base_domain, enabled }
   */
  import { StatCard } from '$lib/ui';

  export let apps = [];
  export let certs = null;
  export let config = { base_domain: '', enabled: false };

  $: exposedCount = apps.filter((a) => a.enabled).length;
  $: caddyReachable = certs ? certs.reachable : null;
  $: certCount = certs?.certs?.length || 0;
  $: certsExpiringSoon = (certs?.certs || []).filter(
    (c) => typeof c.days_left === 'number' && c.days_left < 15
  ).length;

  $: caddyState = caddyReachable === null ? 'desconocido' : caddyReachable ? 'online' : 'sin respuesta';
  $: caddyVariant = caddyReachable === null ? 'warn' : caddyReachable ? 'ok' : 'crit';

  $: domainState = !config.base_domain ? 'sin configurar' : config.enabled ? 'activo' : 'pausado';
  $: domainVariant = !config.base_domain ? 'warn' : config.enabled ? 'ok' : 'warn';
</script>

<div class="nx-kpis">
  <StatCard
    label="Expuestas"
    value={exposedCount}
    variant={exposedCount > 0 ? 'ok' : 'warn'}
    tag={apps.length > exposedCount ? `${apps.length - exposedCount} pausadas` : 'todas activas'}
    tagVariant={exposedCount > 0 ? 'ok' : 'warn'}
  />
  <StatCard
    label="Caddy"
    value={caddyReachable === null ? '—' : caddyReachable ? 'OK' : 'OFF'}
    variant={caddyReachable ? 'ok' : caddyReachable === false ? 'crit' : 'warn'}
    tag={caddyState}
    tagVariant={caddyVariant}
  />
  <StatCard
    label="Certificados"
    value={certCount}
    variant={certsExpiringSoon > 0 ? 'warn' : 'ok'}
    tag={certsExpiringSoon > 0 ? `${certsExpiringSoon} por expirar` : certCount > 0 ? 'todos válidos' : '—'}
    tagVariant={certsExpiringSoon > 0 ? 'warn' : 'ok'}
  />
  <StatCard
    label="Dominio"
    value={config.base_domain || '—'}
    variant={domainVariant}
    tag={domainState}
    tagVariant={domainVariant}
    valueColored={false}
  >
    <span class="nx-dom-spacer"></span>
  </StatCard>
</div>

<style>
  .nx-kpis {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 8px;
  }
  @media (min-width: 600px) {
    .nx-kpis { grid-template-columns: repeat(4, 1fr); }
  }
  /* El dominio puede ser largo: reducir y truncar su valor mono. */
  .nx-kpis :global(.stat-card:last-child .sc-val) {
    font-size: 14px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .nx-dom-spacer { display: none; }
</style>
