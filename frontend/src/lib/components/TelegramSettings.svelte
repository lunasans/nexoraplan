<!--
  Telegram: eigenen Bot verbinden und einstellen, welche Nachrichten kommen.
  Alles ist erst aus – jede Nachricht wird einzeln eingeschaltet.
-->
<script>
  import { api } from '#lib/api.js';
  import { handleError, showToast } from '#lib/stores.svelte.js';
  import TimeInput from './TimeInput.svelte';

  let info = $state(null);
  let token = $state('');
  let cfg = $state({ digest: false, payments: false, payment_days: 1, alerts: false, time: '07:00' });
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    load();
  });

  async function load() {
    try {
      info = await api('GET', '/api/notifications');
      cfg = { ...info.settings };
    } catch (err) {
      handleError(err);
    }
  }

  async function run(fn) {
    busy = true;
    error = '';
    try {
      await fn();
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }

  const saveToken = (e) => {
    e.preventDefault();
    return run(async () => {
      const { bot } = await api('PUT', '/api/notifications/telegram', { token });
      token = '';
      showToast(`Bot @${bot} gespeichert – jetzt in Telegram „Starten“ tippen`);
      await load();
    });
  };

  const link = () => run(async () => {
    const { chat_name } = await api('POST', '/api/notifications/telegram/link', {});
    showToast(`Verbunden mit ${chat_name} – Testnachricht ist unterwegs`);
    await load();
  });

  const save = (e) => {
    e.preventDefault();
    return run(async () => {
      await api('PUT', '/api/notifications', { ...cfg, payment_days: Number(cfg.payment_days) });
      showToast('Benachrichtigungen gespeichert');
    });
  };

  const send = (kind) => run(async () => {
    await api('POST', '/api/notifications/send', { kind });
    showToast('Gesendet – schau in Telegram');
  });

  const disconnect = () => {
    if (!confirm('Telegram trennen? Es kommen dann keine Nachrichten mehr.')) return;
    return run(async () => {
      await api('DELETE', '/api/notifications/telegram');
      await load();
    });
  };
</script>

<section class="card" aria-labelledby="tg-title">
  <div class="card-head">
    <h2 id="tg-title">Telegram</h2>
    {#if info?.linked}<span class="chip"><span class="dot" style="background: var(--ok)"></span>verbunden</span>{/if}
  </div>
  {#if info}
    <div class="card-body form">
      {#if !info.can_store}
        <p class="error small">Der App fehlt PLAN_ENCRYPTION_KEY – ohne ihn speichert sie kein Bot-Token.</p>
      {:else if !info.has_token}
        <ol class="small muted" style="margin: 0; padding-left: 18px; display: grid; gap: 4px">
          <li>In Telegram <strong>@BotFather</strong> öffnen und <code>/newbot</code> schicken.</li>
          <li>Namen vergeben – BotFather antwortet mit einem <strong>Token</strong> (123456789:ABC…).</li>
          <li>Das Token hier eintragen.</li>
        </ol>
        <form class="form" onsubmit={saveToken}>
          <label>Bot-Token
            <input name="telegram_token" autocomplete="off" spellcheck="false" placeholder="123456789:ABC…" bind:value={token} />
          </label>
          <div class="actions"><button type="submit" class="primary" disabled={busy || !token.trim()}>Prüfen &amp; speichern</button></div>
        </form>
      {:else if !info.linked}
        <p class="small">Bot <strong>@{info.bot}</strong> ist gespeichert. Öffne ihn in Telegram
          (<a href="https://t.me/{info.bot}" target="_blank" rel="noopener">t.me/{info.bot}</a>), tippe <strong>Starten</strong> und dann hier:</p>
        <div class="actions">
          <button type="button" class="danger spacer" onclick={disconnect} disabled={busy}>Token entfernen</button>
          <button type="button" class="primary" id="tg-link" onclick={link} disabled={busy}>Verknüpfen</button>
        </div>
      {:else}
        <p class="small muted">Nachrichten von <strong>@{info.bot}</strong> an {info.chat_name}. Einmal täglich zur eingestellten Uhrzeit –
          Warnungen nur einmal je Ereignis.</p>
        <form class="form" onsubmit={save}>
          <label class="check"><input type="checkbox" name="digest" bind:checked={cfg.digest} />
            <span>Tagesübersicht <span class="hint">– Termine, Aufgaben und Abbuchungen des Tages, Rest im Budget</span></span></label>
          <label class="check"><input type="checkbox" name="payments" bind:checked={cfg.payments} />
            <span>Abbuchungen vorher <span class="hint">– Abos &amp; Verträge mit Pocket und Zahlungsart</span></span></label>
          {#if cfg.payments}
            <label style="padding-left: 24px">Wie lange vorher
              <select name="payment_days" bind:value={cfg.payment_days}>
                <option value={1}>1 Tag</option><option value={3}>3 Tage</option><option value={7}>7 Tage</option>
              </select>
            </label>
          {/if}
          <label class="check"><input type="checkbox" name="alerts" bind:checked={cfg.alerts} />
            <span>Engpässe &amp; Budget <span class="hint">– Pocket reicht nicht, Budget oder Kategorie überschritten</span></span></label>
          <label style="max-width: 160px">Uhrzeit<TimeInput name="notify_time" label="Uhrzeit" required bind:value={cfg.time} /></label>
          <div class="actions">
            <button type="button" class="danger spacer" onclick={disconnect} disabled={busy}>Trennen</button>
            <button type="submit" class="primary" disabled={busy}>Speichern</button>
          </div>
        </form>
        <div class="actions" style="justify-content: flex-start">
          <span class="small muted">Jetzt senden:</span>
          <button type="button" onclick={() => send('digest')} disabled={busy}>Übersicht</button>
          <button type="button" onclick={() => send('payments')} disabled={busy}>Abbuchungen</button>
          <button type="button" onclick={() => send('alerts')} disabled={busy}>Warnungen</button>
        </div>
      {/if}
      {#if error}<p class="error" id="tg-error">{error}</p>{/if}
    </div>
  {/if}
</section>
