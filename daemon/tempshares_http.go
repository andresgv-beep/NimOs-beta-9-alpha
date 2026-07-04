package main

// ═══════════════════════════════════════════════════════════════════
// TEMP SHARES · capa HTTP
// ───────────────────────────────────────────────────────────────────
// API autenticada (la usa Files y Panel de Control → Enlaces
// compartidos):
//   POST   /api/tempshares            crear enlace
//   GET    /api/tempshares            listar (admin: todos · user: suyos)
//   PATCH  /api/tempshares/{token}    reconfigurar (dueño o admin)
//   DELETE /api/tempshares/{token}    revocar (dueño o admin)
//   DELETE /api/tempshares/expired    limpiar expirados
//
// Público (SIN auth · el token ES la capability):
//   GET    /s/{token}                 página de descarga (o gate contraseña)
//   POST   /s/{token}                 descarga real (form; la contraseña va
//                                     en el body, nunca en la URL/historial)
//
// Seguridad: reutiliza la jaula de Files (resolveShare + relWithinShare +
// openRootAt + serveFileDownload) → imposible salir del share. La
// contraseña usa el scrypt de auth_crypto. Scope "lan" exige IP privada.
// ═══════════════════════════════════════════════════════════════════

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"
)

// ─── API autenticada ────────────────────────────────────────────────

func handleTempSharesRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	isAdmin := session.Role == "admin"
	sub := strings.TrimPrefix(r.URL.Path, "/api/tempshares")
	sub = strings.Trim(sub, "/")

	switch {
	case sub == "" && r.Method == "POST":
		tempShareCreate(w, r, session)
	case sub == "" && r.Method == "GET":
		dbTempShareCleanup()
		items, err := dbTempShareList(session.Username, isAdmin)
		if err != nil {
			jsonError(w, 500, "No se pudieron listar los enlaces")
			return
		}
		jsonOk(w, map[string]interface{}{
			"items":      items,
			"publicBase": tempSharePublicBase(),
			"now":        time.Now().UnixMilli(),
		})
	case sub == "expired" && r.Method == "DELETE":
		n, err := dbTempShareDeleteExpired(session.Username, isAdmin)
		if err != nil {
			jsonError(w, 500, "No se pudieron limpiar los expirados")
			return
		}
		jsonOk(w, map[string]interface{}{"removed": n})
	case sub != "" && r.Method == "PATCH":
		tempShareReconfigure(w, r, session, sub)
	case sub != "" && r.Method == "DELETE":
		ts, err := dbTempShareGet(sub)
		if err != nil {
			jsonError(w, 404, "Enlace no encontrado")
			return
		}
		if !isAdmin && ts.CreatedBy != session.Username {
			jsonError(w, 403, "No es tu enlace")
			return
		}
		dbTempShareDelete(sub)
		jsonOk(w, map[string]interface{}{"revoked": true})
	default:
		jsonError(w, 404, "Not found")
	}
}

type tempShareCreateReq struct {
	Share         string  `json:"share"`
	Path          string  `json:"path"`
	Scope         string  `json:"scope"`
	TTLHours      float64 `json:"ttlHours"`
	Password      string  `json:"password"`
	MaxConcurrent int     `json:"maxConcurrent"`
}

