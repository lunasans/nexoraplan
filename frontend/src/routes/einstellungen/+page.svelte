<!-- Einstellungen: SOGo-Kalender verbinden, Kalender ein-/ausblenden, Konto -->
<script>
  import { api } from '#lib/api.js';
  import { calendars, handleError, loadCalendars, session, showToast } from '#lib/stores.svelte.js';
  import Icon from '#lib/components/Icon.svelte';

  let dav = $state(null);
  let form = $state({ url: '', username: '', password: '' });
  let davError = $state('');
  let busy = $state(false);
  let pw = $state({ current: '', next: '' });
  let pwError = $state('');

  $effect(() => {
    loadDav();
    loadCalendars(true);
  });

  async function loadDav() {
    try {
      dav = await api('GET', '/api/caldav');
      form = { url: dav.url || dav.suggested_url || '', username: dav.username || '', password: '' };
    } catch (err) {
      handleError(err);
    }
  }

  async function connect(event) {
    event.preventDefault();
    busy = true;
    davError = '';
    try {
      const { calendars: n } = await api('PUT', '/api/caldav', form);
      showToast(`Verbunden – ${n} ${n === 1 ? 'Kalender' : 'Kalender'} gefunden`);
      await loadDav();
      await loadCalendars(true);
    } catch (err) {
      davError = err.message;
    } finally {
      busy = false;
    }
  }

  async function disconnect() {
    if (!confirm('Verbindung zum Kalenderserver trennen? Die Termine bleiben dort erhalten.')) return;
    try {
      await api('DELETE', '/api/caldav');
      await loadDav();
      await loadCalendars(true);
      showToast('Verbindung getrennt');
    } catch (err) {
      handleError(err);
    }
  }

  async function setVisible(cal, visible) {
    try {
      await api('PUT', '/api/calendars/visibility', { href: cal.href, visible });
      cal.visible = visible;
    } catch (err) {
      handleError(err);
    }
  }

  async function setDefault(href) {
    try {
      await api('PUT', '/api/calendars/default', { href });
      calendars.default = href;
    } catch (err) {
      handleError(err);
    }
  }

  async function changePassword(event) {
    event.preventDefault();
    pwError = '';
    try {
      const { signed_out: n } = await api('POST', '/api/password', { current_password: pw.current, new_password: pw.next });
      pw = { current: '', next: '' };
      showToast(n ? `Passwort geändert – ${n} andere Sitzung(en) abgemeldet` : 'Passwort geändert');
    } catch (err) {
      pwError = err.message;
    }
  }

  async function logout() {
    try {
      await api('POST', '/api/logout');
    } finally {
      session.user = null;
    }
  }
</script>

<header class="page-head">
  <div class="page-title"><h1>Einstellungen</h1><span class="muted">{session.user.email}</span></div>
  <button type="button" onclick={logout}><Icon name="logout" />Abmelden</button>
</header>

<div class="field-row" style="grid-template-columns: repeat(auto-fit, minmax(min(100%, 360px), 1fr)); align-items: start; gap: 16px">
  <section class="card" aria-labelledby="dav-title">
    <div class="card-head">
      <h2 id="dav-title">Kalender aus SOGo</h2>
      {#if dav?.configured}<span class="chip"><span class="dot" style="background: var(--ok)"></span>verbunden</span>{/if}
    </div>
    <form class="card-body form" onsubmit={connect}>
      <p class="small muted">
        Termine aus deinem Webmail-Kalender (SOGo) erscheinen in der Woche und lassen sich hier anlegen und ändern.
        Melde dich mit deiner Postfach-Adresse an – am besten mit einem App-Passwort
        (im Panel unter Postfächer → App-Passwörter).
      </p>
      {#if dav && !dav.can_store}
        <p class="error small">Der App fehlt PLAN_ENCRYPTION_KEY – ohne ihn speichert sie kein Kalender-Passwort.</p>
      {/if}
      <label>Adresse
        <input name="caldav_url" type="url" required placeholder="https://mail.example.de/SOGo/dav/" bind:value={form.url} />
        {#if dav?.suggested_url && form.url !== dav.suggested_url}
          <button type="button" class="link small" style="justify-self: start" onclick={() => (form.url = dav.suggested_url)}>Vorschlag des Panels: {dav.suggested_url}</button>
        {/if}
      </label>
      <label>Postfach<input name="caldav_user" required autocomplete="off" placeholder="ich@example.de" bind:value={form.username} /></label>
      <label>Passwort
        <input name="caldav_password" type="password" autocomplete="new-password" placeholder={dav?.has_password ? 'gespeichert – leer lassen zum Behalten' : ''} bind:value={form.password} />
      </label>
      {#if davError}<p class="error" id="dav-error">{davError}</p>{/if}
      <div class="actions">
        {#if dav?.configured}<button type="button" class="danger spacer" onclick={disconnect}>Trennen</button>{/if}
        <button type="submit" class="primary" disabled={busy || !dav?.can_store}>{busy ? 'Prüfe …' : dav?.configured ? 'Speichern' : 'Verbinden'}</button>
      </div>
    </form>
  </section>

  <section class="card" aria-labelledby="cals-title">
    <div class="card-head">
      <h2 id="cals-title">Kalender</h2>
      <button type="button" class="ghost icon-button" aria-label="Neu laden" onclick={() => loadCalendars(true)}><Icon name="refresh" /></button>
    </div>
    {#if calendars.error}<p class="card-body error small">{calendars.error}</p>{/if}
    {#each calendars.list as c (c.href)}
      <div class="list-row">
        <label class="check" style="flex: 1 1 180px; min-width: 0">
          <input type="checkbox" checked={c.visible} onchange={(e) => setVisible(c, e.currentTarget.checked)} />
          <span class="dot" style="background: {c.color}"></span>
          <span style="overflow-wrap: anywhere">{c.name}</span>
          {#if c.read_only}<span class="muted" title="schreibgeschützt"><Icon name="lock" /></span>{/if}
        </label>
        {#if !c.read_only}
          <label class="check small muted" style="font-weight: 500">
            <input type="radio" name="default-calendar" checked={calendars.default === c.href} onchange={() => setDefault(c.href)} />Standard
          </label>
        {/if}
      </div>
    {/each}
    <p class="card-body hint">Haken: in der Woche anzeigen. „Standard“: dort landen neue Termine.</p>
  </section>

  <section class="card" aria-labelledby="pw-title">
    <div class="card-head"><h2 id="pw-title">Passwort ändern</h2></div>
    <form class="card-body form" onsubmit={changePassword}>
      <input type="email" autocomplete="username" value={session.user.email} hidden />
      <label>Bisheriges Passwort<input type="password" autocomplete="current-password" required bind:value={pw.current} /></label>
      <label>Neues Passwort<input type="password" autocomplete="new-password" required minlength="10" placeholder="mindestens 10 Zeichen" bind:value={pw.next} /></label>
      {#if pwError}<p class="error">{pwError}</p>{/if}
      <p class="hint">Alle anderen Browser werden dabei abgemeldet.</p>
      <div class="actions"><button type="submit" class="primary">Ändern</button></div>
    </form>
  </section>
</div>
