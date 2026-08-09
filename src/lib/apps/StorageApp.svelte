<script>
  /**
   * StorageApp · Gestión de almacenamiento (v3 · Fase A MVP)
   * ────────────────────────────────────────────────────────────
   * Migración desde Beta 7 siguiendo mockup "nimos-storage-retro.html".
   *
   * Scope Fase A:
   *   - Listar pools (ZFS + BTRFS) con info rica
   *   - Pantalla inteligente: banner si hay pools restaurables sin montar
   *   - Restaurar pool existente (flujo crítico · tu caso)
   *   - Ver discos con SMART status
   *   - Snapshots (listar)
   *   - Scrub manual
   *   - Escaneo de discos
   *
   * Fase B (pendiente):
   *   - Crear pool nuevo (+ wizard selector vdev)
   *   - Add/remove/replace disco
   *   - Exportar/destruir pool
   *   - Snapshots (crear/rollback/borrar)
   *   - Datasets
   *
   * TODO backend (gaps actuales):
   *   - Temperatura disco y horas operación (no se exponen en JSON)
   *   - Breakdown por categoría para donut (solo total used/available)
   *   - Rol disco (data/parity) en RAIDZ (inferible en frontend)
   *
   * Backend endpoints (Beta 8 v2):
   *   GET  /api/storage/v2/pools
   *   GET  /api/storage/v2/status
   *   GET  /api/storage/v2/disks
   *   GET  /api/storage/v2/alerts
   *   GET  /api/storage/v2/capabilities
   *   GET  /api/storage/v2/observed
   *   GET  /api/storage/v2/snapshots?pool=X
   *   POST /api/storage/v2/scan
   *   POST /api/storage/v2/scrub
   *   POST /api/storage/v2/wipe
   *   POST /api/storage/v2/pools/import
   *   POST /api/storage/v2/pool/export
   *   POST /api/storage/v2/pool/destroy
   */
  import { onMount, onDestroy } from 'svelte';
  import { token } from '$lib/stores/auth.js';
  import AppShell from '$lib/components/AppShell.svelte';
  import { Spinner, ConfirmDialog } from '$lib/ui';
  import ExportPoolWizard from './storage/ExportPoolWizard.svelte';

  import CreatePoolWizard from './storage/CreatePoolWizard.svelte';
  import ImportOrphanModal from './storage/ImportOrphanModal.svelte';
  import DestroyOrphanModal from './storage/DestroyOrphanModal.svelte';
  import UpgradeRaidModal from './storage/UpgradeRaidModal.svelte';
  import StorageScrub from './storage/StorageScrub.svelte';
  import StorageSmart from './storage/StorageSmart.svelte';
  import StorageSnapshots from './storage/StorageSnapshots.svelte';
  import StorageDisks from './storage/StorageDisks.svelte';
  import StorageKPIs from './storage/StorageKPIs.svelte';
  import StorageOverview from './storage/StorageOverview.svelte';
  import * as api from './storage/api.js';
  import { fmtBytes } from './storage/formatters.js';
  import './storage/views-styles.css';

  // ─── State ───
  let active = 'overview'; // 'overview' | 'disks' | 'snapshots' | 'scrub' | 'smart'

  // Export pool wizard state (UI lo llama "Desmontar", backend lo llama "export")
  let exportPoolName = null;   // nombre del pool a desmontar (null = wizard cerrado)

  // Destroy pool wizard state (destrucción definitiva · 3 pasos)

  // Create pool wizard state
  let creatingPool = false;  // true = wizard abierto
  let upgradingPool = null;  // pool en proceso de upgrade a RAID1 (modal abierto)

  // Limpiar disco (wipe)
  let wipeDisk = null;         // path del disco a formatear (null = dialog cerrado)
  let wipeSerial = '';         // serial esperado del disco (AUDIT F10)
  let wipeProcessing = false;
  let wipeError = '';

  let pools = [];
  let disks = {};
  let alerts = [];
  let capabilities = {};
  let status = {};
  let connError = false; // AUDIT F5: el último loadAll no pudo leer /pools

  // Fase 7 Bloque C3.2: Observed state
  //
  // observedSnapshot: el snapshot completo del observer (con generation, etc.)
  // orphanFilesystems: filesystems BTRFS detectados que NO pertenecen a un pool
  //                    managed. Estos son los candidatos a "Importar" o "Destruir".
  // divergences: lista pre-computada de problemas (pool_missing_device, io_errors...)
  let observedSnapshot = {};
  let orphanFilesystems = [];
  let divergences = [];

  // Modales (cada componente gestiona su propio estado interno).
  // El padre solo guarda QUÉ modal está abierto y CON QUÉ datos.
  let importingFS = null;       // ObservedBtrfs a importar (null = cerrado)
  let destroyingOrphan = null;  // ObservedBtrfs a destruir (null = cerrado)

  // Scrub
  let scrubbing = {};
  let scrubMsg = '';

  // Scan
  let scanning = false;

  // Snapshots (por pool)
  let snapshots = {};

  let loading = true;
  let pollInterval;

  // ─── Derived ───
  $: hasPools = pools.length > 0;

  // Metadata de cada vista para el page-header
  const VIEW_META = {
    overview:  { title: 'Resumen',    desc: 'volúmenes activos del sistema' },
    disks:     { title: 'Discos',     desc: 'dispositivos físicos del sistema' },
    snapshots: { title: 'Snapshots',  desc: 'puntos de restauración por pool' },
    scrub:     { title: 'Scrub',      desc: 'chequeo de integridad manual' },
    smart:     { title: 'SMART',      desc: 'diagnóstico de discos' },
  };
  $: viewMeta = VIEW_META[active] || VIEW_META.overview;
  $: totalDisksAssigned = pools.reduce((s, p) => s + (p.devices?.length || 0), 0);
  $: totalDisksFree = (disks.eligible?.length || 0);
  $: totalCapacity = pools.reduce((s, p) => s + (p.usage?.total_bytes || 0), 0);
  $: totalUsed = pools.reduce((s, p) => s + (p.usage?.used_bytes || 0), 0);
  $: totalFree = totalCapacity - totalUsed;
  // Salud agregada — v2 usa pool.mounted + pool.health.status
  // healthy + mounted → ok
  // degraded/at_risk/unstable → warn
  // critical o !mounted → crit
  $: overallHealth = pools.every(p => p.mounted && p.health?.status === 'healthy') && alerts.length === 0 ? 'ok'
                   : pools.some(p => !p.mounted || p.health?.status === 'critical') ? 'crit'
                   : 'warn';
  $: overallUsagePct = totalCapacity > 0 ? Math.round((totalUsed / totalCapacity) * 100) : 0;

  // ─── API ───
  // Todas las llamadas HTTP viven en ./storage/api.js (importado como `api`).
  // El componente solo orquesta: pide datos, gestiona estado, renderiza.

  async function loadAll() {
    try {
      // Fase 7 Bloque C3.2: añadido /observed para detectar filesystems
      // huérfanos (BTRFS no gestionados por NimOS) y mostrar divergencias.
      // Cada llamada aislada: si una falla, las demás siguen sirviendo
      // datos al usuario (degradación graceful).
      // AUDIT F5: getPools falla → null, NO []. Antes un error transitorio
      // (daemon reiniciando) vaciaba pools y la UI mostraba "Sin volúmenes
      // configurados" + Salud "OK · sin incidencias" (every() sobre []).
      // Ahora se conservan los últimos pools conocidos y se avisa.
      const [poolsData, statusData, disksData, alertsData, capsData, observedData] = await Promise.all([
        api.getPools().catch(() => null),
        api.getStatus().catch(() => ({})),
        api.getDisks().catch(() => ({})),
        api.getAlerts().catch(() => ({ alerts: [] })),
        api.getCapabilities().catch(() => ({})),
        api.getObserved().catch(() => ({ filesystems: [], divergences: [] })),
      ]);

      if (poolsData === null) {
        connError = true; // conservar pools previos, avisar en la UI
      } else {
        connError = false;
        pools = Array.isArray(poolsData) ? poolsData : [];
      }
      status = statusData || {};
      // /v2/disks devuelve {eligible, nvme, usb, provisioned} igual que legacy.
      disks = disksData || {};
      alerts = alertsData?.alerts || [];
      capabilities = capsData || {};

      // Observed state: filesystems detectados + divergencias pre-computadas.
      // Filtramos a los que NO están gestionados (orphans → importables).
      observedSnapshot = observedData || {};
      orphanFilesystems = (observedData?.filesystems || []).filter(fs => !fs.is_managed);
      divergences = observedData?.divergences || [];
    } catch (e) {
      console.error('[StorageApp] loadAll failed', e);
    }
    loading = false;
  }

  // ─── Refresh observed (escape hatch para el usuario) ────────────────────
  //
  // Fuerza un scan inmediato del observer y recarga datos. Útil cuando se
  // ha cambiado algo fuera de NimOS (cable conectado, disco USB, etc.).
  let refreshing = false;
  async function refreshObserved() {
    refreshing = true;
    try {
      // Llamada con ?refresh=true fuerza re-scan en el backend
      await api.getObserved({ refresh: true });
      // Recargar todos los datos para reflejar el nuevo estado
      await loadAll();
    } catch (e) {
      console.error('[StorageApp] refreshObserved failed', e);
    }
    refreshing = false;
  }

  // ─── Importar filesystem huérfano como pool managed ────────────────────

  let suggestedImportName = '';

  function openImportModal(fs) {
    importingFS = fs;
    // Sugerir un nombre razonable: usar el label si existe, lowercased
    suggestedImportName = (fs.label || '').toLowerCase().replace(/[^a-z0-9-]/g, '-').slice(0, 32);
    if (!suggestedImportName) suggestedImportName = 'imported-pool';
  }

  // ─── Bloque C3.4: Bridge wizard create → modal import ──────────────────
  //
  // Cuando el wizard de crear pool detecta DISK_HAS_FILESYSTEM y el usuario
  // elige "Importar pool existente", el wizard se cierra y emite este evento.
  // Nosotros buscamos el FS observado correspondiente y abrimos el modal.
  function handleWizardImportRequest(ev) {
    const uuid = ev.detail?.uuid;
    if (!uuid) return;
    creatingPool = false;
    // Buscar el FS en el observed state. Si no está como orphan (porque era
    // managed por otro pool), reconstruimos un objeto mínimo desde los detalles.
    let fs = orphanFilesystems.find(f => f.uuid === uuid);
    if (!fs) {
      const det = ev.detail.details || {};
      fs = {
        uuid: det.fs_uuid,
        label: det.fs_label,
        profile: det.fs_profile,
        size_bytes: det.size_bytes,
        used_bytes: det.used_bytes,
        observation_health: det.observation_health,
        is_mounted: false,
        devices: [{ path: det.disk }],
        devices_online: 1,
        devices_expected: 1,
      };
    }
    openImportModal(fs);
  }

  function closeImportModal() {
    importingFS = null;
  }

  // Tras importar con éxito: cerrar modal + refrescar observer + reload UI
  async function handleImportDone() {
    closeImportModal();
    await api.getObserved({ refresh: true });
    await loadAll();
  }

  // ─── Destruir filesystem huérfano (wipe disks) ─────────────────────────

  function openDestroyOrphanModal(fs) {
    destroyingOrphan = fs;
  }

  function closeDestroyOrphanModal() {
    destroyingOrphan = null;
  }

  // Tras destruir con éxito: cerrar modal + refrescar observer + reload UI
  async function handleDestroyOrphanDone() {
    closeDestroyOrphanModal();
    await api.getObserved({ refresh: true });
    await loadAll();
  }

  // ─── Helper: estado real de un disco (Bloque C3.3) ─────────────────────
  //
  // Cruza el path del disco con managed pools y observed orphans para
  // determinar el estado real. Esto previene el escenario donde el usuario
  // formatea un disco con un BTRFS huérfano valioso sin saberlo.
  //
  // Estados posibles:
  //   'managed'    → disco en uso por un pool gestionado por NimOS
  //   'orphan'     → disco con BTRFS no gestionado (puede importarse)
  //   'free'       → disco completamente limpio, listo para usar
  //
  // Devuelve un objeto con info estructurada para el render.
  function diskStatus(diskPath) {
    if (!diskPath) return { kind: 'free', label: 'disponible', variant: 'accent' };

    // ¿Pertenece a un pool managed?
    for (const pool of pools) {
      const poolDevices = pool.devices || [];
      for (const d of poolDevices) {
        const dPath = typeof d === 'string' ? d : (d.current_path || '');
        if (dPath === diskPath) {
          return {
            kind: 'managed',
            label: `pool ${pool.name}`,
            variant: 'success',
            poolName: pool.name,
            tooltip: `Disco en uso por el pool gestionado "${pool.name}"`,
          };
        }
      }
    }

    // ¿Tiene un BTRFS huérfano?
    for (const fs of orphanFilesystems) {
      for (const dev of (fs.devices || [])) {
        if (dev.path === diskPath) {
          return {
            kind: 'orphan',
            label: 'BTRFS huérfano',
            variant: 'warn',
            fsUuid: fs.uuid,
            fsLabel: fs.label,
            tooltip: `Tiene un filesystem BTRFS no gestionado ` +
                     `(label: ${fs.label || 'sin label'}, UUID: ${fs.uuid}). ` +
                     `Importable desde sección Observados.`,
          };
        }
      }
    }

    // Disco limpio
    return {
      kind: 'free',
      label: 'disponible',
      variant: 'accent',
      tooltip: 'Disco limpio, listo para crear un nuevo pool',
    };
  }

  async function loadSnapshots(poolName) {
    if (!poolName) return;
    try {
      const data = await api.getSnapshots(poolName);
      snapshots[poolName] = data?.snapshots || [];
      snapshots = snapshots;
    } catch (e) {
      console.warn('[StorageApp] loadSnapshots failed:', e.message);
    }
  }

  // ─── Scan ───
  async function rescanDisks() {
    scanning = true;
    try {
      // Llamada que swallows error: no usamos el payload, solo log si falla.
      await api.scanDisks().catch(e => {
        console.warn('[StorageApp] scan failed:', e.message);
      });
      await loadAll();
    } catch (e) {
      console.error('[StorageApp] scan unexpected:', e);
    }
    scanning = false;
  }

  // ─── Export pool (UI: "Desmontar") ───
  function openExportPoolWizard(poolName) {
    exportPoolName = poolName;    // abre el wizard
  }

  async function handleExportPoolDone() {
    exportPoolName = null;        // cerrar wizard
    // Forzar re-scan del observer (tras export el FS aparece como huérfano).
    await api.getObserved({ refresh: true });
    await loadAll();              // recargar lista de pools (el pool ya no debería estar)
  }

  // ─── Limpiar disco (wipe) ───
  function openWipeDialog(diskPath, diskSerial = '') {
    wipeDisk = diskPath;
    wipeSerial = diskSerial; // AUDIT F10: identidad que verá el backend
    wipeError = '';
  }

  // ─── Reemplazar disco (reparación de pool) ───
  let replacePool = null;       // pool cuyo disco se reemplaza
  let replaceOldDisk = null;    // disco a reemplazar (el que falta/falla)
  let replaceNewDeviceId = '';  // disco libre elegido
  let replaceProcessing = false;
  let replaceError = '';

  function openReplaceDialog(pool, disk) {
    replacePool = pool;
    replaceOldDisk = disk;
    replaceNewDeviceId = '';
    replaceError = '';
  }

  function closeReplaceDialog() {
    replacePool = null;
    replaceOldDisk = null;
    replaceNewDeviceId = '';
    replaceError = '';
    replaceNeedsForce = false;
  }

  // AUDIT F10: si el disco nuevo trae un filesystem, el backend responde
  // DISK_HAS_FILESYSTEM. Se muestra el aviso y el siguiente click confirma
  // la destrucción reintentando con force.
  let replaceNeedsForce = false;

  async function confirmReplace() {
    if (!replacePool || !replaceOldDisk || !replaceNewDeviceId || replaceProcessing) return;
    replaceProcessing = true;
    replaceError = '';
    try {
      const oldId = replaceOldDisk.id || replaceOldDisk.device_id || replaceOldDisk.serial;
      const op = await api.replaceDevice(replacePool.id || replacePool.name, oldId, replaceNewDeviceId,
        { force: replaceNeedsForce });
      replaceProcessing = false;
      closeReplaceDialog();
      // UI honesta: la API devuelve la operación IN_PROGRESS — el swap en BD
      // ocurre al terminar la copia (minutos/horas). No afirmar éxito aún.
      await loadAll();
      startRepairPolling(op?.id || op?.data?.id || null);
    } catch (err) {
      console.error('replace error:', err);
      if (err.code === 'DISK_HAS_FILESYSTEM') {
        replaceNeedsForce = true;
        const d = err.details || {};
        replaceError = `El disco nuevo contiene un filesystem ${d.fs_type || ''}`
          + (d.fs_label ? ` ("${d.fs_label}")` : '')
          + '. Pulsa "Reemplazar" de nuevo para DESTRUIRLO y continuar.';
      } else {
        replaceError = err.message || 'Error al reemplazar el disco';
      }
      replaceProcessing = false;
    }
  }

  // Polling acelerado (cada 3s) mientras dura una reparación.
  //
  // UI HONESTA (AUDIT-R8): antes el polling paraba cuando resilver_active
  // pasaba a false — que también ocurre cuando el replace FALLA (remount
  // imposible, error de btrfs, reinicio...). El fallo desaparecía en
  // silencio y el pool quedaba degradado sin que la UI dijera nada.
  // Ahora: si tenemos el id de la operación, se sigue LA OPERACIÓN hasta
  // su estado terminal. failed → banner persistente con el error real.
  let repairPollInterval = null;
  let repairOpId = null;
  let repairFailedMsg = '';   // banner de fallo de reparación (persistente)
  function startRepairPolling(opId = null) {
    if (opId) repairOpId = opId;
    if (repairPollInterval) return;
    repairPollInterval = setInterval(async () => {
      await loadAll();

      // Con id de op: la op manda (fuente de verdad del desenlace).
      if (repairOpId) {
        try {
          const ops = await api.getOperations({ limit: 20 });
          const op = (ops?.operations || ops || []).find(o => o.id === repairOpId);
          if (op && op.status === 'failed') {
            repairFailedMsg = op.error
              ? `El reemplazo de disco FALLÓ: ${op.error}`
              : 'El reemplazo de disco FALLÓ. Revisa las alertas y el estado del pool.';
            repairOpId = null;
            clearInterval(repairPollInterval);
            repairPollInterval = null;
            return;
          }
          if (op && (op.status === 'completed')) {
            repairFailedMsg = '';
            repairOpId = null;
            clearInterval(repairPollInterval);
            repairPollInterval = null;
            return;
          }
          // in_progress (o aún no visible) → seguir
          return;
        } catch (e) {
          // sin lectura de ops este ciclo: seguir intentando, no concluir nada
          return;
        }
      }

      // Sin id (reparaciones re-adoptadas tras reinicio): heurística previa.
      const anyRepairing = (pools || []).some(p => p.health?.resilver_active);
      if (!anyRepairing) {
        clearInterval(repairPollInterval);
        repairPollInterval = null;
      }
    }, 3000);
  }

  async function confirmWipe() {
    if (!wipeDisk || wipeProcessing) return;
    wipeProcessing = true;
    wipeError = '';
    try {
      await api.wipeDisk(wipeDisk, { serial: wipeSerial });
      // Éxito
      wipeProcessing = false;
      wipeDisk = null;
      await loadAll();
    } catch (err) {
      console.error('wipe error:', err);
      wipeError = err.message || 'Error al formatear';
      wipeProcessing = false;
    }
  }

  // ─── Crear pool (wizard 4 pasos · desde discos libres) ───
  function openCreatePoolWizard() {
    creatingPool = true;
  }

  async function handleCreatePoolDone() {
    creatingPool = false;
    // Forzar re-scan del observer para reflejar el nuevo pool managed.
    await api.getObserved({ refresh: true });
    await loadAll();
    active = 'overview'; // salta a resumen para ver el pool recién creado
  }

  // ─── Upgrade a RAID1 ───
  async function handleUpgradeRaidDone() {
    upgradingPool = null;
    await loadAll(); // refleja el nuevo profile + el disco que entró al pool
  }

  // ─── Scrub (AUDIT F8) ───
  // scrubStatus: { [poolName]: payload de /scrub/status }. Se carga al
  // entrar en la vista scrub y se refresca cada 4s mientras alguno corre,
  // para que el progreso avance y "Último scrub" sea real (antes: "—").
  let scrubStatus = {};
  let scrubPollInterval = null;

  async function loadScrubStatuses() {
    const entries = await Promise.all((pools || []).map(async (p) => {
      try {
        return [p.name, await api.getScrubStatus(p.name)];
      } catch {
        return [p.name, scrubStatus[p.name] || null]; // conservar último conocido
      }
    }));
    scrubStatus = Object.fromEntries(entries.filter((e) => e[1]));

    const anyRunning = Object.values(scrubStatus).some((s) => s?.status === 'scrubbing');
    if (anyRunning && !scrubPollInterval) {
      scrubPollInterval = setInterval(loadScrubStatuses, 4000);
    } else if (!anyRunning && scrubPollInterval) {
      clearInterval(scrubPollInterval);
      scrubPollInterval = null;
    }
  }

  // Cargar estados al entrar en la vista scrub (y si cambia la lista de pools).
  $: if (active === 'scrub' && pools.length) loadScrubStatuses();

  async function startScrub(poolName) {
    if (!confirm(`¿Iniciar scrub del pool "${poolName}"? El sistema puede ir más lento mientras corre.`)) return;
    scrubbing[poolName] = true;
    scrubbing = scrubbing;
    scrubMsg = '';
    try {
      try {
        await api.startScrub(poolName);
        scrubMsg = `Scrub iniciado en "${poolName}"`;
      } catch (e) {
        scrubMsg = e.message || 'Error al iniciar scrub';
      }
      await loadAll();
      await loadScrubStatuses(); // refleja "en curso" y arranca el polling
    } catch {
      scrubMsg = 'Error de conexión';
    }
    scrubbing[poolName] = false;
    scrubbing = scrubbing;
  }

  // ─── Lifecycle ───
  onMount(async () => {
    let attempts = 0;
    while (!$token && attempts < 10) { await new Promise(r => setTimeout(r, 200)); attempts++; }
    await loadAll();
    pollInterval = setInterval(loadAll, 20000); // 20s · storage es lento de cambiar
    // Si al entrar ya hay un pool reparándose (recarga a medias), acelerar.
    if ((pools || []).some(p => p.health?.resilver_active)) {
      startRepairPolling();
    }
  });

  onDestroy(() => {
    if (pollInterval) clearInterval(pollInterval);
    if (repairPollInterval) clearInterval(repairPollInterval);
    if (scrubPollInterval) clearInterval(scrubPollInterval);
  });