func tempShareCreate(w http.ResponseWriter, r *http.Request, session *DBSession) {
	var req tempShareCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, "Body inválido")
		return
	}
	if req.Scope != "lan" && req.Scope != "public" {
		jsonError(w, 400, "scope debe ser 'lan' o 'public'")
		return
	}
	expiresAt, err := tempShareValidateTTL(req.TTLHours)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if req.MaxConcurrent < 0 || req.MaxConcurrent > 64 {
		jsonError(w, 400, "maxConcurrent fuera de rango (0-64)")
		return
	}

	// El creador debe poder leer el share (mismo gate que Files)
	share, _ := resolveShare(req.Share)
	if share == nil {
		jsonError(w, 404, "Share no encontrado")
		return
	}
	if getSharePermission(session, share) == "none" {
		jsonError(w, 403, "Sin acceso a este share")
		return
	}

	// Validar que el archivo existe DENTRO de la jaula y es regular
	rel, perr := relWithinShare(req.Path)
	if perr != nil {
		jsonError(w, 400, perr.Error())
		return
	}
	root, oerr := openRootAt(share.Path)
	if oerr != nil {
		jsonError(w, 500, "No se pudo abrir el share")
		return
	}
	defer root.Close()
	stat, serr := root.Stat(rel)
	if serr != nil {
		jsonError(w, 404, "Archivo no encontrado")
		return
	}
	if stat.IsDir() {
		jsonError(w, 400, "Solo se pueden compartir archivos (no carpetas)")
		return
	}

	ts := &TempShare{
		Share:         req.Share,
		Path:          rel,
		FileName:      relBase(rel),
		SizeBytes:     stat.Size(),
		Scope:         req.Scope,
		CreatedBy:     session.Username,
		ExpiresAt:     expiresAt,
		MaxConcurrent: req.MaxConcurrent,
	}
	if req.Password != "" {
		h, herr := hashPassword(req.Password)
		if herr != nil {
			jsonError(w, 500, "No se pudo proteger el enlace")
			return
		}
		ts.passwordHash = h
		ts.HasPassword = true
	}
	if err := dbTempShareCreate(ts); err != nil {
		jsonError(w, 500, "No se pudo crear el enlace")
		return
	}
	dbTempShareCleanup()
	logMsg("tempshare: %s creó /s/%s → %s/%s (scope=%s, ttl=%.0fh)",
		session.Username, ts.Token, ts.Share, ts.Path, ts.Scope, req.TTLHours)
	jsonOk(w, map[string]interface{}{
		"item":       ts,
		"publicBase": tempSharePublicBase(),
	})
}

type tempSharePatchReq struct {
	Scope         *string  `json:"scope"`
	TTLHours      *float64 `json:"ttlHours"` // re-extiende DESDE AHORA
	Password      *string  `json:"password"`
	ClearPassword bool     `json:"clearPassword"`
	MaxConcurrent *int     `json:"maxConcurrent"`
}

func tempShareReconfigure(w http.ResponseWriter, r *http.Request, session *DBSession, token string) {
	ts, err := dbTempShareGet(token)
	if err != nil {
		jsonError(w, 404, "Enlace no encontrado")
		return
	}
	if session.Role != "admin" && ts.CreatedBy != session.Username {
		jsonError(w, 403, "No es tu enlace")
		return
	}
	var req tempSharePatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, 400, "Body inválido")
		return
	}
	if req.Scope != nil {
		if *req.Scope != "lan" && *req.Scope != "public" {
			jsonError(w, 400, "scope debe ser 'lan' o 'public'")
			return
		}
		ts.Scope = *req.Scope
	}
	if req.TTLHours != nil {
		expiresAt, terr := tempShareValidateTTL(*req.TTLHours)
		if terr != nil {
			jsonError(w, 400, terr.Error())
			return
		}
		ts.ExpiresAt = expiresAt
	}
	if req.ClearPassword {
		ts.passwordHash = ""
		ts.HasPassword = false
	} else if req.Password != nil && *req.Password != "" {
		h, herr := hashPassword(*req.Password)
		if herr != nil {
			jsonError(w, 500, "No se pudo proteger el enlace")
			return
		}
		ts.passwordHash = h
		ts.HasPassword = true
	}
	if req.MaxConcurrent != nil {
		if *req.MaxConcurrent < 0 || *req.MaxConcurrent > 64 {
			jsonError(w, 400, "maxConcurrent fuera de rango (0-64)")
			return
		}
		ts.MaxConcurrent = *req.MaxConcurrent
	}
	if err := dbTempShareUpdate(ts); err != nil {
		jsonError(w, 500, "No se pudo actualizar el enlace")
		return
	}
	jsonOk(w, map[string]interface{}{"item": ts})
}

// tempSharePublicBase compone https://dominio:puerto desde la config de
// exposición (si está activa). Vacío si no hay dominio configurado.
func tempSharePublicBase() string {
	if networkRepo == nil {
		return ""
	}
	cfg, err := networkRepo.GetExposureConfig(context.Background())
	if err != nil || !cfg.Enabled || cfg.BaseDomain == "" {
		return ""
	}
	if cfg.HTTPSPort == 443 {
		return "https://" + cfg.BaseDomain
	}
	return fmt.Sprintf("https://%s:%d", cfg.BaseDomain, cfg.HTTPSPort)
}

// ─── Handler público /s/{token} ─────────────────────────────────────

