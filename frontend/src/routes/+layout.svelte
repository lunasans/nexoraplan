<script>
  // Schriften lokal wie im Panel (kein externer Font-Dienst)
  import '@fontsource-variable/bricolage-grotesque/opsz.css';
  import '@fontsource-variable/jetbrains-mono';
  import '../app.css';
  import { api } from '#lib/api.js';
  import AppShell from '#lib/components/AppShell.svelte';
  import Login from '#lib/components/Login.svelte';
  import Toast from '#lib/components/Toast.svelte';
  import { session } from '#lib/stores.svelte.js';

  let { children } = $props();
  let error = $state('');

  $effect(() => {
    checkSession();
  });

  async function checkSession() {
    try {
      session.user = await api('GET', '/api/me');
    } catch (err) {
      if (err.status !== 401) {
        error = err.message;
        return;
      }
      try {
        session.setupNeeded = (await api('GET', '/api/setup')).needed;
      } catch (e) {
        error = e.message;
        return;
      }
    }
    session.checked = true;
  }
</script>

{#if error}
  <div class="center"><p class="error">nexora/plan ist nicht erreichbar: {error}</p></div>
{:else if !session.checked}
  <div class="center"><p class="muted">Lade …</p></div>
{:else if !session.user}
  <Login />
{:else}
  <AppShell>{@render children()}</AppShell>
{/if}
<Toast />
