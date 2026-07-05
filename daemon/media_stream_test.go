// media_stream_test.go — Verifica la lógica pura del remux del MediaPlayer:
// parseo de ffprobe, construcción de args de ffmpeg y saneado de parámetros.

package main

import (
	"strings"
	"testing"
)

func TestParseProbeJSON_RealWorldShape(t *testing.T) {
	// Forma real de un mkv HEVC + EAC3/AC3 + subrip (el caso "peli muda").
	raw := []byte(`{
		"streams": [
			{"codec_type":"video","codec_name":"hevc"},
			{"codec_type":"audio","codec_name":"eac3","channels":6,"tags":{"language":"spa"}},
			{"codec_type":"audio","codec_name":"ac3","channels":6,"tags":{"language":"eng","title":"Comentario"}},
			{"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"spa"}},
			{"codec_type":"attachment","codec_name":"ttf"}
		],
		"format": {"duration": "7100.512"}
	}`)
	streams, dur, err := parseProbeJSON(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(streams) != 4 {
		t.Fatalf("streams = %d, want 4 (el attachment se descarta)", len(streams))
	}
	// Índices RELATIVOS por tipo (como el mapeo 0:a:N de ffmpeg).
	if streams[1].Type != "audio" || streams[1].Index != 0 || streams[1].Codec != "eac3" || streams[1].Lang != "spa" {
		t.Errorf("audio 0 mal parseado: %+v", streams[1])
	}
	if streams[2].Index != 1 || streams[2].Title != "Comentario" {
		t.Errorf("audio 1 mal parseado: %+v", streams[2])
	}
	if streams[3].Type != "subtitle" || streams[3].Index != 0 {
		t.Errorf("subtítulo mal parseado: %+v", streams[3])
	}
	if dur < 7100 || dur > 7101 {
		t.Errorf("duration = %v, want ~7100.5", dur)
	}
}

func TestBuildStreamArgs_CopyVideoTranscodeAudio(t *testing.T) {
	args := strings.Join(buildStreamArgs("/x/peli.mkv", 0, 0), " ")
	for _, want := range []string{
		"-c:v copy", "-c:a aac", "-map 0:v:0", "-map 0:a:0", "-f mp4", "pipe:1", "-nostdin",
		// Regresión del desfase A/V de 10s: fragmentos por TIEMPO, no por
		// keyframe (los GOPs de 10s del HEVC retrasaban el audio un fragmento).
		"-frag_duration 2000000",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args sin %q: %s", want, args)
		}
	}
	if strings.Contains(args, "-ss") {
		t.Error("sin t no debe haber -ss")
	}
	// NimOS es visor, no media server: el remux NUNCA transcodifica vídeo.
	if strings.Contains(args, "libx264") || strings.Contains(args, "scale=") {
		t.Error("el remux NO debe tocar el vídeo (solo copia)")
	}
}

func TestBuildStreamArgs_SeekBeforeInput(t *testing.T) {
	args := buildStreamArgs("/x/peli.mkv", 1234.5, 1)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-ss 1234.500") || !strings.Contains(joined, "-map 0:a:1") {
		t.Fatalf("seek/pista mal montados: %s", joined)
	}
	// El -ss debe ir ANTES del -i (seek de entrada por keyframe = rápido).
	ssAt, inAt := -1, -1
	for i, a := range args {
		if a == "-ss" {
			ssAt = i
		}
		if a == "-i" {
			inAt = i
		}
	}
	if ssAt < 0 || inAt < 0 || ssAt > inAt {
		t.Fatalf("-ss debe preceder a -i: %v", args)
	}
}
