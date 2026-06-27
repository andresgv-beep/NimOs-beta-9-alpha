// storage_references_http.go — Endpoint de referencias (G3).
//
//   GET /api/shares/{name}/references  → { share, apps: [...] }

package main

import "net/http"

func sharesReferencesHTTP(w http.ResponseWriter, r *http.Request, name string) {
	if requireAdmin(w, r) == nil {
		return
	}
	ref, err := findShareReferences(name)
	if err != nil {
		jsonError(w, 500, err.Error())
		return
	}
	jsonOk(w, map[string]interface{}{
		"ok":    true,
		"share": ref.Share,
		"apps":  ref.Apps,
	})
}
