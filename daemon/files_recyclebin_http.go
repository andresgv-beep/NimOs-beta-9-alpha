package main

import (
	"net/http"
	"os"
)

// ═══════════════════════════════════════════════════════════════════════
// PAPELERA · HTTP handlers · NimOS Beta 8.1
// ═══════════════════════════════════════════════════════════════════════
//
// Rutas (bajo /api/files/recyclebin):
//   GET  /api/files/recyclebin/list?share=X     listar
//   POST /api/files/recyclebin/restore  {share, ids:[...]}  restaurar
//   POST /api/files/recyclebin/delete   {share, ids:[...]}  eliminar definitivo
//   POST /api/files/recyclebin/empty    {share}             vaciar todo
//
// restore/delete aceptan uno o varios ids (selección múltiple en la UI).
// Requieren permiso rw sobre el share.
// ═══════════════════════════════════════════════════════════════════════

// openShareRootForRecycle resuelve el share, comprueba permiso rw y abre el
// os.Root. Devuelve nil (y responde error) si algo falla.
func openShareRootForRecycle(w http.ResponseWriter, session *DBSession, shareName string) (*os.Root, bool) {
	if shareName == "" {
		jsonError(w, 400, "Missing share")
		return nil, false
	}
	share, _ := resolveShare(shareName)
	if share == nil {
		jsonError(w, 404, "Shared folder not found")
		return nil, false
	}
	if !requireShareMounted(w, share) {
		return nil, false
	}
	if getSharePermission(session, share) != "rw" {
		jsonError(w, 403, "Write access denied")
		return nil, false
	}
	root, err := openRootAt(share.Path)
	if err != nil {
		jsonError(w, 500, "Cannot open share")
		return nil, false
	}
	return root, true
}

// bodyIDs extrae un array de strings "ids" del body JSON.
func bodyIDs(body map[string]interface{}) []string {
	raw, ok := body["ids"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// GET /api/files/recyclebin/list?share=X
func recycleBinListHTTP(w http.ResponseWriter, r *http.Request, session *DBSession) {
	shareName := r.URL.Query().Get("share")
	root, ok := openShareRootForRecycle(w, session, shareName)
	if !ok {
		return
	}
	defer root.Close()

	items := listRecycleBin(root)
	count, bytes := recycleBinStats(root)
	jsonOk(w, map[string]interface{}{
		"items":      items,
		"count":      count,
		"totalBytes": bytes,
	})
}

// POST /api/files/recyclebin/restore  {share, ids:[...]}
func recycleBinRestoreHTTP(w http.ResponseWriter, r *http.Request, session *DBSession) {
	body, _ := readBody(r)
	shareName := bodyStr(body, "share")
	ids := bodyIDs(body)
	root, ok := openShareRootForRecycle(w, session, shareName)
	if !ok {
		return
	}
	defer root.Close()

	if len(ids) == 0 {
		jsonError(w, 400, "No items selected")
		return
	}
	var restored int
	var errs []string
	for _, id := range ids {
		if err := restoreFromRecycleBin(root, id); err != nil {
			errs = append(errs, id+": "+err.Error())
		} else {
			restored++
		}
	}
	jsonOk(w, map[string]interface{}{"ok": true, "restored": restored, "errors": errs})
}

// POST /api/files/recyclebin/delete  {share, ids:[...]}
func recycleBinDeleteHTTP(w http.ResponseWriter, r *http.Request, session *DBSession) {
	body, _ := readBody(r)
	shareName := bodyStr(body, "share")
	ids := bodyIDs(body)
	root, ok := openShareRootForRecycle(w, session, shareName)
	if !ok {
		return
	}
	defer root.Close()

	if len(ids) == 0 {
		jsonError(w, 400, "No items selected")
		return
	}
	var deleted int
	var errs []string
	for _, id := range ids {
		if err := deleteFromRecycleBin(root, id); err != nil {
			errs = append(errs, id+": "+err.Error())
		} else {
			deleted++
		}
	}
	jsonOk(w, map[string]interface{}{"ok": true, "deleted": deleted, "errors": errs})
}

// POST /api/files/recyclebin/empty  {share}
func recycleBinEmptyHTTP(w http.ResponseWriter, r *http.Request, session *DBSession) {
	body, _ := readBody(r)
	shareName := bodyStr(body, "share")
	root, ok := openShareRootForRecycle(w, session, shareName)
	if !ok {
		return
	}
	defer root.Close()

	if err := emptyRecycleBin(root); err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"ok": true})
}
