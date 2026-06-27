// storage_references.go — G3 · "¿Qué referencia este pool/share?"
//
// PROBLEMA:
// DestroyPool destruye un pool sin avisar de qué contiene, y borrar una share no
// dice qué apps la usan. El usuario no tiene forma de ver, ANTES de una operación
// destructiva, qué hay enganchado. (Queja histórica de OMV: "no veo qué usa este
// volumen".)
//
// SOLUCIÓN (v1, solo lectura):
// Resolver qué referencia un pool (sus shares + las apps que usan esas shares) y
// qué referencia una share (las apps que la usan). Puramente informativo: no
// cambia el comportamiento de ninguna op destructiva — eso es un guard aparte.

package main

// PoolReferences — qué hay enganchado a un pool.
type PoolReferences struct {
	Pool   string   `json:"pool"`
	Shares []string `json:"shares"`
	Apps   []string `json:"apps"`
}

// ShareReferences — qué usa una share.
type ShareReferences struct {
	Share string   `json:"share"`
	Apps  []string `json:"apps"`
}

// resolvePoolReferences — PURA. Cruza la lista de shares con el pool dado:
// devuelve las shares de ese pool y el conjunto (sin duplicar) de apps que las usan.
func resolvePoolReferences(poolName string, allShares []DBShare) PoolReferences {
	out := PoolReferences{Pool: poolName, Shares: []string{}, Apps: []string{}}
	seenApp := map[string]bool{}
	for _, s := range allShares {
		if s.Pool != poolName {
			continue
		}
		out.Shares = append(out.Shares, s.Name)
		for _, ap := range s.AppPermissions {
			if ap.AppId != "" && !seenApp[ap.AppId] {
				seenApp[ap.AppId] = true
				out.Apps = append(out.Apps, ap.AppId)
			}
		}
	}
	return out
}

// resolveShareReferences — PURA. Las apps (sin duplicar) que usan una share.
func resolveShareReferences(share DBShare) ShareReferences {
	out := ShareReferences{Share: share.Name, Apps: []string{}}
	seen := map[string]bool{}
	for _, ap := range share.AppPermissions {
		if ap.AppId != "" && !seen[ap.AppId] {
			seen[ap.AppId] = true
			out.Apps = append(out.Apps, ap.AppId)
		}
	}
	return out
}

// ── Wrappers de producción (leen de la BD) ──

func findPoolReferences(poolName string) (PoolReferences, error) {
	shares, err := dbSharesListRaw()
	if err != nil {
		return PoolReferences{}, err
	}
	return resolvePoolReferences(poolName, shares), nil
}

func findShareReferences(shareName string) (ShareReferences, error) {
	s, err := dbSharesGetRaw(shareName)
	if err != nil {
		return ShareReferences{}, err
	}
	if s == nil {
		return ShareReferences{Share: shareName, Apps: []string{}}, nil
	}
	return resolveShareReferences(*s), nil
}