func handleTempSharePublic(w http.ResponseWriter, r *http.Request) {
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/s/"), "/")
	// Token: solo base62 y longitud exacta → lo demás ni toca la DB
	if len(token) != tempShareTokenLen || !isBase62(token) {
		tempShareRenderError(w, 404, "Enlace no válido",
			"Este enlace no existe o está mal escrito.")
		return
	}

	ts, err := dbTempShareGet(token)
	if err != nil {
		tempShareRenderError(w, 404, "Enlace no encontrado",
			"Este enlace no existe o fue revocado.")
		return
	}
	now := time.Now().UnixMilli()
	if now > ts.ExpiresAt {
		tempShareRenderError(w, 410, "Enlace caducado",
			"Este enlace expiró y ya no está disponible.")
		return
	}
	// Scope LAN: exigir IP privada/loopback
	if ts.Scope == "lan" && !isPrivateClient(r) {
		tempShareRenderError(w, 403, "Solo red local",
			"Este enlace solo funciona dentro de la red local.")
		return
	}

	switch r.Method {
	case "GET":
		tempShareRenderPage(w, ts, "")
	case "POST":
		tempShareDownload(w, r, ts)
	default:
		jsonError(w, 405, "Method not allowed")
	}
}

func tempShareDownload(w http.ResponseWriter, r *http.Request, ts *TempShare) {
	// Gate de contraseña (viene en el body del form, nunca en la URL)
	if ts.HasPassword {
		if !verifyPassword(r.FormValue("password"), ts.passwordHash) {
			// Timing ya equalizado por scrypt; re-render con error
			tempShareRenderPage(w, ts, "Contraseña incorrecta")
			return
		}
	}
	// Slot de descarga concurrente
	if !tempShareAcquireSlot(ts.Token, ts.MaxConcurrent) {
		tempShareRenderError(w, 429, "Demasiadas descargas",
			"Se alcanzó el límite de descargas simultáneas. Prueba en un momento.")
		return
	}
	defer tempShareReleaseSlot(ts.Token, ts.MaxConcurrent)

	share, _ := resolveShare(ts.Share)
	if share == nil {
		tempShareRenderError(w, 410, "Archivo no disponible",
			"La carpeta de origen ya no existe.")
		return
	}
	root, oerr := openRootAt(share.Path)
	if oerr != nil {
		tempShareRenderError(w, 500, "Error interno",
			"No se pudo acceder al archivo.")
		return
	}
	defer root.Close()
	if _, serr := root.Stat(ts.Path); serr != nil {
		tempShareRenderError(w, 410, "Archivo no disponible",
			"El archivo fue movido o eliminado.")
		return
	}
	dbTempShareCountDownload(ts.Token)
	logMsg("tempshare: descarga /s/%s (%s) desde %s", ts.Token, ts.FileName, clientIP(r))
	serveFileDownload(w, r, root, ts.Path)
}

