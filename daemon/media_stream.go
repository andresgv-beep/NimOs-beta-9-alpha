// media_stream.go — MediaPlayer · probe de códecs y remux al vuelo (ffmpeg).
//
// PROBLEMA: los navegadores no descodifican audio Dolby/DTS (AC3/EAC3/DTS,
// licencias) → una peli HEVC+EAC3 se ve PERO NO SE OYE. Y los subtítulos que
// viven DENTRO del mkv (subrip) eran invisibles para el player (solo veíamos
// .srt hermanos).
//
// SOLUCIÓN (la estándar de los NAS ligeros, sin transcoding pesado):
//   · GET /api/media/probe  → ffprobe: qué pistas tiene el fichero (códecs,
//     canales, idiomas, duración). El player decide con datos.
//   · GET /api/media/stream → ffmpeg remux: VÍDEO COPIADO TAL CUAL (cero
//     coste real de CPU) + audio transcodificado a AAC estéreo (barato hasta
//     en la Pi) en MP4 fragmentado por chunked streaming. Seek: parámetro
//     t=<segundos> (ffmpeg -ss de entrada, salto por keyframe).
//   · GET /api/media/subs   → pista de subtítulos interna → WebVTT.
//
// SEGURIDAD: misma disciplina que /api/files/download — sesión requerida,
// permiso del share por usuario, ruta confinada al share (relWithinShare) y
// ffmpeg SIEMPRE por exec con array de args (jamás shell). El proceso muere
// con la conexión (CommandContext sobre r.Context()).
//
// Si ffmpeg no está instalado, los endpoints devuelven 501 y el player sigue
// con reproducción directa (el probe avisa con "ffmpeg": false).

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ── Router ───────────────────────────────────────────────────────────────────

func handleMediaRoutes(w http.ResponseWriter, r *http.Request) {
	session := requireAuth(w, r)
	if session == nil {
		return
	}
	switch {
	case r.URL.Path == "/api/media/probe" && r.Method == "GET":
		mediaProbeHTTP(w, r, session)
	case r.URL.Path == "/api/media/stream" && r.Method == "GET":
		mediaStreamHTTP(w, r, session)
	case r.URL.Path == "/api/media/subs" && r.Method == "GET":
		mediaSubsHTTP(w, r, session)
	default:
		jsonError(w, 404, "Not found")
	}
}

// ── Resolución segura del fichero ────────────────────────────────────────────

// resolveMediaPath valida share+path (permiso de sesión + confinamiento) y
// devuelve la ruta absoluta del fichero para pasársela a ffmpeg.
func resolveMediaPath(session *DBSession, shareName, subPath string) (string, int, string) {
	if shareName == "" || subPath == "" {
		return "", 400, "share y path requeridos"
	}
	share, _ := resolveShare(shareName)
	if share == nil {
		return "", 404, "Share not found"
	}
	if getSharePermission(session, share) == "none" {
		return "", 403, "Access denied"
	}
	rel, err := relWithinShare(subPath)
	if err != nil {
		return "", 400, err.Error()
	}
	abs := filepath.Join(share.Path, rel)
	// Cinturón además de relWithinShare: el Join debe seguir dentro del share.
	base := filepath.Clean(share.Path)
	if abs != base && !strings.HasPrefix(abs, base+string(filepath.Separator)) {
		return "", 400, "Invalid path"
	}
	return abs, 0, ""
}

func ffmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// ── Probe ────────────────────────────────────────────────────────────────────

// MediaStreamInfo es una pista del fichero, reducida a lo que el player usa.
type MediaStreamInfo struct {
	Index    int    `json:"index"` // índice RELATIVO dentro de su tipo (0:a:N)
	Type     string `json:"type"`  // video | audio | subtitle
	Codec    string `json:"codec"` // hevc, eac3, subrip…
	Channels int    `json:"channels,omitempty"`
	Width    int    `json:"width,omitempty"`  // solo vídeo
	Height   int    `json:"height,omitempty"` // solo vídeo (decide 4K → transcode)
	Lang     string `json:"lang,omitempty"`
	Title    string `json:"title,omitempty"`
}

// ffprobeOut refleja lo mínimo del JSON de ffprobe.
type ffprobeOut struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Channels  int    `json:"channels"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Tags      struct {
			Language string `json:"language"`
			Title    string `json:"title"`
		} `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// parseProbeJSON convierte la salida de ffprobe al contrato del player.
// PURA (testeable sin ffmpeg): índices relativos por tipo, como los usa
// el mapeo 0:a:N / 0:s:N de ffmpeg.
func parseProbeJSON(raw []byte) (streams []MediaStreamInfo, duration float64, err error) {
	var out ffprobeOut
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, 0, err
	}
	counts := map[string]int{}
	for _, s := range out.Streams {
		t := s.CodecType
		if t != "video" && t != "audio" && t != "subtitle" {
			continue
		}
		streams = append(streams, MediaStreamInfo{
			Index:    counts[t],
			Type:     t,
			Codec:    s.CodecName,
			Channels: s.Channels,
			Width:    s.Width,
			Height:   s.Height,
			Lang:     s.Tags.Language,
			Title:    s.Tags.Title,
		})
		counts[t]++
	}
	duration, _ = strconv.ParseFloat(out.Format.Duration, 64)
	return streams, duration, nil
}

