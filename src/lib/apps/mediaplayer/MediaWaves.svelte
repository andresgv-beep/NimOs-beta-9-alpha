<script>
  /**
   * MediaWaves · visualizador de ondas del modo audio
   * ──────────────────────────────────────────────────
   * Pinta en <canvas> el espectro REAL de la música (AnalyserNode de la Web
   * Audio API, FFT). Barras verde-señal con acentos morados, amplitud
   * simétrica desde el centro y contornos fundidos a negro (fade lateral +
   * viñeta radial) — el look aprobado en el mockup.
   *
   * Props:
   *   · analyser  AnalyserNode | null — si null (aún sin reproducir o la
   *     Web Audio no pudo engancharse), pinta una onda idle decorativa.
   *   · playing   bool — pausado congela la onda (no se borra).
   */
  import { onMount, onDestroy } from 'svelte';

  export let analyser = null;
  export let playing = false;

  let canvas;
  let raf = 0;
  let freq = null;
  let idlePhase = 0;

  const GREEN = '#00ff9f';
  const PURPLE = '#7f77dd';
  const BG = '#0a0a0d';

  function draw() {
    raf = requestAnimationFrame(draw);
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    const dpr = window.devicePixelRatio || 1;
    const w = canvas.clientWidth;
    const h = canvas.clientHeight;
    if (!w || !h) return;
    if (canvas.width !== w * dpr || canvas.height !== h * dpr) {
      canvas.width = w * dpr;
      canvas.height = h * dpr;
      ctx.scale(dpr, dpr);
    }

    ctx.clearRect(0, 0, w, h);
    ctx.fillStyle = BG;
    ctx.fillRect(0, 0, w, h);

    const N = 64;
    const gap = 3;
    const barW = (w - gap * (N + 1)) / N;
    const midY = h / 2;

    let levels;
    if (analyser) {
      if (!freq || freq.length !== analyser.frequencyBinCount) {
        freq = new Uint8Array(analyser.frequencyBinCount);
      }
      analyser.getByteFrequencyData(freq);
      // Muestrear los bins bajos-medios (donde vive la música) sobre N barras.
      const usable = Math.floor(freq.length * 0.72);
      levels = new Array(N);
      for (let i = 0; i < N; i++) {
        const a = Math.floor((i / N) * usable);
        const b = Math.max(a + 1, Math.floor(((i + 1) / N) * usable));
        let sum = 0;
        for (let j = a; j < b; j++) sum += freq[j];
        levels[i] = sum / (b - a) / 255;
      }
    } else {
      // Idle: onda senoidal viajera suave (sin datos reales todavía).
      if (playing) idlePhase += 0.045;
      levels = new Array(N);
      for (let i = 0; i < N; i++) {
        const t = i / (N - 1);
        levels[i] =
          0.18 +
          0.32 * Math.abs(Math.sin(t * Math.PI * 3 + idlePhase)) * (0.4 + 0.6 * Math.sin(t * Math.PI));
      }
    }

    for (let i = 0; i < N; i++) {
      const x = gap + i * (barW + gap);
      const amp = Math.max(0.03, levels[i]) * (h * 0.46);
      ctx.fillStyle = i % 6 === 0 ? PURPLE : GREEN;
      ctx.globalAlpha = 0.45 + 0.55 * Math.sin((i / (N - 1)) * Math.PI);
      // Barra simétrica desde el centro, extremos redondeados.
      const r = Math.min(barW / 2, 2);
      roundRect(ctx, x, midY - amp, barW, amp * 2, r);
    }
    ctx.globalAlpha = 1;

    // Contornos fundidos a negro: fade lateral + viñeta radial (mockup).
    const fadeW = w * 0.12;
    let g = ctx.createLinearGradient(0, 0, fadeW, 0);
    g.addColorStop(0, BG);
    g.addColorStop(1, 'rgba(10,10,13,0)');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, fadeW, h);
    g = ctx.createLinearGradient(w, 0, w - fadeW, 0);
    g.addColorStop(0, BG);
    g.addColorStop(1, 'rgba(10,10,13,0)');
    ctx.fillStyle = g;
    ctx.fillRect(w - fadeW, 0, fadeW, h);
    const rg = ctx.createRadialGradient(w / 2, h / 2, Math.min(w, h) * 0.34, w / 2, h / 2, Math.max(w, h) * 0.62);
    rg.addColorStop(0, 'rgba(10,10,13,0)');
    rg.addColorStop(1, BG);
    ctx.fillStyle = rg;
    ctx.fillRect(0, 0, w, h);
  }

  function roundRect(ctx, x, y, w, h, r) {
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.fill();
  }

  // Pausado: se para el bucle (la última onda queda congelada en el canvas).
  $: if (canvas) {
    cancelAnimationFrame(raf);
    if (playing || analyser) raf = requestAnimationFrame(draw);
    else draw();
  }

  onMount(() => {
    raf = requestAnimationFrame(draw);
  });
  onDestroy(() => cancelAnimationFrame(raf));
</script>

<canvas bind:this={canvas} class="waves"></canvas>

<style>
  .waves {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    display: block;
  }
</style>
