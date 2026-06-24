<script>
  /**
   * Spinner · Spinner ASCII estilo terminal
   * ─────────────────────────────────────────
   * Uso:
   *   <Spinner />                    → | / - \ rotando
   *   <Spinner variant="dots" />     → . .. ...
   *   <Spinner variant="arrows" />   → ← ↑ → ↓
   *
   * Si quieres un spinner circular SVG clásico:
   *   <Spinner variant="ring" />
   */
  import { onMount, onDestroy } from 'svelte';

  export let variant = 'classic';
  export let speed = 120;

  const FRAMES = {
    classic: ['|', '/', '-', '\\'],
    dots:    ['.  ', '.. ', '...', '   '],
    arrows:  ['←', '↑', '→', '↓'],
    bar:     ['[    ]', '[=   ]', '[==  ]', '[=== ]', '[====]', '[ ===]', '[  ==]', '[   =]'],
    braille: ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'],
  };

  let frame = 0;
  let interval;

  onMount(() => {
    if (variant !== 'ring') {
      interval = setInterval(() => {
        frame = (frame + 1) % (FRAMES[variant] || FRAMES.classic).length;
      }, speed);
    }
  });

  onDestroy(() => {
    if (interval) clearInterval(interval);
  });

  $: char = (FRAMES[variant] || FRAMES.classic)[frame] || '';
</script>

{#if variant === 'ring'}
  <span class="ring" aria-label="loading"></span>
{:else}
  <span class="ascii">{char}</span>
{/if}

<style>
  .ascii {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--accent);
    display: inline-block;
    min-width: 1em;
  }
  .ring {
    display: inline-block;
    width: 14px;
    height: 14px;
    border: 2px solid rgba(255, 255, 255, 0.1);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: ring-spin 0.8s linear infinite;
  }
  @keyframes ring-spin {
    to { transform: rotate(360deg); }
  }
</style>