func mediaProbeHTTP(w http.ResponseWriter, r *http.Request, session *DBSession) {
	abs, code, msg := resolveMediaPath(session, r.URL.Query().Get("share"), r.URL.Query().Get("path"))
	if code != 0 {
		jsonError(w, code, msg)
		return
	}
	if !ffmpegAvailable() {
		jsonOk(w, map[string]interface{}{"ok": true, "ffmpeg": false, "streams": []MediaStreamInfo{}, "duration": 0})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", abs,
	).Output()
	if err != nil {
		jsonError(w, 500, "No se pudo analizar el fichero")
		return
	}
	streams, duration, err := parseProbeJSON(out)
	if err != nil {
		jsonError(w, 500, "Salida de ffprobe ilegible")
		return
	}
	jsonOk(w, map[string]interface{}{
		"ok": true, "ffmpeg": true, "streams": streams, "duration": duration,
	})
}

// ── Stream (remux de AUDIO) ──────────────────────────────────────────────────

// buildStreamArgs monta los args del remux de audio. PURA (testeable). El
// vídeo se COPIA tal cual (coste ~0) y solo se recodifica el audio Dolby/DTS
// a AAC estéreo — el caso "peli muda": el navegador SÍ descodifica el vídeo,
// solo estorbaba el audio. MP4 fragmentado a stdout; t = segundos de arranque
// (seek por keyframe, -ss de ENTRADA: rápido).
//
// ALCANCE (decisión de producto 2026-07-05): NimOS es un VISOR, no un media
// server. NO transcodifica vídeo: un 4K/HEVC que el navegador no descodifica
// se deriva a Jellyfin/Descargar (ver pickPlayback en el frontend). Aquí solo
// arreglamos el audio, que es barato y hace visible el 90% de los ficheros.
//
// FRAGMENTOS de 2s por TIEMPO (-frag_duration): con frag_keyframe los GOP de
// 10s del HEVC dejaban el audio un fragmento atrás → 10s de desfase (medido
// en Shelter 4K y corregido). El seek no se afecta (va por -ss en servidor).
func buildStreamArgs(abs string, t float64, audioIdx int) []string {
	args := []string{"-v", "error", "-nostdin"}
	if t > 0 {
		args = append(args, "-ss", strconv.FormatFloat(t, 'f', 3, 64))
	}
	args = append(args,
		"-i", abs,
		"-map", "0:v:0",
		"-map", fmt.Sprintf("0:a:%d", audioIdx),
		"-c:v", "copy",
		"-c:a", "aac", "-ac", "2", "-b:a", "192k",
		"-f", "mp4",
		"-movflags", "empty_moov+default_base_moof",
		"-frag_duration", "2000000",
		"pipe:1",
	)
	return args
}

func mediaStreamHTTP(w http.ResponseWriter, r *http.Request, session *DBSession) {
	abs, code, msg := resolveMediaPath(session, r.URL.Query().Get("share"), r.URL.Query().Get("path"))
	if code != 0 {
		jsonError(w, code, msg)
		return
	}
	if !ffmpegAvailable() {
		jsonError(w, 501, "ffmpeg no instalado en el NAS")
		return
	}
	t, _ := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	if t < 0 || t > 360000 {
		t = 0
	}
	audioIdx, _ := strconv.Atoi(r.URL.Query().Get("audio"))
	if audioIdx < 0 || audioIdx > 63 {
		audioIdx = 0
	}

	// CommandContext: si el cliente corta (seek, cierre de ventana), el
	// contexto muere y ffmpeg se mata solo — sin procesos zombis.
	cmd := exec.CommandContext(r.Context(), "ffmpeg", buildStreamArgs(abs, t, audioIdx)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		jsonError(w, 500, "No se pudo preparar el remux")
		return
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		jsonError(w, 500, "No se pudo arrancar ffmpeg")
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)

	// Copia con flush por trozos: el <video> empieza a reproducir en cuanto
	// llegan los primeros fragmentos del fMP4.
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64*1024)
	for {
		n, rerr := stdout.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				break // cliente desconectado → el context matará a ffmpeg
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			break
		}
	}
	cmd.Wait()
}

// ── Subtítulos internos → WebVTT ─────────────────────────────────────────────

func mediaSubsHTTP(w http.ResponseWriter, r *http.Request, session *DBSession) {
	abs, code, msg := resolveMediaPath(session, r.URL.Query().Get("share"), r.URL.Query().Get("path"))
	if code != 0 {
		jsonError(w, code, msg)
		return
	}
	if !ffmpegAvailable() {
		jsonError(w, 501, "ffmpeg no instalado en el NAS")
		return
	}
	track, _ := strconv.Atoi(r.URL.Query().Get("track"))
	if track < 0 || track > 63 {
		track = 0
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffmpeg",
		"-v", "error", "-nostdin", "-i", abs,
		"-map", fmt.Sprintf("0:s:%d", track), "-f", "webvtt", "pipe:1",
	).Output()
	if err != nil {
		// Pistas bitmap (PGS/DVD) no se pueden convertir a texto.
		jsonError(w, 415, "Esta pista de subtítulos no es convertible a texto")
		return
	}
	// Tope de cordura: un .vtt gigante no es un subtítulo.
	if len(out) > 8<<20 {
		jsonError(w, 415, "Pista de subtítulos demasiado grande")
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	io.Copy(w, strings.NewReader(string(out)))
}
