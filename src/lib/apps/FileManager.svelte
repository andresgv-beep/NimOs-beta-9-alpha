<script>
  /**
   * FileManager · v3.1 (Beta 8.1 refactor)
   * ───────────────────────────────────────────────────────────────
   * Migración del FileManager standalone a AppShell + TreeNode.
   *
   * CAMBIOS v3.1 respecto al anterior:
   *   · Eliminado chrome custom (.files-root, .sidebar, .sb-header,
   *     .sb-storage, .inner-wrap, .inner, .inner-titlebar, .statusbar).
   *   · Envuelto en <AppShell> · ahora tiene titlebar v3 con cubo +
   *     path dinámico nimos://host/files/{share}/{...pathParts} +
   *     LEDs C2 min/max/close como el resto de apps.
   *   · TreeNode v3.1 en slot `sidebar-content` con grupos
   *     Local / Remoto separados por sb-section labels.
   *   · View toggle + Nueva carpeta + Subir + clipboard badge
   *     viven en `toolbar` (fila propia debajo del page-header),
   *     no en `titlebar-actions` — así la titlebar queda limpia
   *     con solo cubo + path + LEDs, igual que Storage/NimHealth.
   *   · Breadcrumb del path en `page-header`. Back button al inicio.
   *   · Footer del AppShell muestra path mono + selected count.
   *   · ctx-menu pasa a position:fixed (no necesita root wrapper).
   *
   * LÓGICA SIN CAMBIOS:
   *   · fetchShares / fetchFiles / fetchStorage
   *   · Upload chunked con addTask (CHUNK_SIZE 20MB)
   *   · ctx-menu (open/copy/cut/paste/zip/unzip/rename/info/delete)
   *   · Modales: rename / info / newFolder
   *   · clipboard cut/copy/paste
   *   · download-token endpoint (CRIT-008)
   *   · Toda la API del daemon intacta
   *
   * ELIMINADO (no migrado):
   *   · sb-storage (caja con pool/uso en el sidebar) · esa info
   *     vive en Storage app, no es responsabilidad de Files.
   */
  import { onMount, onDestroy } from 'svelte';
  import AppShell from '$lib/components/AppShell.svelte';
  import TreeNode from '$lib/components/TreeNode.svelte';
  import { getToken, jsonHdrs as hdrs } from '$lib/stores/auth.js';
  import { openWindow } from '$lib/stores/windows.js';
  import { APP_META } from '$lib/apps.js';
  import { isMediaFile } from './mediaplayer/mediaUtils.js';
  import { notifySuccess, notifyError, notifyWarning } from '$lib/stores/notifications.js';
  import { addTask } from '$lib/stores/uploadTasks.js';
  import FilesGridView from './files/FilesGridView.svelte';
  import FilesListView from './files/FilesListView.svelte';
  import FilesContextMenu from './files/FilesContextMenu.svelte';
  import FilesModals from './files/FilesModals.svelte';
  import FilesRecycleBin from './files/FilesRecycleBin.svelte';
  import ShareTempModal from './files/ShareTempModal.svelte';

  // Deep-link opcional: abrir directamente en un share + ruta (p. ej. desde el
  // Panel de Juego → carpeta del server). Si no se pasan, comportamiento normal.
  export let initialShare = null;
  export let initialPath = '/';

  let shares = [];
  let currentShare = null;
  let currentPath = '/';
  let files = [];

  // Papelera: modo vista + share actual derivado (para saber si tiene papelera)
  let showRecycleBin = false;
  $: currentShareObj = shares.find((s) => s.name === currentShare) || null;
  $: currentShareHasRecycle = !!currentShareObj?.recycleBin;
  let loading = false;
  let selected = new Set();
  let selectionAnchor = null;

  // ── Clipboard ──
  let clipboard = null; // { entries: [{ file, path }], share, op: 'copy'|'cut' }

  // ── Context menu ──
  let ctxMenu = null;   // { x, y, file, idx } | null
  let ctxTarget = null; // archivo target del click derecho

  // ── Modals ──
  let renameModal = null;     // { file, newName }
  let infoModal = null;       // file
  let viewMode = 'grid';      // 'grid' | 'list'
  let newFolderModal = null;  // { name: '' }

  function filePath(file) {
    return currentPath === '/' ? `/${file.name}` : `${currentPath}/${file.name}`;
  }

  async function fetchShares() {
    try {
      const r = await fetch('/api/files', { headers: hdrs() });
      const d = await r.json();
      if (d.shares) shares = d.shares;
    } catch {}
  }
  async function fetchFiles() {
    if (!currentShare) { files = []; return; }
    loading = true;
    try {
      const r = await fetch(`/api/files?share=${currentShare}&path=${encodeURIComponent(currentPath)}`, { headers: hdrs() });
      const d = await r.json();
      files = d.files || [];
    } catch { files = []; }
    clearSelection();
    loading = false;
  }



  onMount(() => {
    fetchShares();

    // Si nos pasaron un destino inicial, abrimos ahí directamente (el listado
    // de shares de la barra lateral se carga aparte y no bloquea esto).
    if (initialShare) {
      currentShare = initialShare;
      currentPath = initialPath || '/';
      fetchFiles();
    }

    const handleMouseDown = (e) => {
      if (e.button === 2) return;
      if (!e.target.closest('.ctx-menu')) closeCtx();
    };
    document.addEventListener('mousedown', handleMouseDown);
    return () => { document.removeEventListener('mousedown', handleMouseDown); };
  });

  $: if (currentShare !== undefined || currentPath) fetchFiles();

  function navigate(share, path) { currentShare = share; currentPath = path; showRecycleBin = false; closeCtx(); fetchFiles(); }
  function goBack() {
    if (currentPath !== '/') currentPath = currentPath.split('/').slice(0, -1).join('/') || '/';
    else if (currentShare) { currentShare = null; currentPath = '/'; }
    closeCtx();
    fetchFiles();
  }
  // playInMediaPlayer · abre el reproductor nativo con el fichero (y su
  // carpeta como cola). El streaming va con la cookie de sesión, sin tokens.
  function playInMediaPlayer(file) {
    const meta = APP_META.mediaplayer;
    openWindow('mediaplayer', { width: meta.width, height: meta.height }, {
      mediaTarget: { share: currentShare, path: filePath(file) },
    });
    closeCtx();
  }

  async function openItem(file) {
    closeCtx();
    if (file.isDirectory) {
      currentPath = currentPath === '/' ? `/${file.name}` : `${currentPath}/${file.name}`;
      fetchFiles();
      return;
    }
    // Media → reproductor nativo de NimOS (doble clic = reproducir, como en
    // cualquier escritorio). "Descargar" sigue bajando el fichero.
    if (isMediaFile(file.name)) {
      playInMediaPlayer(file);
      return;
    }
    const fp = filePath(file);
    // CRIT-008: download token corto en lugar de session token en URL
    try {
      const res = await fetch('/api/files/download-token', { method: 'POST', headers: hdrs(), body: JSON.stringify({ share: currentShare, path: fp }) });
      const data = await res.json();
      if (data.token) {
        window.open(`/api/files/download?share=${currentShare}&path=${encodeURIComponent(fp)}&dl=${data.token}`, '_blank');
      } else {
        window.open(`/api/files/download?share=${currentShare}&path=${encodeURIComponent(fp)}&token=${getToken()}`, '_blank');
      }
    } catch {
      window.open(`/api/files/download?share=${currentShare}&path=${encodeURIComponent(fp)}&token=${getToken()}`, '_blank');
    }
  }
  function fileKey(file) {
    return file?.name || '';
  }

  function clearSelection() {
    selected = new Set();
    selectionAnchor = null;
  }

  function toggleSelect(file, e, orderedFiles = sorted) {
    const key = fileKey(file);
    if (!key) return;

    if (e?.shiftKey && selectionAnchor) {
      const anchorIndex = orderedFiles.findIndex((item) => fileKey(item) === selectionAnchor);
      const currentIndex = orderedFiles.findIndex((item) => fileKey(item) === key);
      if (anchorIndex !== -1 && currentIndex !== -1) {
        const range = orderedFiles
          .slice(Math.min(anchorIndex, currentIndex), Math.max(anchorIndex, currentIndex) + 1)
          .map(fileKey);
        selected = (e.ctrlKey || e.metaKey)
          ? new Set([...selected, ...range])
          : new Set(range);
        return;
      }
    }

    if (e?.ctrlKey || e?.metaKey) {
      const next = new Set(selected);
      next.has(key) ? next.delete(key) : next.add(key);
      selected = next;
    } else {
      selected = new Set([key]);
    }
    selectionAnchor = key;
  }

  function toggleSelectAll() {
    if (sorted.length > 0 && selected.size === sorted.length) clearSelection();
    else {
      selected = new Set(sorted.map(fileKey));
      selectionAnchor = sorted.length ? fileKey(sorted[0]) : null;
    }
  }

  const CHUNK_SIZE = 20 * 1024 * 1024; // 20MB por chunk

  function uploadFiles() {
    const input = document.createElement('input'); input.type = 'file'; input.multiple = true;
    input.onchange = async (e) => {
      const files = Array.from(e.target.files);
      for (const f of files) {
        const totalChunks = Math.ceil(f.size / CHUNK_SIZE) || 1;
        addTask(f.name, f.size, f, currentShare, currentPath, totalChunks, CHUNK_SIZE);
      }
      setTimeout(() => fetchFiles(), 2000);
    };
    input.click();
  }

  async function createFolder() {
    if (!newFolderModal?.name?.trim() || !currentShare) return;
    const name = newFolderModal.name.trim();
    try {
      const r = await fetch("/api/files/mkdir", { method: "POST", headers: hdrs(), body: JSON.stringify({ share: currentShare, path: currentPath, name }) });
      const d = await r.json();
      if (d.ok) {
        notifySuccess(`Carpeta "${name}" creada`, 'Files');
        fetchFiles();
      } else {
        notifyError(d.error || `No se pudo crear "${name}"`, 'Files');
      }
    } catch {
      notifyError(`No se pudo crear "${name}"`, 'Files');
    }
    newFolderModal = null;
  }

  // ── Context menu ──
  function onContextMenu(e, file, idx) {
    e.preventDefault();
    e.stopPropagation();
    ctxTarget = file;
    const key = fileKey(file);
    if (!selected.has(key)) {
      selected = new Set([key]);
      selectionAnchor = key;
    }
    const p = calcMenuPos(e);
    ctxMenu = { x: p.x, y: p.y, file, idx };
  }

  /**
   * v3.1 fix: el ctx-menu se posiciona con position:absolute relative
   * al .window ancestro (WindowFrame tiene will-change:transform, lo que
   * crea un contenedor positional para sus descendientes). Restamos el
   * rect del .window para convertir clientX/Y (coords del viewport) a
   * coords locales del WindowFrame. Sin `zoom` (Beta 9) las coords ya son
   * píxeles reales: no hay que dividir por nada.
   */
  function calcMenuPos(e, menuW = 200, menuH = 290) {
    const win = e.target.closest('.window');
    if (!win) return { x: 0, y: 0 };
    const rect = win.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    const maxX = rect.width  - menuW - 8;
    const maxY = rect.height - menuH - 8;
    return {
      x: Math.max(0, Math.min(x, maxX)),
      y: Math.max(0, Math.min(y, maxY)),
    };
  }

  function closeCtx() { ctxMenu = null; ctxTarget = null; }

  // Descarga (antes inline en el menú contextual)
  // triggerDownload · fuerza una descarga REAL vía un <a download> temporal.
  // El &download=1 hace que el backend sirva el fichero como adjunto aunque
  // sea audio/vídeo (si no, el navegador lo reproduce inline en una pestaña).
  // El anchor evita la pestaña fantasma del window.open y el bloqueo de popups.
  function triggerDownload(url, name) {
    const a = document.createElement('a');
    a.href = url;
    a.download = name || '';
    a.rel = 'noopener';
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  async function downloadFile(file) {
    const fp = filePath(file);
    const base = `/api/files/download?share=${currentShare}&path=${encodeURIComponent(fp)}&download=1`;
    try {
      const res = await fetch('/api/files/download-token', { method: 'POST', headers: hdrs(), body: JSON.stringify({ share: currentShare, path: fp }) });
      const data = await res.json();
      const url = data.token ? `${base}&dl=${data.token}` : `${base}&token=${getToken()}`;
      triggerDownload(url, file.name);
    } catch {
      triggerDownload(`${base}&token=${getToken()}`, file.name);
    }
    closeCtx();
  }

  // Despachador de las acciones emitidas por FilesContextMenu
  function handleCtxAction(e) {
    const { type, file } = e.detail;
    switch (type) {
      case 'open':     openItem(file); break;
      case 'play':     playInMediaPlayer(file); break;
      case 'copy':     copyFile(file); break;
      case 'cut':      cutFile(file); break;
      case 'paste':    pasteFile(); break;
      case 'download': downloadFile(file); break;
      case 'zip':      zipSelected(); break;
      case 'unzip':    unzipFile(file); break;
      case 'rename':   startRename(file); break;
      case 'info':     showInfo(file); break;
      case 'delete':   deleteFile(file); break;
      case 'share':    shareTempTarget = { file, share: currentShare, path: filePath(file) }; break;
    }
  }

  // ── Compartir temporal · target del modal ({ file, share, path } | null) ──
  let shareTempTarget = null;

  // ── Acciones ──
  async function deleteFile(file) {
    closeCtx();
    if (!confirm(`¿Eliminar "${file.name}"? Esta acción no se puede deshacer.`)) return;
    const fp = filePath(file);
    const res = await fetch('/api/files/delete', {
      method: 'POST', headers: hdrs(),
      body: JSON.stringify({ share: currentShare, path: fp })
    });
    const d = await res.json();
    if (d.ok) fetchFiles();
    else alert(d.error || 'Error al eliminar');
  }

  function selectedEntries(file) {
    const useSelection = selected.size > 1 && selected.has(fileKey(file));
    const items = useSelection
      ? sorted.filter((item) => selected.has(fileKey(item)))
      : [file];
    return items.map((item) => ({ file: item, path: filePath(item) }));
  }

  function copyFile(file) {
    clipboard = { entries: selectedEntries(file), share: currentShare, op: 'copy' };
    closeCtx();
  }

  function cutFile(file) {
    clipboard = { entries: selectedEntries(file), share: currentShare, op: 'cut' };
    closeCtx();
  }

  async function pasteFile() {
    if (!clipboard || !currentShare) return;
    const pending = clipboard;
    closeCtx();
    const completed = [];
    const partial = [];
    const failed = [];

    for (const entry of pending.entries) {
      const destPath = currentPath === '/'
        ? `/${entry.file.name}`
        : `${currentPath}/${entry.file.name}`;
      try {
        const res = await fetch('/api/files/paste', {
          method: 'POST', headers: hdrs(),
          body: JSON.stringify({
            srcShare: pending.share,
            srcPath: entry.path,
            destShare: currentShare,
            destPath,
            action: pending.op
          })
        });
        const data = await res.json();
        if (data.ok) completed.push(entry);
        else if (data.partial && data.copied) partial.push({ entry, error: data.error });
        else failed.push({ entry, error: data.error || 'Error al pegar' });
      } catch {
        failed.push({ entry, error: 'Error de conexión al pegar' });
      }
    }

    if (pending.op === 'cut') {
      // Solo conservamos en el portapapeles lo que no llegó a copiarse. Los
      // parciales ya tienen destino y repetirlos provocaría una colisión.
      clipboard = failed.length
        ? { ...pending, entries: failed.map(({ entry }) => entry) }
        : null;
    }

    if (completed.length || partial.length) fetchFiles();

    if (failed.length || partial.length) {
      const details = [
        failed.length ? `${failed.length} sin ${pending.op === 'cut' ? 'mover' : 'copiar'}` : '',
        partial.length ? `${partial.length} copiados, pero con el original conservado` : '',
      ].filter(Boolean).join(' · ');
      notifyWarning(details, 'Operación incompleta');
    } else {
      const count = completed.length;
      notifySuccess(
        count === 1
          ? `${completed[0].file.name} ${pending.op === 'cut' ? 'movido' : 'copiado'} correctamente`
          : `${count} elementos ${pending.op === 'cut' ? 'movidos' : 'copiados'} correctamente`,
        'Files'
      );
    }
  }

  function startRename(file) {
    closeCtx();
    renameModal = { file, newName: file.name };
    setTimeout(() => document.getElementById('rename-input')?.select(), 50);
  }

  async function confirmRename() {
    if (!renameModal || !renameModal.newName.trim() || renameModal.newName === renameModal.file.name) {
      renameModal = null; return;
    }
    const oldPath = filePath(renameModal.file);
    const newPath = currentPath === '/'
      ? `/${renameModal.newName.trim()}`
      : `${currentPath}/${renameModal.newName.trim()}`;
    const res = await fetch('/api/files/rename', {
      method: 'POST', headers: hdrs(),
      body: JSON.stringify({ share: currentShare, oldPath, newPath })
    });
    const d = await res.json();
    renameModal = null;
    if (d.ok) fetchFiles();
    else alert(d.error || 'Error al renombrar');
  }

  function showInfo(file) {
    closeCtx();
    infoModal = file;
  }

  // ── Zip / Unzip ──
  async function zipSelected() {
    closeCtx();
    const sel = sorted.filter((file) => selected.has(fileKey(file)));
    if (!sel.length || !currentShare) return;
    const paths = sel.map(f => currentPath === '/' ? `/${f.name}` : `${currentPath}/${f.name}`);
    const name = sel.length === 1 ? sel[0].name + '.zip' : 'archive.zip';
    try {
      const r = await fetch('/api/files/zip', {
        method: 'POST', headers: hdrs(),
        body: JSON.stringify({ share: currentShare, paths, name })
      });
      const d = await r.json();
      if (d.ok) {
        notifySuccess(`${d.name} creado`, 'Comprimido');
        fetchFiles();
      } else {
        notifyError(d.error || 'Error al comprimir', 'Zip');
      }
    } catch {
      notifyError('Error de conexión', 'Zip');
    }
  }

  async function unzipFile(file) {
    closeCtx();
    const fp = filePath(file);
    try {
      const r = await fetch('/api/files/unzip', {
        method: 'POST', headers: hdrs(),
        body: JSON.stringify({ share: currentShare, path: fp })
      });
      const d = await r.json();
      if (d.ok) {
        notifySuccess(`${d.count} archivos extraídos en "${d.folder}"`, 'Descomprimido');
        fetchFiles();
      } else {
        notifyError(d.error || 'Error al descomprimir', 'Unzip');
      }
    } catch {
      notifyError('Error de conexión', 'Unzip');
    }
  }

  $: sorted = [...files].sort((a,b) => (a.isDirectory?-1:1) - (b.isDirectory?-1:1) || a.name.localeCompare(b.name));
  $: shareInfo = shares.find(s => s.name === currentShare);
  $: pathParts = currentPath === '/' ? [] : currentPath.split('/').filter(Boolean);

  // Path dinámico para titlebar v3: nimos://host/files/{share}/{...pathParts}
  $: titlebarPath = currentShare
    ? ['files', currentShare, ...pathParts]
    : ['files'];

  $: localShares = shares.filter(s => !s.remote);
  $: remoteShares = shares.filter(s => s.remote);

  function handleKeydown(e) {
    const target = e.target;
    const editing = target?.matches?.('input, textarea, select, [contenteditable="true"]');
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'a' && currentShare && !editing) {
      e.preventDefault();
      selected = new Set(sorted.map(fileKey));
      selectionAnchor = sorted.length ? fileKey(sorted[0]) : null;
      return;
    }
    if (e.key === 'Escape') {
      closeCtx();
      renameModal = null;
      infoModal = null;
      newFolderModal = null;
      clearSelection();
    }
    if (e.key === 'Enter' && renameModal) confirmRename();
  }
