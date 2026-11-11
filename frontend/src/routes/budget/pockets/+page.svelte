<!--
  Pockets: Unterkonten (z. B. bei der C24 Bank), auf die monatlich ein fester
  Betrag geht. Je Pocket: drauf − Abos & Verträge (anteilig) = frei − Ausgaben
  = übrig. Reicht der Betrag nicht für die Verträge, gibt es eine Warnung.
-->
<script>
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api } from '#lib/api.js';
  import { addMonths, dayString, money, moneyInput, monthTitle, parseMoney, parseSignedMoney, shortDate } from '#lib/format.js';
  import { handleError, loadPockets, showToast } from '#lib/stores.svelte.js';
  import BudgetTabs from '#lib/components/BudgetTabs.svelte';
  import DateInput from '#lib/components/DateInput.svelte';
  import Icon from '#lib/components/Icon.svelte';
  import Modal from '#lib/components/Modal.svelte';

  const COLORS = ['#2563eb', '#15803d', '#ca8a04', '#ea580c', '#dc2626', '#7c3aed', '#db2777', '#0d9488', '#76736e'];

  let data = $state(null);
  let edit = $state(null);
  let error = $state('');
  let busy = $state(false);
  let details = $state({}); // Pocket-ID → Vorschau mit allen Posten (aufgeklappt)
  const month = $derived(page.url.searchParams.get('m') || '');
  const real = $derived((data?.pockets ?? []).filter((p) => p.id));
  const none = $derived((data?.pockets ?? []).find((p) => !p.id));
  const shortTotal = $derived(real.reduce((s, p) => s + p.short_cents, 0));

  $effect(() => {
    load(month);
  });

  async function load(m = month) {
    try {
      data = await api('GET', `/api/budget${m ? `?month=${m}` : ''}`);
    } catch (err) {
      handleError(err);
    }
  }

  function go(delta) {
    goto(`/budget/pockets?m=${addMonths(data.month, delta)}`, { keepFocus: true, noScroll: true });
  }

  function open(p) {
    error = '';
    const today = dayString(new Date());
    edit = p
      ? { id: p.id, name: p.name, color: p.color, monthly: moneyInput(p.monthly_cents), depositDay: p.deposit_day || 1,
          balance: p.balance_cents === null ? '' : (p.balance_cents < 0 ? '-' : '') + (moneyInput(Math.abs(p.balance_cents)) || '0,00'),
          balanceOn: p.balance_on || today }
      : { id: 0, name: '', color: COLORS[real.length % COLORS.length], monthly: '', depositDay: 1, balance: '', balanceOn: today };
  }

  async function save(event) {
    event.preventDefault();
    const cents = edit.monthly.trim() === '' ? 0 : parseMoney(edit.monthly);
    if (cents === null) {
      error = 'Bitte einen Betrag wie 400,00 eingeben';
      return;
    }
    let balance = null;
    if (edit.balance.trim() !== '') {
      balance = parseSignedMoney(edit.balance);
      if (balance === null) {
        error = 'Stand bitte wie 120,00 eingeben (auch 0 oder -20,00)';
        return;
      }
    }
    busy = true;
    const body = {
      name: edit.name, color: edit.color, monthly_cents: cents, deposit_day: Number(edit.depositDay) || 1,
      balance_cents: balance, balance_on: balance === null ? null : edit.balanceOn,
    };
    try {
      if (edit.id) await api('PUT', `/api/pockets/${edit.id}`, body);
      else await api('POST', '/api/pockets', body);
      showToast(edit.id ? 'Pocket gespeichert' : 'Pocket angelegt');
      if (edit.id) delete details[edit.id];
      edit = null;
      await Promise.all([load(), loadPockets()]);
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }

  async function remove() {
    if (!confirm(`Pocket „${edit.name}“ löschen? Abos, Verträge und Kategorien bleiben, nur ohne Pocket.`)) return;
    try {
      await api('DELETE', `/api/pockets/${edit.id}`);
      edit = null;
      showToast('Pocket gelöscht');
      await Promise.all([load(), loadPockets()]);
    } catch (err) {
      handleError(err);
    }
  }

  async function toggleDetails(p) {
    if (details[p.id]) {
      delete details[p.id];
      return;
    }
    try {
      details[p.id] = await api('GET', `/api/pockets/${p.id}/forecast`);
    } catch (err) {
      handleError(err);
    }
  }

  function share(p) {
    return p.monthly_cents > 0 ? (p.contracts_cents + p.spent_cents) / p.monthly_cents : p.contracts_cents + p.spent_cents > 0 ? 2 : 0;
  }
</script>

<header class="page-head">
  <div class="page-title">
    <h1>Budget</h1>
    {#if data}<span class="muted">Pockets · {monthTitle(data.month)}</span>{/if}
  </div>
  {#if data}
    <div class="nav-group">
      <button type="button" class="icon-button" aria-label="Voriger Monat" onclick={() => go(-1)}><Icon name="left" /></button>
      <button type="button" onclick={() => goto('/budget/pockets')} disabled={data.today.startsWith(data.month)}>Heute</button>
      <button type="button" class="icon-button" aria-label="Nächster Monat" onclick={() => go(1)}><Icon name="right" /></button>
    </div>
    <button type="button" class="primary" onclick={() => open(null)}><Icon name="plus" />Pocket</button>
  {/if}
</header>

<BudgetTabs />

{#if data}
  {#if shortTotal > 0}
    <div class="banner" role="status" id="pocket-warning">
      <span><strong>{money(shortTotal)} zu wenig im Monat:</strong>
        {real.filter((p) => p.short_cents).map((p) => `${p.name} braucht ${money(p.contracts_cents)} für Abos & Verträge, es gehen ${money(p.monthly_cents)} drauf`).join('; ')}.</span>
    </div>
  {/if}

  {#if real.length}
    <div class="tiles">
      <div class="tile"><span class="label">Gesamtbudget (Summe der Pockets)</span><span class="value">{money(data.plan.total_cents)}</span>
        <span class="small muted">monatlich auf {real.length} {real.length === 1 ? 'Pocket' : 'Pockets'}</span></div>
      <div class="tile"><span class="label">Abos &amp; Verträge (anteilig)</span><span class="value">{money(data.plan.contracts_cents)}</span></div>
      <div class="tile"><span class="label">Übrig im Monat</span><span class="value" class:neg={data.remaining_cents < 0}>{money(data.remaining_cents)}</span></div>
    </div>
  {/if}

  <div class="pocket-grid">
    {#each real as p (p.id)}
      {@const s = share(p)}
      <section class="card pocket" style="border-top: 4px solid {p.color}" aria-label="Pocket {p.name}">
        <div class="card-head">
          <h2>{p.name}</h2>
          <button type="button" class="icon-button ghost" aria-label="{p.name} bearbeiten" onclick={() => open(p)}><Icon name="pencil" /></button>
        </div>
        <div class="plan">
          <div class="plan-row"><span>Monatlich drauf</span><span class="mono">{money(p.monthly_cents)}</span></div>
          <div class="plan-row"><span>Aufgefüllt <span class="muted small">(Einnahme in {monthTitle(data.month).split(' ')[0]})</span></span>
            <span class="mono" class:ok-text={p.topped_up_cents > 0}>+{money(p.topped_up_cents)}</span></div>
          {#if p.contracts}
            <div class="plan-row minus"><span>Abos &amp; Verträge <span class="muted small">({p.contracts}, anteilig)</span></span><span class="mono">−{money(p.contracts_cents)}</span></div>
            <div class="plan-row sum"><span>Frei</span><span class="mono" class:neg={p.free_cents < 0}>{money(p.free_cents)}</span></div>
          {/if}
          {#if p.spent_cents}
            <div class="plan-row minus"><span>Ausgaben <span class="muted small">(Kategorien des Pockets)</span></span><span class="mono">−{money(p.spent_cents)}</span></div>
            <div class="plan-row total"><span>Übrig</span><span class="mono" class:neg={p.remaining_cents < 0}>{money(p.remaining_cents)}</span></div>
          {/if}
          <span class="meter" class:over={s > 1}><span style="width: {Math.min(s, 1) * 100}%"></span></span>
          {#if p.short_cents}
            <p class="small error">Für die Verträge fehlen monatlich {money(p.short_cents)} – Dauerauftrag auf {money(p.contracts_cents)} erhöhen.</p>
          {:else if p.contracts_cents}
            <p class="small muted">Mindestens {money(p.contracts_cents)} im Monat nötig, damit auch Jahres- und Quartalsverträge gedeckt sind.</p>
          {/if}
        </div>
        {#if p.forecast}
          {@const f = p.forecast}
          <div class="forecast" class:short={f.short_on}>
            <div class="plan-row"><span>Stand {shortDate(f.start_on)}</span><span class="mono">{money(f.start_cents)}</span></div>
            {#if f.start_on !== data.today}<div class="plan-row"><span>Heute voraussichtlich</span><span class="mono">{money(f.today_cents)}</span></div>{/if}
            {#if f.short_on}
              <p class="small error" id="forecast-short-{p.id}"><strong>Am {shortDate(f.short_on, true)} fehlen {money(f.short_cents)}</strong> ({f.short_title}).
                {#if p.short_cents === 0}Im Schnitt reicht der Betrag – es ist nur noch nicht genug angespart.{/if}</p>
            {:else}
              <p class="small ok-text">Reicht bis {shortDate(f.until, true)} – niedrigster Stand {money(f.lowest_cents)} am {shortDate(f.lowest_on, true)}.</p>
            {/if}
            <button type="button" class="link small" style="justify-self: start" onclick={() => toggleDetails(p)}>{details[p.id] ? 'Vorschau ausblenden' : 'Vorschau: 12 Monate'}</button>
            {#if details[p.id]}
              <div class="forecast-list">
                {#each details[p.id].events as e (e.day + e.title + e.amount_cents)}
                  <div class="forecast-row" class:neg={e.balance_cents < 0}>
                    <span class="mono small">{shortDate(e.day, true)}</span>
                    <span class="small">{e.title}</span>
                    <span class="mono small" class:ok-text={e.amount_cents > 0}>{e.amount_cents > 0 ? '+' : '−'}{money(Math.abs(e.amount_cents))}</span>
                    <span class="mono small balance">{money(e.balance_cents)}</span>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        {:else if p.contracts}
          <p class="small muted" style="padding: 0 14px 12px">Trag den aktuellen Stand ein (Bearbeiten), dann prüft die App jeden Fälligkeitstag.</p>
        {/if}
      </section>
    {:else}
      <section class="card">
        <div class="card-body form">
          <h2>Noch keine Pockets</h2>
          <p class="small muted">Lege deine Unterkonten an (bei C24 „Pockets“) und trage ein, wie viel monatlich auf jedes geht.
            Die Summe ist dann dein Gesamtbudget. Abos, Verträge und Kategorien ordnest du einem Pocket zu – die App zeigt je Pocket,
            was frei bleibt, und warnt, wenn der Betrag für die Verträge nicht reicht.</p>
          <div class="actions" style="justify-content: flex-start"><button type="button" class="primary" onclick={() => open(null)}><Icon name="plus" />Pocket anlegen</button></div>
        </div>
      </section>
    {/each}
    {#if none && real.length}
      <section class="card pocket" aria-label="Ohne Pocket">
        <div class="card-head"><h2 class="muted">Ohne Pocket</h2></div>
        <div class="plan">
          {#if none.contracts}<div class="plan-row"><span>Abos &amp; Verträge <span class="muted small">({none.contracts})</span></span><span class="mono">{money(none.contracts_cents)}</span></div>{/if}
          {#if none.spent_cents}<div class="plan-row"><span>Ausgaben</span><span class="mono">{money(none.spent_cents)}</span></div>{/if}
          <p class="small muted">Im Dialog von Abo, Vertrag oder Kategorie ein Pocket wählen.</p>
        </div>
      </section>
    {/if}
  </div>
{:else}
  <p class="muted">Lade …</p>
{/if}

<Modal open={!!edit} onclose={() => (edit = null)} id="pocket-dialog" label="pocket-dialog-title">
  {#if edit}
    <form class="form" onsubmit={save} novalidate>
      <div class="dialog-head"><h2 id="pocket-dialog-title">{edit.id ? 'Pocket bearbeiten' : 'Neues Pocket'}</h2></div>
      <div class="dialog-body">
        <!-- svelte-ignore a11y_autofocus -->
        <label>Name<input name="name" required maxlength="100" placeholder="z. B. Versicherungen, Haushalt" autofocus bind:value={edit.name} /></label>
        <div class="field-row">
          <label>Monatlich drauf in €<input name="monthly" inputmode="decimal" placeholder="z. B. 50,00" bind:value={edit.monthly} /></label>
          <label>Kommt am Tag<input name="deposit_day" type="number" min="1" max="31" bind:value={edit.depositDay} /></label>
        </div>
        <span class="hint">Was jeden Monat auf das Pocket geht, z. B. per Dauerauftrag, und an welchem Tag.</span>
        <div class="field-row">
          <label>Aktueller Stand in € (optional)<input name="balance" inputmode="decimal" placeholder="z. B. 120,00" bind:value={edit.balance} /></label>
          <label>Stand vom<DateInput name="balance_on" label="Stand vom" bind:value={edit.balanceOn} /></label>
        </div>
        <span class="hint">Mit Stand rechnet die App 12 Monate voraus und warnt, wenn an einem Fälligkeitstag zu wenig im Pocket ist.
          Leer lassen = keine Vorschau.</span>
        <div class="form" style="gap: 4px">
          <span class="small" style="font-weight: 550">Farbe</span>
          <div class="weekdays" role="radiogroup" aria-label="Farbe">
            {#each COLORS as c (c)}
              <button type="button" role="radio" aria-checked={edit.color === c} aria-label={c}
                style="background: {c}; border-color: {edit.color === c ? 'var(--ink)' : c}; outline: {edit.color === c ? '2px solid var(--ink)' : 'none'}; outline-offset: 2px; width: 32px"
                onclick={() => (edit.color = c)}></button>
            {/each}
          </div>
        </div>
        {#if error}<p class="error">{error}</p>{/if}
      </div>
      <div class="dialog-foot actions">
        {#if edit.id}<button type="button" class="danger spacer" onclick={remove}><Icon name="trash" />Löschen</button>{/if}
        <button type="button" onclick={() => (edit = null)}>Abbrechen</button>
        <button type="submit" class="primary" disabled={busy}>Speichern</button>
      </div>
    </form>
  {/if}
</Modal>
