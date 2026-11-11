<!-- Budget: Monatsübersicht mit Kategorien und allen Buchungen -->
<script>
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api } from '#lib/api.js';
  import { addMonths, intervalText, money, monthTitle, parseMoney, moneyInput, shortDate, weekdayShort } from '#lib/format.js';
  import { categories, handleError, loadCategories, openTransaction, showToast } from '#lib/stores.svelte.js';
  import Icon from '#lib/components/Icon.svelte';

  let data = $state(null);
  const month = $derived(page.url.searchParams.get('m') || '');
  const catById = $derived(Object.fromEntries((data?.categories ?? []).map((c) => [c.id, c])));
  const byDay = $derived.by(() => {
    const groups = [];
    for (const t of data?.transactions ?? []) {
      if (groups.at(-1)?.day !== t.day) groups.push({ day: t.day, items: [] });
      groups.at(-1).items.push(t);
    }
    return groups;
  });
  const plan = $derived(data?.plan);
  const used = $derived(plan && plan.free_cents > 0 ? data.variable_cents / plan.free_cents : 0);
  let editTotal = $state(null); // Eingabe beim Ändern des Gesamtbudgets
  let totalError = $state('');

  $effect(() => {
    load(month);
  });

  async function load(m = month) {
    try {
      data = await api('GET', `/api/budget${m ? `?month=${m}` : ''}`);
      if (!categories.loaded) loadCategories();
    } catch (err) {
      handleError(err);
    }
  }

  const reload = () => load();

  function go(delta) {
    goto(`/budget?m=${addMonths(data.month, delta)}`, { keepFocus: true, noScroll: true });
  }

  function newTx(kind) {
    const today = data.today;
    const day = today.startsWith(data.month) ? today : `${data.month}-01`;
    openTransaction({ day, kind }, reload);
  }

  function startEditTotal() {
    totalError = '';
    editTotal = moneyInput(plan.total_cents);
  }

  async function saveTotal(event, remove = false) {
    event?.preventDefault();
    const cents = remove ? 0 : parseMoney(editTotal);
    if (cents === null) {
      totalError = 'Bitte einen Betrag wie 2.000,00 eingeben';
      return;
    }
    try {
      await api('PUT', '/api/budget/total', { total_cents: cents });
      editTotal = null;
      showToast(remove ? 'Gesamtbudget entfernt' : 'Gesamtbudget gespeichert');
      await load();
    } catch (err) {
      handleError(err);
    }
  }

  function share(c) {
    return c.budget_cents > 0 ? c.spent_cents / c.budget_cents : 0;
  }
</script>

