<script>
  import { onMount } from 'svelte';
  import { appState, init } from '$lib/stores/auth.js';
  import { loadPrefs } from '$lib/stores/theme.js';
  import Login from '$lib/components/Login.svelte';
  import SetupWizard from '$lib/components/SetupWizard.svelte';
  import Desktop from '$lib/components/Desktop.svelte';
  import MobileShell from '$lib/mobile/MobileShell.svelte';
  import { isMobile } from '$lib/stores/viewport.js';
  import Spinner from '$lib/ui/Spinner.svelte';

  let prefsReady = false;

  onMount(async () => {
    await init();
    await loadPrefs();
    prefsReady = true;
  });
</script>

{#if $appState === 'loading' || ($appState === 'desktop' && !prefsReady)}
  <div class="boot">
    <div class="boot-inner">
      <Spinner variant="braille" />
      <span class="boot-text">loading nimos...</span>
    </div>
  </div>
{:else if $appState === 'wizard'}
  <SetupWizard />
{:else if $appState === 'login'}
  <Login />
{:else if $appState === 'desktop'}
  {#if $isMobile}
    <MobileShell />
  {:else}
    <Desktop />
  {/if}
{/if}

<style>
  .boot {
    width: 100%;
    height: 100vh;
    background: var(--bg);
    display: flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-mono);
  }
  .boot-inner {
    display: flex;
    align-items: center;
    gap: 12px;
    color: var(--fg-dim);
    font-size: 11px;
    letter-spacing: 1.5px;
    text-transform: uppercase;
  }
  .boot-text {
    color: var(--accent);
  }
</style>