</script>

<AppShell
  appId="storage"
  title="Storage"
  headerIcon="S"
  pathSegments={['storage', active]}
  sections={[
    {
      label: 'Volúmenes',
      items: [
        {
          id: 'overview',
          label: 'Resumen',
          icon: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="12" height="3" rx="1"/><rect x="2" y="6.5" width="12" height="3" rx="1"/><rect x="2" y="10" width="12" height="3" rx="1"/></svg>`,
        },
        {
          id: 'disks',
          label: 'Discos',
          icon: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="8" cy="4" rx="5" ry="1.5"/><path d="M3 4v8c0 0.8 2.2 1.5 5 1.5s5-0.7 5-1.5V4"/></svg>`,
        },
        {
          id: 'snapshots',
          label: 'Snapshots',
          icon: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="6"/><path d="M8 4v4l2.5 1.5"/></svg>`,
        },
      ],
    },
    {
      label: 'Herramientas',
      items: [
        {
          id: 'scrub',
          label: 'Scrub',
          icon: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 8a5.5 5.5 0 0 1 9.5-3.8"/><path d="M13.5 8a5.5 5.5 0 0 1-9.5 3.8"/></svg>`,
        },
        {
          id: 'smart',
          label: 'SMART',
          icon: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M2 9l3-1 1.5 3 2-6 1.5 4 2-1h2"/></svg>`,
        },
      ],
    },
  ]}
  bind:active
  bodyPadding={false}