<header class="page-head">
  <div class="page-title">
    <h1>Budget</h1>
    {#if data}<span class="muted">{monthTitle(data.month)}</span>{/if}
  </div>
  {#if data}
    <div class="nav-group">
      <button type="button" class="icon-button" aria-label="Voriger Monat" onclick={() => go(-1)}><Icon name="left" /></button>
      <button type="button" onclick={() => goto('/budget')} disabled={data.today.startsWith(data.month)}>Heute</button>
      <button type="button" class="icon-button" aria-label="Nächster Monat" onclick={() => go(1)}><Icon name="right" /></button>
    </div>
    <button type="button" onclick={() => newTx('income')}><Icon name="plus" />Einnahme</button>
    <button type="button" class="primary" onclick={() => newTx('expense')}><Icon name="plus" />Ausgabe</button>
  {/if}
</header>

<nav class="tabs" aria-label="Budget">
  <a class="active" href="/budget" aria-current="page">Übersicht</a>
  <a href="/budget/kategorien">Kategorien, Abos &amp; Verträge</a>
</nav>

{#if data}
  <section class="card" aria-labelledby="plan-title">
    <div class="card-head">
      <h2 id="plan-title">Gesamtbudget</h2>
      {#if plan.total_cents && editTotal === null}<button type="button" class="ghost" onclick={startEditTotal}><Icon name="pencil" />Ändern</button>{/if}
    </div>
    {#if editTotal !== null || !plan.total_cents}
      <form class="card-body form" onsubmit={saveTotal} novalidate>
        {#if !plan.total_cents}
          <p class="small muted">Lege fest, wie viel du im Monat insgesamt ausgeben willst. Abos und Verträge gehen davon
            anteilig ab (240 € im Jahr = 20 € im Monat), der Rest ist frei für Einkäufe, Freizeit usw.</p>
        {/if}
        <div style="display: flex; gap: 8px; flex-wrap: wrap; align-items: end">
          <label style="flex: 1 1 180px">Gesamtbudget pro Monat in €
            <input name="budget_total" inputmode="decimal" placeholder="z. B. 2.000,00" bind:value={() => editTotal ?? '', (v) => (editTotal = v)} />
          </label>
          {#if plan.total_cents}
            <button type="button" class="danger" onclick={(e) => saveTotal(e, true)}>Entfernen</button>
            <button type="button" onclick={() => (editTotal = null)}>Abbrechen</button>
          {/if}
          <button type="submit" class="primary">Speichern</button>
        </div>
        {#if totalError}<p class="error">{totalError}</p>{/if}
      </form>
    {/if}
    {#if plan.total_cents || plan.free_cents}
      <div class="plan">
        {#if plan.total_cents}
          <div class="plan-row"><span>Gesamtbudget</span><span class="mono">{money(plan.total_cents)}</span></div>
          <div class="plan-row minus"><span>Abos &amp; Verträge <span class="muted small">(anteilig, {data.contracts.length})</span></span><span class="mono">−{money(plan.contracts_cents)}</span></div>
          <div class="plan-row sum"><span>Frei verfügbar</span><span class="mono" class:neg={plan.free_cents < 0}>{money(plan.free_cents)}</span></div>
        {:else}
          <div class="plan-row sum"><span>Budget der Kategorien</span><span class="mono">{money(plan.free_cents)}</span></div>
        {/if}
        <div class="plan-row minus"><span>Ausgaben <span class="muted small">(ohne Abos &amp; Verträge)</span></span><span class="mono">−{money(data.variable_cents)}</span></div>
        <div class="plan-row total"><span>Übrig</span><span class="mono" class:neg={data.remaining_cents < 0} id="budget-remaining">{money(data.remaining_cents)}</span></div>
        {#if plan.free_cents > 0}<span class="meter" class:over={used > 1}><span style="width: {Math.min(used, 1) * 100}%"></span></span>{/if}
        {#if plan.total_cents && plan.allocated_cents}
          <p class="small muted">Auf Kategorien verteilt: {money(plan.allocated_cents)} · nicht verteilt: {money(plan.free_cents - plan.allocated_cents)}</p>
        {/if}
      </div>
    {/if}
  </section>

  <div class="tiles">
    <div class="tile"><span class="label">Einnahmen</span><span class="value pos">{money(data.income_cents)}</span></div>
    <div class="tile"><span class="label">Ausgaben gebucht</span><span class="value">{money(data.expense_cents)}</span><span class="small muted">inkl. Abos &amp; Verträge</span></div>
    <div class="tile"><span class="label">Saldo</span><span class="value" class:neg={data.balance_cents < 0}>{money(data.balance_cents)}</span></div>
  </div>

  <div class="field-row" style="grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr)); align-items: start; gap: 16px">
    <section class="card" aria-labelledby="cats-title">
      <div class="card-head"><h2 id="cats-title">Kategorien</h2><a class="small" href="/budget/kategorien">Verwalten</a></div>
      <div class="cat-list">
        {#each data.categories as c (c.id)}
          {@const s = share(c)}
          <div class="cat-row">
            <span class="name"><span class="dot" style="background: {c.color}"></span><span>{c.name}</span></span>
            <span class="nums">{money(c.spent_cents)}{c.budget_cents ? ` / ${money(c.budget_cents)}` : ''}</span>
            {#if c.budget_cents}<span class="meter" class:over={s > 1}><span style="width: {Math.min(s, 1) * 100}%"></span></span>{/if}
            {#if c.fixed_cents}<span class="small muted" style="grid-column: 1 / -1">dazu {money(c.fixed_cents)} aus Abos &amp; Verträgen</span>{/if}
          </div>
        {:else}
          <p class="card-body empty">Noch keine Kategorien. <a href="/budget/kategorien">Anlegen</a></p>
        {/each}
        {#if data.uncategorized_cents}
          <div class="cat-row"><span class="name muted"><span>Ohne Kategorie</span></span><span class="nums">{money(data.uncategorized_cents)}</span></div>
        {/if}
      </div>
    </section>

    <section class="card" aria-labelledby="contracts-title">
      <div class="card-head"><h2 id="contracts-title">Abos &amp; Verträge</h2><a class="small" href="/budget/kategorien">Verwalten</a></div>
      {#each data.contracts as x (x.id)}
        <div class="list-row">
          <div class="grow"><span>{x.title}</span><span class="small muted">{intervalText(x)}</span></div>
          <div style="text-align: right; display: grid">
            <span class="mono">{money(x.amount_cents)}</span>
            {#if x.interval_months > 1}<span class="small muted mono">≈ {money(x.monthly_cents)} / Monat</span>{/if}
          </div>
        </div>
      {:else}
        <p class="card-body empty">Keine Abos oder Verträge. <a href="/budget/kategorien">Anlegen</a></p>
      {/each}
      {#if data.contracts.length}
        <div class="list-row"><div class="grow"><span>Im Monat (anteilig)</span></div><span class="mono">{money(plan.contracts_cents)}</span></div>
      {/if}
    </section>

    <section class="card" aria-labelledby="tx-title">
      <div class="card-head"><h2 id="tx-title">Buchungen</h2><span class="small muted">{data.transactions.length}</span></div>
      <div class="tx-list">
        {#each byDay as g (g.day)}
          <div class="tx-day">{weekdayShort(g.day)} {shortDate(g.day)}</div>
          {#each g.items as t (t.id)}
            {@const c = catById[t.category_id]}
            <button type="button" class="tx-row" class:income={t.kind === 'income'} onclick={() => openTransaction({ transaction: t }, reload)}>
              <span class="note">{t.note || c?.name || (t.kind === 'income' ? 'Einnahme' : 'Ausgabe')}</span>
              <span class="amount">{t.kind === 'income' ? '+' : '−'}{money(t.amount_cents)}</span>
              <span class="meta">
                {#if c}<span class="dot" style="background: {c.color}"></span>{c.name}{/if}
                {#if t.recurring_id}<span title="Abo / Vertrag"><Icon name="repeat" /></span>{/if}
              </span>
            </button>
          {/each}
        {:else}
          <p class="card-body empty">Keine Buchungen in diesem Monat.</p>
        {/each}
      </div>
    </section>
  </div>
{:else}
  <p class="muted">Lade …</p>
{/if}