</script>

<svelte:window on:keydown={handleKeydown} />

<AppShell
  appId="files"
  title="Files"
  headerIcon="F"
  pathSegments={titlebarPath}
  bodyPadding={false}
>
  <!-- ═══ SIDEBAR · grupos Local/Remoto + TreeNode ═══ -->
  <svelte:fragment slot="sidebar-content">
    {#if localShares.length > 0}
      <div class="fm-sb-group">
        <span>Local</span>
        <span class="count">{localShares.length}</span>
      </div>
      {#each localShares as share}
        <TreeNode
          share={share.name}
          path="/"
          name={share.displayName || share.name}
          depth={0}
          activePath={currentPath}
          activeShare={currentShare}
          onNavigate={navigate}
          remote={false}
        />
      {/each}
    {/if}

    {#if remoteShares.length > 0}
      <div class="fm-sb-group">
        <span>Remoto</span>
        <span class="count">{remoteShares.length}</span>
      </div>
      {#each remoteShares as share}
        <TreeNode
          share={share.name}
          path="/"
          name={share.displayName || share.name}
          depth={0}
          activePath={currentPath}
          activeShare={currentShare}
          onNavigate={navigate}
          remote={true}
        />
      {/each}
    {/if}
  </svelte:fragment>

  <!-- ═══ PAGE HEADER · back + título + breadcrumb + acciones ═══
       Acciones (clipboard + view toggle + nueva carpeta + subir)
       a la derecha vía .ph-right. Breadcrumb con flex:1 que se
       trunca con ellipsis si excede el ancho disponible.
  -->
  <svelte:fragment slot="page-header">
    <button class="fm-back" on:click={goBack} title="Atrás">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
        <polyline points="15 18 9 12 15 6"/>
      </svg>
    </button>
    {#if currentShare}
      <b>{shareInfo?.displayName || currentShare}</b>
      <span class="ph-desc">{sorted.length} {sorted.length === 1 ? 'elemento' : 'elementos'}</span>
      {#if pathParts.length > 0}
        <span class="fm-crumb">
          <span class="fm-crumb-sep">/</span>
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <span class="fm-crumb-part" on:click={() => navigate(currentShare, '/')}>{shareInfo?.displayName || currentShare}</span>
          {#each pathParts as part, i}
            <span class="fm-crumb-sep">/</span>
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <span
              class="fm-crumb-part"
              class:cur={i === pathParts.length - 1}
              on:click={() => { currentPath = '/' + pathParts.slice(0, i+1).join('/'); fetchFiles(); }}
            >{part}</span>
          {/each}
        </span>
      {/if}
    {:else}
      <b>Carpetas compartidas</b>
      <span class="ph-desc">{shares.length} {shares.length === 1 ? 'recurso' : 'recursos'}</span>
    {/if}
  </svelte:fragment>

  <!-- ═══ TOOLBAR · fila propia debajo del header (estilo subbar mockup) ═══
       Acciones (clipboard + view toggle + nueva carpeta + subir). Ya NO
       viven en el page-header: ahí colisionaban con los controles de
       ventana flotantes. Aquí tienen su propia franja. -->
  <svelte:fragment slot="toolbar">
    {#if currentShare || clipboard}
      <div class="fm-toolbar">
        {#if clipboard}
          <div class="clipboard-badge" class:cut={clipboard.op === 'cut'}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" style="width:10px;height:10px">
              <rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
            </svg>
            {clipboard.op === 'cut' ? 'Cortado' : 'Copiado'}:
            {clipboard.entries.length === 1 ? clipboard.entries[0].file.name : `${clipboard.entries.length} elementos`}
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <span class="cb-clear" on:click={() => clipboard = null}>✕</span>
          </div>
        {/if}
        {#if currentShare}
          <div class="tb-view-group">
            <button
              class="tb-plain-btn"
              class:active={sorted.length > 0 && selected.size === sorted.length}
              title={sorted.length > 0 && selected.size === sorted.length ? 'Quitar selección' : 'Seleccionar todo'}
              aria-label={sorted.length > 0 && selected.size === sorted.length ? 'Quitar selección' : 'Seleccionar todo'}
              on:click={toggleSelectAll}
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width:14px;height:14px">
                <rect x="3" y="3" width="18" height="18" rx="2"/><path d="m8 12 3 3 5-6"/>
              </svg>
            </button>
            <div class="tb-sep"></div>
            <button class="tb-plain-btn" class:active={viewMode === 'grid'} title="Vista cuadrícula" on:click={() => viewMode = 'grid'}>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" style="width:14px;height:14px">
                <rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/>
                <rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/>
              </svg>
            </button>
            <button class="tb-plain-btn" class:active={viewMode === 'list'} title="Vista lista" on:click={() => viewMode = 'list'}>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" style="width:14px;height:14px">
                <line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/>
                <line x1="8" y1="18" x2="21" y2="18"/>
                <line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/>
                <line x1="3" y1="18" x2="3.01" y2="18"/>
              </svg>
            </button>
            <div class="tb-sep"></div>
            <button class="tb-plain-btn" title="Nueva carpeta" on:click={() => newFolderModal = { name: '' }}>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width:14px;height:14px">
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
                <line x1="12" y1="11" x2="12" y2="17"/><line x1="9" y1="14" x2="15" y2="14"/>
              </svg>
            </button>
            {#if currentShareHasRecycle}
              <button class="tb-plain-btn" title="Papelera de reciclaje" on:click={() => showRecycleBin = true}>
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="width:14px;height:14px">
                  <polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/>
                </svg>
              </button>
            {/if}
          </div>
          <button class="btn-import" on:click={uploadFiles}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" style="width:11px;height:11px">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
              <polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/>
            </svg>
            Subir
          </button>
        {/if}
      </div>
    {/if}
  </svelte:fragment>

  <!-- ═══ CONTENT · papelera / grid / list ═══ -->
  {#if showRecycleBin && currentShare}
    <FilesRecycleBin
      share={currentShare}
      on:close={() => { showRecycleBin = false; fetchFiles(); }}
      on:changed={() => fetchFiles()}
    />
  {:else if viewMode === 'grid'}
    <FilesGridView
      {currentShare} {localShares} {remoteShares} {selected} {clipboard} {loading} {filePath}
      files={sorted}
      on:navigate={(e) => navigate(e.detail.share, e.detail.path)}
      on:select={(e) => toggleSelect(e.detail.file, e.detail.e, e.detail.files)}
      on:clear={clearSelection}
      on:open={(e) => openItem(e.detail)}
      on:context={(e) => onContextMenu(e.detail.e, e.detail.file, e.detail.i)}
      on:bgcontext={(e) => { const p = calcMenuPos(e.detail); ctxMenu = { x: p.x, y: p.y, file: null, idx: -1 }; }}
    />
  {:else}
    <FilesListView
      {currentShare} {localShares} {remoteShares} {selected} {clipboard} {loading} {filePath}
      files={sorted}
      on:navigate={(e) => navigate(e.detail.share, e.detail.path)}
      on:select={(e) => toggleSelect(e.detail.file, e.detail.e, e.detail.files)}
      on:clear={clearSelection}
      on:open={(e) => openItem(e.detail)}
      on:context={(e) => onContextMenu(e.detail.e, e.detail.file, e.detail.i)}
      on:bgcontext={(e) => { const p = calcMenuPos(e.detail); ctxMenu = { x: p.x, y: p.y, file: null, idx: -1 }; }}
    />
  {/if}

  <!-- ═══ FOOTER · path mono + selected ═══ -->
  <svelte:fragment slot="footer">
    {#if currentShare}
      <span class="fm-foot-path">
        {shareInfo?.displayName || currentShare}{currentPath !== '/' ? currentPath : ''}
      </span>
    {:else}
      <span>NimOS Storage</span>
    {/if}
  </svelte:fragment>
  <svelte:fragment slot="footer-right">
    {#if selected.size > 0}
      <span class="fm-sel">{selected.size} sel.</span>
    {/if}
  </svelte:fragment>
</AppShell>

<!-- ════════════════════════════════════════════════════════════
     CTX MENU · componente (position:fixed, fuera del AppShell)
     ════════════════════════════════════════════════════════════ -->
<FilesContextMenu menu={ctxMenu} {clipboard} on:action={handleCtxAction} />

<!-- ════════════════════════════════════════════════════════════
     COMPARTIR TEMPORAL · modal (enlace /s/token con caducidad)
     ════════════════════════════════════════════════════════════ -->
<ShareTempModal target={shareTempTarget} on:close={() => (shareTempTarget = null)} />

<!-- ════════════════════════════════════════════════════════════
     MODALES · componente (rename / info / new folder)
     ════════════════════════════════════════════════════════════ -->
<FilesModals
  bind:renameModal bind:infoModal bind:newFolderModal
  {currentShare} {filePath}
  on:rename={confirmRename}
  on:create={createFolder}
/>

<style>
  /* ═══════════════════════════════════════════════════════════
     SIDEBAR · grupos Local/Remoto (label + count)
     ───────────────────────────────────────────────────────────
     Reemplaza al .sb-section del AppShell render automático
     para soportar el contador a la derecha del label.
     ═══════════════════════════════════════════════════════════ */
  .fm-sb-group {
    padding: 14px 6px 6px;
    font-size: 11px;
    color: var(--ink-mute, #9a9aa3);
    letter-spacing: 0;
    font-weight: 650;
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .fm-sb-group .count {
    color: var(--ink-mute, #9a9aa3);
    letter-spacing: 0;
    font-weight: 500;
  }

  /* ═══════════════════════════════════════════════════════════
     PAGE HEADER · back + título + breadcrumb
     ═══════════════════════════════════════════════════════════ */
  .fm-back {
    width: 22px;
    height: 22px;
    border: none;
    background: transparent;
    color: var(--ink-mute, #9a9aa3);
    cursor: pointer;
    border-radius: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.12s, color 0.12s;
    padding: 0;
    flex-shrink: 0;
  }
  .fm-back svg { width: 14px; height: 14px; }
  .fm-back:hover {
    background: var(--side-hover, rgba(255,255,255,0.04));
    color: var(--ink, #f2f2f5);
  }

  .fm-crumb {
    flex: 1;
    min-width: 0;
    font-family: var(--font-mono, monospace);
    font-size: 11px;
    color: var(--ink-mute, #9a9aa3);
    margin-left: 4px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .fm-crumb-sep {
    display: inline-block;
    color: var(--ink-trace, #44444a);
    margin: 0 2px;
  }
  .fm-crumb-part {
    display: inline-block;
    padding: 1px 5px;
    border-radius: 3px;
    cursor: pointer;
    white-space: nowrap;
    transition: background 0.1s, color 0.1s;
    /* perforar la drag-zone de WindowFrame (z-index 5) para recibir clicks */
    position: relative;
    z-index: 6;
  }
  .fm-crumb-part:hover {
    color: var(--ink, #f2f2f5);
    background: rgba(255,255,255,0.04);
  }
  .fm-crumb-part.cur {
    color: var(--ink, #f2f2f5);
    background: rgba(255,255,255,0.05);
  }

  /* ═══════════════════════════════════════════════════════════
     TOOLBAR · franja propia debajo del header (estilo subbar mockup)
     ───────────────────────────────────────────────────────────
     Antes los botones vivían en .ph-right del page-header y
     colisionaban con los controles de ventana flotantes. Ahora
     tienen su propia fila, alineados a la derecha.
     ═══════════════════════════════════════════════════════════ */
  .fm-toolbar {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    padding: 8px 24px;
    border-bottom: 1px solid var(--line, rgba(255,255,255,0.04));
    flex-wrap: wrap;
  }

  .clipboard-badge {
    display: flex; align-items: center; gap: 5px;
    padding: 3px 8px 3px 6px;
    border-radius: 4px;
    font-size: 10px;
    color: var(--ink-dim, #c8c8cf);
    background: var(--bg-card, #15151a);
    border: 1px solid var(--line, rgba(255,255,255,0.08));
    max-width: 180px;
    overflow: hidden; white-space: nowrap; text-overflow: ellipsis;
    font-family: var(--font-sans);
    margin-right: auto; /* empuja el badge a la izquierda, botones a la derecha */
  }
  .clipboard-badge.cut {
    color: var(--warn, #fbbf24);
    border-color: rgba(251, 191, 36, 0.25);
    background: rgba(251, 191, 36, 0.06);
  }
  .cb-clear {
    cursor: pointer;
    color: var(--ink-mute, #9a9aa3);
    font-size: 10px;
    margin-left: 2px;
    flex-shrink: 0;
  }
  .cb-clear:hover { color: var(--ink, #f2f2f5); }

  .tb-view-group {
    display: flex;
    align-items: center;
    gap: 1px;
  }
  .tb-plain-btn {
    width: 26px; height: 26px;
    background: transparent;
    border: none;
    border-radius: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    color: var(--ink-mute, #9a9aa3);
    transition: background 0.12s, color 0.12s;
    flex-shrink: 0;
    padding: 0;
  }
  .tb-plain-btn:hover {
    background: rgba(255,255,255,0.06);
    color: var(--ink, #f2f2f5);
  }
  .tb-plain-btn.active {
    background: rgba(255,255,255,0.08);
    color: var(--ink, #f2f2f5);
  }
  .tb-plain-btn svg { pointer-events: none; }
  .tb-sep {
    width: 1px; height: 14px;
    background: var(--line, rgba(255,255,255,0.08));
    margin: 0 3px;
    flex-shrink: 0;
  }
  .btn-import {
    display: flex; align-items: center; gap: 5px;
    padding: 4px 10px;
    background: var(--signal, #5b8ff9);
    border: none;
    border-radius: 4px;
    color: var(--bg-window, #16161a);
    font-family: var(--font-sans);
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
    transition: filter 0.12s;
  }
  .btn-import:hover { filter: brightness(1.1); }


  /* ═══════════════════════════════════════════════════════════
     FOOTER · path mono + selected count
     ═══════════════════════════════════════════════════════════ */
  .fm-foot-path {
    font-family: var(--font-mono, monospace);
    color: var(--ink-dim, #c8c8cf);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .fm-sel {
    color: var(--signal, #5b8ff9);
    font-weight: 500;
  }

</style>
