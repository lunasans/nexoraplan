<!-- Budget: Monatsübersicht mit Kategorien und allen Buchungen -->
<script>
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api } from '#lib/api.js';
  import { addMonths, money, monthTitle, shortDate, weekdayShort } from '#lib/format.js';
  import { categories, handleError, loadCategories, openTransaction } from '#lib/stores.svelte.js';
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
  // Nur Ausgaben in Kategorien mit Budget zählen gegen das Budget (Miete ohne Budget nicht)
  const remaining = $derived(data ? data.categories.filter((c) => c.budget_cents > 0).reduce((s, c) => s + c.budget_cents - c.spent_cents, 0) : 0);

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
  <a href="/budget/kategorien">Kategorien &amp; feste Buchungen</a>
</nav>

{#if data}
  <div class="tiles">
    <div class="tile"><span class="label">Einnahmen</span><span class="value pos">{money(data.income_cents)}</span></div>
    <div class="tile"><span class="label">Ausgaben</span><span class="value">{money(data.expense_cents)}</span></div>
    <div class="tile"><span class="label">Saldo</span><span class="value" class:neg={data.balance_cents < 0}>{money(data.balance_cents)}</span></div>
    {#if data.budget_cents}
      <div class="tile"><span class="label">Budget übrig</span><span class="value" class:neg={remaining < 0}>{money(remaining)}</span>
        <span class="small muted">von {money(data.budget_cents)}</span></div>
    {/if}
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
          </div>
        {:else}
          <p class="card-body empty">Noch keine Kategorien. <a href="/budget/kategorien">Anlegen</a></p>
        {/each}
        {#if data.uncategorized_cents}
          <div class="cat-row"><span class="name muted"><span>Ohne Kategorie</span></span><span class="nums">{money(data.uncategorized_cents)}</span></div>
        {/if}
      </div>
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
                {#if t.recurring_id}<span title="Feste Buchung"><Icon name="repeat" /></span>{/if}
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