func isBase62(s string) bool {
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func isPrivateClient(r *http.Request) bool {
	ip := net.ParseIP(clientIP(r))
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback()
}

// ─── Página pública (server-rendered, autocontenida) ────────────────

var tempSharePageTpl = template.Must(template.New("ts").Parse(`<!doctype html>
<html lang="es"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{.Title}} · NimOS</title>
<style>
  :root { color-scheme: dark; }
  * { margin:0; padding:0; box-sizing:border-box; }
  body { min-height:100vh; display:flex; align-items:center; justify-content:center;
         background:#0b0b0f; color:#e8e8ea;
         font-family:ui-sans-serif,system-ui,-apple-system,sans-serif; padding:20px; }
  .card { width:100%; max-width:360px; background:#141419;
          border:1px solid rgba(255,255,255,0.08); border-radius:10px;
          padding:28px 26px; text-align:center; }
  .brand { display:flex; align-items:center; justify-content:center; gap:7px; margin-bottom:24px; }
  .brand .cube { width:18px; height:18px; border-radius:4px; background:#00ff9f;
                 color:#0b0b0f; display:inline-flex; align-items:center; justify-content:center;
                 font-weight:700; font-size:11px; }
  .brand span { font-size:12px; color:#9a9aa3; font-family:ui-monospace,monospace; letter-spacing:.5px; }
  .fico { width:72px; height:72px; border-radius:16px; background:rgba(122,158,177,.12);
          display:flex; align-items:center; justify-content:center; margin:0 auto 16px; font-size:34px; }
  .fname { font-size:16px; font-weight:500; margin-bottom:5px; word-break:break-all; }
  .fmeta { font-size:12px; color:#9a9aa3; font-family:ui-monospace,monospace; margin-bottom:18px; }
  .pill { display:inline-flex; align-items:center; gap:6px; padding:5px 12px; border-radius:20px;
          background:rgba(255,255,255,.05); font-size:11px; color:#c8c8cf; margin-bottom:22px; }
  .pill .clock { color:#f0b429; }
  input[type=password] { width:100%; padding:10px 12px; background:#0b0b0f;
          border:1px solid rgba(255,255,255,.12); border-radius:6px; color:#e8e8ea;
          font-size:13px; margin-bottom:12px; outline:none; }
  input[type=password]:focus { border-color:rgba(0,255,159,.4); }
  button { width:100%; padding:13px; border:none; border-radius:8px; font-size:14px;
           font-weight:500; color:#0b0b0f; background:#00ff9f; cursor:pointer; }
  button:hover { filter:brightness(1.08); }
  .foot { font-size:11px; color:#6a6a72; font-family:ui-monospace,monospace; margin-top:14px; }
  .err { font-size:12px; color:#f87171; margin-bottom:12px; }
  .emsg { font-size:13px; color:#9a9aa3; line-height:1.5; }
  .etitle { font-size:16px; font-weight:500; margin-bottom:8px; }
</style></head><body>
<div class="card">
  <div class="brand"><span class="cube">N</span><span>compartido vía NimOS</span></div>
{{if .IsError}}
  <div class="fico">⚠</div>
  <div class="etitle">{{.Title}}</div>
  <div class="emsg">{{.Message}}</div>
{{else}}
  <div class="fico">📄</div>
  <div class="fname">{{.FileName}}</div>
  <div class="fmeta">{{.SizeHuman}}</div>
  <div class="pill"><span class="clock">◷</span> Caduca en <span id="cd">{{.Remaining}}</span></div>
  <form method="post">
    {{if .NeedsPassword}}
      {{if .PwError}}<div class="err">{{.PwError}}</div>{{end}}
      <input type="password" name="password" placeholder="Contraseña" autofocus required>
      <button type="submit">Desbloquear y descargar</button>
    {{else}}
      <button type="submit">⬇ Descargar</button>
    {{end}}
  </form>
  <div class="foot">{{.Downloads}} descargas{{if .MaxNote}} · {{.MaxNote}}{{end}}</div>
  <script>
    var end = {{.ExpiresAt}};
    function tick(){
      var ms = end - Date.now();
      if (ms <= 0) { location.reload(); return; }
      var h = Math.floor(ms/3600000), m = Math.floor(ms%3600000/60000);
      var el = document.getElementById('cd');
      if (el) el.textContent = (h>48? Math.floor(h/24)+'d '+(h%24)+'h' : h+'h '+m+'m');
    }
    tick(); setInterval(tick, 30000);
  </script>
{{end}}
</div></body></html>`))

type tempSharePageData struct {
	Title, Message, FileName, SizeHuman, Remaining, PwError, MaxNote string
	Downloads                                                        int64
	ExpiresAt                                                        int64
	IsError, NeedsPassword                                           bool
}

func tempShareRenderPage(w http.ResponseWriter, ts *TempShare, pwError string) {
	remaining := time.Until(time.UnixMilli(ts.ExpiresAt))
	h := int(remaining.Hours())
	m := int(remaining.Minutes()) % 60
	rem := fmt.Sprintf("%dh %dm", h, m)
	if h > 48 {
		rem = fmt.Sprintf("%dd %dh", h/24, h%24)
	}
	maxNote := ""
	if ts.MaxConcurrent > 0 {
		maxNote = fmt.Sprintf("máx %d simultáneas", ts.MaxConcurrent)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	tempSharePageTpl.Execute(w, tempSharePageData{
		Title:         ts.FileName,
		FileName:      ts.FileName,
		SizeHuman:     tempShareHumanSize(ts.SizeBytes),
		Remaining:     rem,
		ExpiresAt:     ts.ExpiresAt,
		Downloads:     ts.Downloads,
		MaxNote:       maxNote,
		NeedsPassword: ts.HasPassword,
		PwError:       pwError,
	})
}

func tempShareRenderError(w http.ResponseWriter, status int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	tempSharePageTpl.Execute(w, tempSharePageData{
		Title: title, Message: msg, IsError: true,
	})
}

func tempShareHumanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
