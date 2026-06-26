// storage_share_adopt_http.go — Endpoints de re-adopción de shares huérfanas.
//
//   GET  /api/shares/orphans           — lista subvolúmenes huérfanos (en disco, no en BD)
//   POST /api/shares/orphans/readopt   — re-adopta uno {pool,name} o todos {all:true}

package main

import "net/http"

// GET /api/shares/orphans
func sharesOrphansListHTTP(w http.ResponseWriter, r *http.Request) {
	if requireAdmin(w, r) == nil {
		return
	}
	orphans, err := findOrphanShares(r.Context())
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	if orphans == nil {
		orphans = []OrphanShare{}
	}
	jsonOk(w, map[string]interface{}{
		"ok":      true,
		"orphans": orphans,
		"count":   len(orphans),
	})
}

// POST /api/shares/orphans/readopt
// Body: {"pool":"data8","name":"multimedia"}  ·  o  ·  {"all":true}
func sharesOrphansReadoptHTTP(w http.ResponseWriter, r *http.Request) {
	session := requireAdmin(w, r)
	if session == nil {
		return
	}
	body, err := readBody(r)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}

	// Modo "todos": re-adopta cada huérfano detectado.
	if all, _ := body["all"].(bool); all {
		orphans, err := findOrphanShares(r.Context())
		if err != nil {
			jsonError(w, 500, err.Error())
			return
		}
		readopted := []string{}
		failures := map[string]string{}
		for _, o := range orphans {
			if err := readoptOrphanShare(r.Context(), o.Pool, o.Name, session.Username); err != nil {
				failures[o.Name] = err.Error()
				continue
			}
			readopted = append(readopted, o.Name)
		}
		jsonOk(w, map[string]interface{}{
			"ok":        len(failures) == 0,
			"readopted": readopted,
			"failures":  failures,
		})
		return
	}

	// Modo individual: {pool, name}.
	pool := bodyStr(body, "pool")
	name := bodyStr(body, "name")
	if pool == "" || name == "" {
		jsonError(w, 400, "se requiere 'pool' y 'name' (o 'all':true)")
		return
	}
	if err := readoptOrphanShare(r.Context(), pool, name, session.Username); err != nil {
		jsonError(w, mapShareErrorToStatus(err), err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{"ok": true, "name": name, "pool": pool})
}
