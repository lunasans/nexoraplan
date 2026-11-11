<!-- Rahmen der angemeldeten Ansicht: Seitenleiste (Handy: Leiste unten), Inhalt, Dialoge -->
<script>
  import { version } from '$app/environment';
  import { page } from '$app/state';
  import { session } from '#lib/stores.svelte.js';
  import EventDialog from './EventDialog.svelte';
  import Icon from './Icon.svelte';
  import TransactionDialog from './TransactionDialog.svelte';
  import Wordmark from './Wordmark.svelte';

  let { children } = $props();
  const path = $derived(page.url.pathname);
  const inBudget = $derived(path.startsWith('/budget'));
  const inSettings = $derived(path.startsWith('/einstellungen'));
  const inWeek = $derived(!inBudget && !inSettings);
</script>

<section class="app">
  <aside class="sidebar">
    <a class="logo" href="/" aria-label="nexora/plan – Woche"><Wordmark /></a>
    <nav class="sidenav" aria-label="Bereiche">
      <a id="nav-week" class:active={inWeek} href="/"><Icon name="calendar" />Woche</a>
      <a id="nav-budget" class:active={inBudget} href="/budget"><Icon name="wallet" />Budget</a>
      <a id="nav-settings" class:active={inSettings} href="/einstellungen"><Icon name="settings" />Einstellungen</a>
    </nav>
    <div class="sidebar-foot">
      <span class="user" title={session.user.email}>{session.user.email}</span>
      {#if /^\d{4}\./.test(version)}<span>Version {version}</span>{/if}
    </div>
  </aside>

  <main class="main">
    {@render children()}
  </main>

  <nav class="bottomnav" aria-label="Bereiche">
    <a class:active={inWeek} href="/"><Icon name="calendar" /><span>Woche</span></a>
    <a class:active={inBudget} href="/budget"><Icon name="wallet" /><span>Budget</span></a>
    <a class:active={inSettings} href="/einstellungen"><Icon name="settings" /><span>Einstellungen</span></a>
  </nav>

  <EventDialog />
  <TransactionDialog />
</section>
