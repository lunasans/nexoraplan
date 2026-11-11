<!-- Kategorien mit Monatsbudget und feste Buchungen (Miete, Gehalt …) -->
<script>
  import { api } from '#lib/api.js';
  import { dayString, money, moneyInput, parseMoney, shortDate } from '#lib/format.js';
  import { categories, handleError, loadCategories, showToast } from '#lib/stores.svelte.js';
  import DateInput from '#lib/components/DateInput.svelte';
  import Icon from '#lib/components/Icon.svelte';
  import Modal from '#lib/components/Modal.svelte';

  const COLORS = ['#dc2626', '#ea580c', '#ca8a04', '#15803d', '#0d9488', '#2563eb', '#7c3aed', '#db2777', '#76736e'];

  let recurring = $state([]);
  let cat = $state(null); // Kategorie im Dialog
  let rec = $state(null); // feste Buchung im Dialog
  let error = $state('');
  let busy = $state(false);

  const catById = $derived(Object.fromEntries(categories.list.map((c) => [c.id, c])));
  const totalBudget = $derived(categories.list.reduce((s, c) => s + c.budget_cents, 0));
  const monthlyIncome = $derived(recurring.filter((r) => r.kind === 'income' && !ended(r)).reduce((s, r) => s + r.amount_cents, 0));
  const monthlyFixed = $derived(recurring.filter((r) => r.kind === 'expense' && !ended(r)).reduce((s, r) => s + r.amount_cents, 0));

  $effect(() => {
    loadCategories();
    loadRecurring();
  });

  function ended(r) {
    return r.ends_on && r.ends_on < dayString(new Date());
  }

  async function loadRecurring() {
    try {
      recurring = await api('GET', '/api/recurring');
    } catch (err) {
      handleError(err);
    }
  }

  function editCat(c) {
    error = '';
    cat = c
      ? { id: c.id, name: c.name, color: c.color, budget: moneyInput(c.budget_cents) }
      : { id: 0, name: '', color: COLORS[categories.list.length % COLORS.length], budget: '' };
  }

  async function saveCat(event) {
    event.preventDefault();
    const cents = cat.budget.trim() === '' ? 0 : parseMoney(cat.budget);
    if (cents === null) {
      error = 'Budget bitte wie 250,00 eingeben (leer = kein Budget)';
      return;
    }
    busy = true;
    const body = { name: cat.name, color: cat.color, budget_cents: cents };
    try {
      if (cat.id) await api('PUT', `/api/categories/${cat.id}`, body);
      else await api('POST', '/api/categories', body);
      cat = null;
      await loadCategories();
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }

  async function deleteCat() {
    if (!confirm(`Kategorie „${cat.name}“ löschen? Die Buchungen bleiben, nur ohne Kategorie.`)) return;
    try {
      await api('DELETE', `/api/categories/${cat.id}`);
      cat = null;
      await loadCategories();
      showToast('Kategorie gelöscht');
    } catch (err) {
      handleError(err);
    }
  }

  function editRec(r) {
    error = '';
    const today = dayString(new Date());
    rec = r
      ? { id: r.id, title: r.title, kind: r.kind, amount: moneyInput(r.amount_cents), category: r.category_id ?? '', day: r.day, starts_on: r.starts_on, ends_on: r.ends_on ?? '' }
      : { id: 0, title: '', kind: 'expense', amount: '', category: '', day: 1, starts_on: today.slice(0, 8) + '01', ends_on: '' };
  }

  async function saveRec(event) {
    event.preventDefault();
    const cents = parseMoney(rec.amount);
    if (cents === null) {
      error = 'Bitte einen Betrag wie 850,00 eingeben';
      return;
    }
    busy = true;
    const body = {
      title: rec.title, kind: rec.kind, amount_cents: cents, category_id: rec.category === '' ? null : Number(rec.category),
      day: Number(rec.day), starts_on: rec.starts_on, ends_on: rec.ends_on || null,
    };
    try {
      if (rec.id) await api('PUT', `/api/recurring/${rec.id}`, body);
      else await api('POST', '/api/recurring', body);
      showToast(rec.id ? 'Feste Buchung gespeichert' : 'Feste Buchung angelegt – fällige Monate sind gebucht');
      rec = null;
      await loadRecurring();
    } catch (err) {
      error = err.message;
    } finally {
      busy = false;
    }
  }

  async function deleteRec() {
    if (!confirm(`„${rec.title}“ löschen? Bereits gebuchte Monate bleiben erhalten.`)) return;
    try {
      await api('DELETE', `/api/recurring/${rec.id}`);
      rec = null;
      await loadRecurring();
      showToast('Feste Buchung gelöscht');
    } catch (err) {
      handleError(err);
    }
  }
</script>

<header class="page-head">
  <div class="page-title"><h1>Budget</h1><span class="muted">Kategorien und feste Buchungen</span></div>
</header>

<nav class="tabs" aria-label="Budget">
  <a href="/budget">Übersicht</a>
  <a class="active" href="/budget/kategorien" aria-current="page">Kategorien &amp; feste Buchungen</a>
</nav>

<div class="field-row" style="grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr)); align-items: start; gap: 16px">
  <section class="card" aria-labelledby="cat-title">
    <div class="card-head">
      <h2 id="cat-title">Kategorien</h2>
      <button type="button" onclick={() => editCat(null)}><Icon name="plus" />Kategorie</button>
    </div>
    {#each categories.list as c (c.id)}
      <div class="list-row">
        <span class="dot" style="background: {c.color}; width: 12px; height: 12px"></span>
        <div class="grow"><span>{c.name}</span><span class="small muted">{c.budget_cents ? `${money(c.budget_cents)} im Monat` : 'ohne Budget'}</span></div>
        <div class="buttons"><button type="button" class="icon-button" aria-label="{c.name} bearbeiten" onclick={() => editCat(c)}><Icon name="pencil" /></button></div>
      </div>
    {:else}
      <p class="card-body empty">Lege Kategorien wie „Lebensmittel“ oder „Freizeit“ an und gib ihnen ein Monatsbudget.</p>
    {/each}
    {#if totalBudget}<div class="list-row"><div class="grow"><span>Summe Monatsbudget</span></div><span class="mono">{money(totalBudget)}</span></div>{/if}
  </section>

  <section class="card" aria-labelledby="rec-title">
    <div class="card-head">
      <h2 id="rec-title">Feste Buchungen</h2>
      <button type="button" onclick={() => editRec(null)}><Icon name="plus" />Feste Buchung</button>
    </div>
    {#each recurring as r (r.id)}
      <div class="list-row" style:opacity={ended(r) ? 0.55 : 1}>
        <div class="grow">
          <span>{r.title}</span>
          <span class="small muted">
            am {r.day}. jeden Monats{r.category_id && catById[r.category_id] ? ` · ${catById[r.category_id].name}` : ''}{r.ends_on ? ` · bis ${shortDate(r.ends_on, true)}` : ''}
          </span>
        </div>
        <span class="mono" class:ok-text={r.kind === 'income'}>{r.kind === 'income' ? '+' : '−'}{money(r.amount_cents)}</span>
        <div class="buttons"><button type="button" class="icon-button" aria-label="{r.title} bearbeiten" onclick={() => editRec(r)}><Icon name="pencil" /></button></div>
      </div>
    {:else}
      <p class="card-body empty">Miete, Gehalt, Abos: Feste Buchungen werden jeden Monat am eingestellten Tag automatisch gebucht.</p>
    {/each}
    {#if recurring.length}
      <div class="list-row"><div class="grow"><span>Im Monat fest</span><span class="small muted">Einnahmen {money(monthlyIncome)} · Ausgaben {money(monthlyFixed)}</span></div>
        <span class="mono">{money(monthlyIncome - monthlyFixed)}</span></div>
    {/if}
  </section>
</div>

<Modal open={!!cat} onclose={() => (cat = null)} id="cat-dialog" label="cat-dialog-title">
  {#if cat}
    <form class="form" onsubmit={saveCat} novalidate>
      <div class="dialog-head"><h2 id="cat-dialog-title">{cat.id ? 'Kategorie bearbeiten' : 'Neue Kategorie'}</h2></div>
      <div class="dialog-body">
        <!-- svelte-ignore a11y_autofocus -->
        <label>Name<input name="name" required maxlength="100" autofocus bind:value={cat.name} /></label>
        <label>Monatsbudget in €<input name="budget" inputmode="decimal" placeholder="leer = kein Budget" bind:value={cat.budget} /></label>
        <div class="form" style="gap: 4px">
          <span class="small" style="font-weight: 550">Farbe</span>
          <div class="weekdays" role="radiogroup" aria-label="Farbe">
            {#each COLORS as c (c)}
              <button type="button" role="radio" aria-checked={cat.color === c} aria-label={c} class:on={cat.color === c}
                style="background: {c}; border-color: {cat.color === c ? 'var(--ink)' : c}; outline: {cat.color === c ? '2px solid var(--ink)' : 'none'}; outline-offset: 2px; width: 32px"
                onclick={() => (cat.color = c)}></button>
            {/each}
          </div>
        </div>
        {#if error}<p class="error">{error}</p>{/if}
      </div>
      <div class="dialog-foot actions">
        {#if cat.id}<button type="button" class="danger spacer" onclick={deleteCat}><Icon name="trash" />Löschen</button>{/if}
        <button type="button" onclick={() => (cat = null)}>Abbrechen</button>
        <button type="submit" class="primary" disabled={busy}>Speichern</button>
      </div>
    </form>
  {/if}
</Modal>

<Modal open={!!rec} onclose={() => (rec = null)} id="rec-dialog" label="rec-dialog-title">
  {#if rec}
    <form class="form" onsubmit={saveRec} novalidate>
      <div class="dialog-head"><h2 id="rec-dialog-title">{rec.id ? 'Feste Buchung bearbeiten' : 'Neue feste Buchung'}</h2></div>
      <div class="dialog-body">
        <div class="seg" role="group" aria-label="Art">
          <button type="button" class:on={rec.kind === 'expense'} aria-pressed={rec.kind === 'expense'} onclick={() => (rec.kind = 'expense')}>Ausgabe</button>
          <button type="button" class:on={rec.kind === 'income'} aria-pressed={rec.kind === 'income'} onclick={() => (rec.kind = 'income')}>Einnahme</button>
        </div>
        <!-- svelte-ignore a11y_autofocus -->
        <label>Bezeichnung<input name="title" required maxlength="200" placeholder="z. B. Miete" autofocus bind:value={rec.title} /></label>
        <div class="field-row">
          <label>Betrag in €<input name="amount" inputmode="decimal" placeholder="0,00" bind:value={rec.amount} /></label>
          <label>Am Tag<input name="day" type="number" min="1" max="31" bind:value={rec.day} />
            <span class="hint">31 = immer am Monatsletzten</span></label>
        </div>
        <label>Kategorie
          <select name="category" bind:value={rec.category}>
            <option value="">Ohne Kategorie</option>
            {#each categories.list as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
          </select>
        </label>
        <div class="field-row">
          <label>Ab<DateInput name="starts_on" label="Ab" required bind:value={rec.starts_on} /></label>
          <label>Bis (optional)<DateInput name="ends_on" label="Bis" min={rec.starts_on} bind:value={rec.ends_on} /></label>
        </div>
        {#if !rec.id}<p class="hint">Liegt der Beginn in der Vergangenheit, werden die fälligen Monate gleich gebucht.</p>{/if}
        {#if error}<p class="error">{error}</p>{/if}
      </div>
      <div class="dialog-foot actions">
        {#if rec.id}<button type="button" class="danger spacer" onclick={deleteRec}><Icon name="trash" />Löschen</button>{/if}
        <button type="button" onclick={() => (rec = null)}>Abbrechen</button>
        <button type="submit" class="primary" disabled={busy}>Speichern</button>
      </div>
    </form>
  {/if}
</Modal>
