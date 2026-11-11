<!-- Anmeldung bzw. Einrichtung des einzigen Kontos -->
<script>
  import { api } from '#lib/api.js';
  import { session } from '#lib/stores.svelte.js';
  import Wordmark from './Wordmark.svelte';

  const setup = $derived(session.setupNeeded);
  let email = $state('');
  let password = $state('');
  let setupCode = $state('');
  let error = $state('');
  let busy = $state(false);

  async function submit(event) {
    event.preventDefault();
    busy = true;
    error = '';
    try {
      const user = setup
        ? await api('POST', '/api/setup', { email, password, setup_code: setupCode })
        : await api('POST', '/api/login', { email, password });
      password = '';
      setupCode = '';
      session.setupNeeded = false;
      session.user = user;
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }
</script>

<section class="auth">
  <form id="auth-form" class="auth-card" novalidate onsubmit={submit}>
    <Wordmark />
    <div>
      <h1>{setup ? 'Konto einrichten' : 'Anmelden'}</h1>
      <p class="muted small">
        {setup ? 'Wochenplan, Termine und Budget – das Konto dieser Installation.' : 'Wochenplan, Termine und Budget.'}
      </p>
    </div>
    {#if setup}
      <label>Einrichtungscode
        <input type="text" name="setup_code" required autocomplete="off" spellcheck="false" placeholder="XXXX-XXXX-XXXX-XXXX" bind:value={setupCode} />
        <span class="hint">Zeigt dir das Panel: Domain → Apps → nexora/plan, Kasten „Ersteinrichtung“.</span>
      </label>
    {/if}
    <label>E-Mail
      <!-- svelte-ignore a11y_autofocus -->
      <input type="email" name="email" autocomplete="username" required autofocus bind:value={email} />
    </label>
    <label>Passwort
      <input type="password" name="password" required autocomplete={setup ? 'new-password' : 'current-password'}
        placeholder={setup ? 'mindestens 10 Zeichen' : ''} bind:value={password} />
    </label>
    {#if error}<p id="auth-error" class="error">{error}</p>{/if}
    <button type="submit" id="auth-submit" class="primary" disabled={busy}>{setup ? 'Konto anlegen' : 'Anmelden'}</button>
  </form>
</section>