>

  <!-- Page header: cambia según vista activa (Resumen, Discos, etc.) -->
  <svelte:fragment slot="page-header">
    <b>{viewMeta.title}</b>
    <span class="ph-desc">{viewMeta.desc}</span>
  </svelte:fragment>

  {#if loading}
    <div class="storage-loading">
      <Spinner label="Cargando volúmenes y discos..." />
    </div>
  {:else}

  {#if repairFailedMsg}
    <div class="repair-failed" role="alert">
      ⛔ {repairFailedMsg}
      <button class="repair-failed-dismiss" on:click={() => repairFailedMsg = ''}
        title="Ocultar este aviso (la alerta persiste en el resumen)">✕</button>
    </div>
  {/if}

  {#if active === 'overview'}
    <div class="st-kpis-wrap">
      {#if connError}
        <div class="conn-error" role="alert">
          ⚠ Sin conexión con el daemon — mostrando los últimos datos conocidos
        </div>
      {/if}
      <StorageKPIs pools={pools} disks={disks} alerts={alerts} stale={connError} />
    </div>
  {/if}

  <div class="st-scroll">
    {#if active === 'overview'}
      <StorageOverview
        pools={pools}
        disks={disks}
        alerts={alerts}
        orphanFilesystems={orphanFilesystems}
        divergences={divergences}
        snapshots={snapshots}
        scanning={scanning}
        refreshing={refreshing}
        scrubbing={scrubbing}
        scrubMsg={scrubMsg}
        on:rescan={rescanDisks}
        on:create-pool={openCreatePoolWizard}
        on:refresh-observed={refreshObserved}
        on:scrub={(e) => startScrub(e.detail.poolName)}
        on:upgrade-raid={(e) => upgradingPool = e.detail.pool}
        on:export-pool={(e) => openExportPoolWizard(e.detail.poolName)}
        on:import-orphan={(e) => openImportModal(e.detail.fs)}
        on:destroy-orphan={(e) => openDestroyOrphanModal(e.detail.fs)}
        on:load-snapshots={(e) => loadSnapshots(e.detail.poolName)}
      />
    {/if}

        <!-- ══ DISCOS ══ -->
    {#if active === 'disks'}
      <StorageDisks
        pools={pools}
        disks={disks}
        orphanFilesystems={orphanFilesystems}
        scanning={scanning}
        on:rescan={rescanDisks}
        on:create-pool={openCreatePoolWizard}
        on:wipe={(e) => openWipeDialog(e.detail.path, e.detail.serial)}
        on:replace-device={(e) => openReplaceDialog(e.detail.pool, e.detail.disk)}
      />
    {/if}

        <!-- ══ SNAPSHOTS ══ -->
    {#if active === 'snapshots'}
      <StorageSnapshots
        pools={pools}
        snapshots={snapshots}
        on:load={(e) => loadSnapshots(e.detail.poolName)}
      />
    {/if}

    <!-- ══ SCRUB ══ -->
    {#if active === 'scrub'}
      <StorageScrub
        pools={pools}
        scrubbing={scrubbing}
        scrubMsg={scrubMsg}
        scrubStatus={scrubStatus}
        on:start={(e) => startScrub(e.detail.poolName)}
      />
    {/if}

    <!-- ══ SMART ══ -->
    {#if active === 'smart'}
      <StorageSmart pools={pools} disks={disks} />
    {/if}

  </div>
  {/if}

  <!-- Footer -->
  <svelte:fragment slot="footer">
    <span><span class="k">pools</span> <span class="v">{pools.length}</span></span>
    <span class="sep">·</span>
    <span><span class="k">disks</span> <span class="v">{totalDisksAssigned + totalDisksFree}</span></span>
    <span class="sep">·</span>
    <span><span class="k">btrfs</span> <span class="v" class:tc-accent={capabilities.btrfs}>{capabilities.btrfs ? 'available' : 'n/a'}</span></span>
  </svelte:fragment>

  <svelte:fragment slot="footer-right">
    <span><span class="k">usage</span> <span class="v" class:tc-accent={overallUsagePct < 75}>{overallUsagePct}%</span></span>
  </svelte:fragment>

</AppShell>

<!-- Export pool wizard · se abre desde kebab toolbar Resumen (UI: "Desmontar") -->
{#if exportPoolName}
  <ExportPoolWizard
    poolName={exportPoolName}
    on:done={handleExportPoolDone}
    on:cancel={() => exportPoolName = null}
  />
{/if}

<!-- ConfirmDialog · Limpiar disco (wipe) -->
<ConfirmDialog
  open={wipeDisk !== null}
  title="Limpiar disco"
  message={`Esta acción borrará todos los datos de ${wipeDisk || ''}. No se puede deshacer.`}
  confirmLabel="Limpiar disco"
  inputConfirm="LIMPIAR"
  variant="danger"
  processing={wipeProcessing}
  on:confirm={confirmWipe}
  on:cancel={() => { wipeDisk = null; wipeError = ''; }}
>
  {#if wipeError}
    <div class="dialog-err">{wipeError}</div>
  {/if}
</ConfirmDialog>

<!-- Reemplazar disco · reparación de pool degradado -->
{#if replacePool && replaceOldDisk}
  <div class="replace-overlay" on:click|self={closeReplaceDialog}>
    <div class="replace-dialog">
      <h3 class="replace-title">
        Reemplazar disco en {replacePool.name}
      </h3>

      <p class="replace-info">
        Vas a reemplazar el disco
        <span class="mono">{replaceOldDisk.current_path || replaceOldDisk.model || replaceOldDisk.serial || '—'}</span>
        {#if replaceOldDisk.smart_status === 'missing'}
          <span class="replace-missing">(que falta)</span>
        {/if}
        por un disco libre. Esto reconstruye la redundancia del pool con
        <span class="mono">btrfs replace</span>.
      </p>

      {#if (replacePool.profile === 'raid1' || replacePool.profile === 'raid1c3' || replacePool.profile === 'raid10') && (replacePool.devices?.filter(d => d.smart_status !== 'missing').length || 0) <= 1}
        <div class="replace-warn">
          ⚠ Este pool está degradado y sin redundancia: solo queda una copia de
          los datos. Si tienes una copia de seguridad, es buen momento para
          comprobarla antes de reparar. La reparación leerá del disco que queda.
        </div>
      {/if}

      <label class="replace-label">Disco nuevo (libre):</label>
      {#if (disks.eligible?.length || 0) === 0}
        <div class="replace-err">No hay discos libres disponibles.</div>
      {:else}
        <select class="replace-select" bind:value={replaceNewDeviceId}
        on:change={() => { replaceNeedsForce = false; replaceError = ''; }}>
          <option value="">— Selecciona un disco —</option>
          {#each disks.eligible as d}
            <option value={d.id || d.device_id || d.path}>
              {d.path} · {d.model || 'disco'} · {fmtBytes(d.size_bytes)}
            </option>
          {/each}
        </select>
      {/if}

      {#if replaceError}
        <div class="replace-err">{replaceError}</div>
      {/if}

      <div class="replace-actions">
        <button class="btn-secondary" on:click={closeReplaceDialog} disabled={replaceProcessing}>
          Cancelar
        </button>
        <button
          class="btn-primary"
          on:click={confirmReplace}
          disabled={!replaceNewDeviceId || replaceProcessing}
        >
          {replaceProcessing ? 'Reparando…' : 'Reemplazar y reparar'}
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Create pool wizard · 4 pasos: tipo → discos → nombre → confirmación -->
{#if creatingPool}
  <CreatePoolWizard
    capabilities={capabilities}
    eligibleDisks={disks.eligible || []}
    pools={pools}
    orphanFilesystems={orphanFilesystems}
    on:done={handleCreatePoolDone}
    on:cancel={() => creatingPool = false}
    on:request-import={handleWizardImportRequest}
  />
{/if}

<!-- Modal: Importar filesystem huérfano como pool managed -->
{#if importingFS}
  <ImportOrphanModal
    fs={importingFS}
    suggestedName={suggestedImportName}
    on:done={handleImportDone}
    on:cancel={closeImportModal}
  />
{/if}

<!-- Modal: Mejorar pool single a RAID1 (añadir disco + convertir) -->
{#if upgradingPool}
  <UpgradeRaidModal
    pool={upgradingPool}
    eligibleDisks={disks.eligible || []}
    on:done={handleUpgradeRaidDone}
    on:cancel={() => upgradingPool = null}
  />
{/if}

<!-- Modal: Destruir filesystem huérfano (wipe disks) -->
{#if destroyingOrphan}
  <DestroyOrphanModal
    fs={destroyingOrphan}
    on:done={handleDestroyOrphanDone}
    on:cancel={closeDestroyOrphanModal}
  />
{/if}

<style>
  /* AUDIT F5: aviso de datos no frescos cuando /pools no responde */
  .conn-error {
    flex-shrink: 0;
    padding: 8px 12px;
    margin-bottom: 8px;
    font-size: 12px;
    color: var(--warn);
    border: 1px solid color-mix(in srgb, var(--warn) 35%, var(--line));
    border-left: 3px solid var(--warn);
    border-radius: 4px;
    background: color-mix(in srgb, var(--warn) 8%, transparent);
  }

  /* AUDIT-R8 · Banner persistente de reparación fallida: un replace que
     falla NUNCA debe desaparecer en silencio. Descartable, pero la alerta
     de backend sigue viva en el resumen. */
  .repair-failed {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    margin-bottom: 8px;
    font-size: 12px;
    color: var(--danger, #f87171);
    border: 1px solid color-mix(in srgb, var(--danger, #f87171) 35%, var(--line));
    border-left: 3px solid var(--danger, #f87171);
    border-radius: 4px;
    background: color-mix(in srgb, var(--danger, #f87171) 10%, transparent);
  }
  .repair-failed-dismiss {
    margin-left: auto;
    background: none;
    border: none;
    color: inherit;
    cursor: pointer;
    font-size: 12px;
    padding: 0 4px;
  }

  /* Loading state */
  .storage-loading {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 280px;
  }

  /* Scroll container que envuelve las vistas (overview, disks, etc.) */
  .st-kpis-wrap {
    padding: 18px 24px 0;
    flex-shrink: 0;
  }

  .st-scroll {
    flex: 1;
    overflow-y: auto;
    padding: 20px 24px 24px;
    display: flex;
    flex-direction: column;
    gap: 20px;
  }

  /* Footer (k=label, v=value, sep=separador) */
  .k { color: var(--fg-faint); }
  .v { color: var(--fg-dim); font-feature-settings: "tnum"; }
  .sep { color: var(--fg-faint); }

  /* Error en wipe dialog */
  .replace-overlay {
    position: fixed;
    inset: 0;
    background: rgba(5, 8, 13, 0.72);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  .replace-dialog {
    background: var(--panel-elev, #171c26);
    border: 1px solid var(--line, #2a2f37);
    border-radius: 6px;
    padding: 22px;
    width: min(520px, 92vw);
    max-height: 90vh;
    overflow: auto;
  }
  .replace-title {
    margin: 0 0 12px;
    font-size: 16px;
    font-weight: 650;
    color: var(--text, #e8eaed);
  }
  .replace-info {
    font-size: 0.9rem;
    color: var(--text-dim, #9aa0a6);
    line-height: 1.5;
    margin-bottom: 16px;
  }
  .replace-missing { color: #e0625f; }
  .replace-warn {
    background: rgba(224, 179, 65, 0.12);
    border: 1px solid rgba(224, 179, 65, 0.4);
    border-radius: 4px;
    padding: 12px;
    font-size: 0.84rem;
    color: var(--warn, #e0b341);
    line-height: 1.5;
    margin-bottom: 16px;
  }
  .replace-label {
    display: block;
    font-size: 0.85rem;
    color: var(--text-dim, #9aa0a6);
    margin-bottom: 6px;
  }
  .replace-select {
    width: 100%;
    padding: 10px;
    background: var(--surface-2, #1c2026);
    border: 1px solid var(--line, #2a2f37);
    border-radius: 4px;
    color: var(--text, #e8eaed);
    font-size: 0.9rem;
    margin-bottom: 16px;
  }
  .replace-err {
    color: #e0625f;
    font-size: 0.85rem;
    margin-bottom: 12px;
  }
  .replace-actions {
    display: flex;
    gap: 10px;
    justify-content: flex-end;
  }

  .dialog-err {
    padding: 10px 12px;
    background: rgba(255, 90, 90, 0.08);
    border-left: 3px solid var(--crit);
    font-size: 12px;
    color: var(--crit);
    margin-top: 4px;
  }
</style>
