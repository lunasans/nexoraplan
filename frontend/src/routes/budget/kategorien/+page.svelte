<!-- Kategorien mit Monatsbudget, Abos & Verträge (monatlich bis jährlich) und feste Einnahmen -->
<script>
  import { api } from '#lib/api.js';
  import { INTERVALS, PAYMENT_METHODS, dayString, intervalText, money, moneyInput, parseMoney, paymentLabel, shortDate } from '#lib/format.js';
  import { categories, handleError, loadCategories, showToast } from '#lib/stores.svelte.js';
  import DateInput from '#lib/components/DateInput.svelte';
  import ContractLogo from '#lib/components/ContractLogo.svelte';
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
  // Anteil pro Monat: 240 € jährlich = 20 €
  const perMonth = (r) => Math.round(r.amount_cents / (r.interval_months || 1));
  const monthlyIncome = $derived(recurring.filter((r) => r.kind === 'income' && !ended(r)).reduce((s, r) => s + perMonth(r), 0));
  // Anteil pro Monat je Zahlungsart, z. B. „Lastschrift 932,99 € · Kreditkarte 12,99 €“
  const byPayment = $derived.by(() => {
    const sums = {};
    for (const r of recurring) {
      if (r.kind !== 'expense' || ended(r)) continue;
      const key = paymentLabel(r.payment_method) || 'ohne Angabe';
      sums[key] = (sums[key] || 0) + perMonth(r);
    }
    return Object.entries(sums).sort((a, b) => b[1] - a[1]);
  });
  const monthlyFixed = $derived(recurring.filter((r) => r.kind === 'expense' && !ended(r)).reduce((s, r) => s + perMonth(r), 0));

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
    if (!r) {
      rec = { id: 0, title: '', kind: 'expense', amount: '', category: '', interval: 1, first: today, ends_on: '', day: 0,
        payment: '', paymentNote: '', logoVersion: 0, logoFile: null, logoPreview: '', removeLogo: false };
      return;
    }
    // Erste Abbuchung = Monat von „starts_on“ am gebuchten Tag (31. im November = 30.)
    const [y, m] = r.starts_on.split('-').map(Number);
    const last = new Date(y, m, 0).getDate();
    const first = `${r.starts_on.slice(0, 8)}${String(Math.min(r.day, last)).padStart(2, '0')}`;
    rec = { id: r.id, title: r.title, kind: r.kind, amount: moneyInput(r.amount_cents), category: r.category_id ?? '',
      interval: r.interval_months || 1, first, ends_on: r.ends_on ?? '', day: r.day, origFirst: first,
      payment: r.payment_method || '', paymentNote: r.payment_note || '', logoVersion: r.logo_version,
      logoFile: null, logoPreview: '', removeLogo: false };
  }

  // Logo wählen: Vorschau als data:-URL (die Seite erlaubt keine blob:-Bilder)
  function chooseLogo(event) {
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = '';
    if (!file) return;
    if (!['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(file.type)) {
      error = 'Bitte ein Bild als PNG, JPG, GIF oder WebP wählen';
      return;
    }
    if (file.size > 5 * 1024 * 1024) {
      error = 'Das Bild ist größer als 5 MB';
      return;
    }
    error = '';
    rec.logoFile = file;
    rec.removeLogo = false;
    const reader = new FileReader();
    reader.onload = () => (rec.logoPreview = reader.result);
    reader.readAsDataURL(file);
  }

  function removeLogo() {
    rec.logoFile = null;
    rec.logoPreview = '';
    rec.removeLogo = true;
  }

  async function uploadLogo(id) {
    if (rec.logoFile) {
      const res = await fetch(`/api/recurring/${id}/logo`, { method: 'PUT', headers: { 'Content-Type': rec.logoFile.type }, body: rec.logoFile });
      if (!res.ok) throw new Error((await res.json().catch(() => null))?.fehler || 'Logo nicht gespeichert');
    } else if (rec.removeLogo && rec.logoVersion) {
      await api('DELETE', `/api/recurring/${id}/logo`);
    }
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
      interval_months: Number(rec.interval), starts_on: rec.first, ends_on: rec.ends_on || null,
      // Unverändertes Datum behält z. B. „am 31.“ (Monatsletzter), auch wenn der erste Monat kürzer ist
      day: rec.id && rec.first === rec.origFirst ? rec.day : Number(rec.first.slice(8)),
      payment_method: rec.payment, payment_note: rec.paymentNote,
    };
    try {
      const saved = rec.id ? await api('PUT', `/api/recurring/${rec.id}`, body) : await api('POST', '/api/recurring', body);
      await uploadLogo(saved.id);
      showToast(rec.id ? 'Gespeichert' : 'Angelegt – schon fällige Abbuchungen sind gebucht');
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
      showToast('Gelöscht');
    } catch (err) {
      handleError(err);
    }
  }
</script>

<header class="page-head">
  <div class="page-title"><h1>Budget</h1><span class="muted">Kategorien, Abos &amp; Verträge</span></div>
</header>

<nav class="tabs" aria-label="Budget">
  <a href="/budget">Übersicht</a>
  <a class="active" href="/budget/kategorien" aria-current="page">Kategorien, Abos &amp; Verträge</a>
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
      <h2 id="rec-title">Abos, Verträge &amp; feste Einnahmen</h2>
      <button type="button" onclick={() => editRec(null)}><Icon name="plus" />Abo / Vertrag</button>
    </div>
    {#each recurring as r (r.id)}
      <div class="list-row contract-row" style:opacity={ended(r) ? 0.55 : 1}>
        <ContractLogo contract={r} size={36} />
        <div class="grow">
          <span>{r.title}</span>
          <span class="small muted">
            {intervalText(r)}{r.category_id && catById[r.category_id] ? ` · ${catById[r.category_id].name}` : ''}{r.ends_on ? ` · bis ${shortDate(r.ends_on, true)}` : ''}
          </span>
          {#if r.payment_method}<span class="small muted">{paymentLabel(r.payment_method)}{r.payment_note ? ` · ${r.payment_note}` : ''}</span>{/if}
        </div>
        <div style="display: grid; text-align: right">
          <span class="mono" class:ok-text={r.kind === 'income'}>{r.kind === 'income' ? '+' : '−'}{money(r.amount_cents)}</span>
          {#if (r.interval_months || 1) > 1}<span class="small muted mono">≈ {money(perMonth(r))} / Monat</span>{/if}
        </div>
        <div class="buttons"><button type="button" class="icon-button" aria-label="{r.title} bearbeiten" onclick={() => editRec(r)}><Icon name="pencil" /></button></div>
      </div>
    {:else}
      <p class="card-body empty">Miete, Streaming, Versicherungen, Gehalt: wird automatisch am Fälligkeitstag gebucht – monatlich, vierteljährlich, halbjährlich oder jährlich.
        Ausgaben gehen anteilig vom Gesamtbudget ab.</p>
    {/each}
    {#if recurring.length}
      <div class="list-row"><div class="grow"><span>Pro Monat (anteilig)</span><span class="small muted">Einnahmen {money(monthlyIncome)} · Abos &amp; Verträge {money(monthlyFixed)}</span>
        {#if byPayment.length}<span class="small muted">{byPayment.map(([k, v]) => `${k} ${money(v)}`).join(' · ')}</span>{/if}</div>
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
      <div class="dialog-head"><h2 id="rec-dialog-title">{rec.id ? (rec.kind === 'income' ? 'Feste Einnahme bearbeiten' : 'Abo / Vertrag bearbeiten') : rec.kind === 'income' ? 'Neue feste Einnahme' : 'Neues Abo / neuer Vertrag'}</h2></div>
      <div class="dialog-body">
        <div class="seg" role="group" aria-label="Art">
          <button type="button" class:on={rec.kind === 'expense'} aria-pressed={rec.kind === 'expense'} onclick={() => (rec.kind = 'expense')}>Ausgabe</button>
          <button type="button" class:on={rec.kind === 'income'} aria-pressed={rec.kind === 'income'} onclick={() => (rec.kind = 'income')}>Einnahme</button>
        </div>
        <!-- svelte-ignore a11y_autofocus -->
        <label>Bezeichnung<input name="title" required maxlength="200" placeholder="z. B. Streaming, Haftpflicht, Miete" autofocus bind:value={rec.title} /></label>
        <div class="field-row">
          <label>Betrag in €<input name="amount" inputmode="decimal" placeholder="0,00" bind:value={rec.amount} /></label>
          <label>Abstand
            <select name="interval" bind:value={rec.interval}>
              {#each INTERVALS as i (i.months)}<option value={i.months}>{i.label}</option>{/each}
            </select>
          </label>
        </div>
        <label>Kategorie
          <select name="category" bind:value={rec.category}>
            <option value="">Ohne Kategorie</option>
            {#each categories.list as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
          </select>
        </label>
        <div class="field-row">
          <label>{rec.kind === 'income' ? 'Zahlungseingang' : 'Bezahlt per'}
            <select name="payment_method" bind:value={rec.payment}>
              <option value="">Keine Angabe</option>
              {#each PAYMENT_METHODS as p (p.value)}<option value={p.value}>{p.label}</option>{/each}
            </select>
          </label>
          <label>Konto / Karte (optional)
            <input name="payment_note" maxlength="100" placeholder="z. B. Girokonto, Visa …1234" bind:value={rec.paymentNote} />
          </label>
        </div>
        {#if rec.payment === 'card' || rec.payment === 'sepa'}<p class="hint">Keine vollständige Karten- oder Kontonummer – die letzten 4 Ziffern reichen.</p>{/if}
        <div class="form" style="gap: 4px">
          <span class="small" style="font-weight: 550">Logo (optional)</span>
          <div class="logo-field">
            {#if rec.logoPreview}
              <img class="contract-logo" src={rec.logoPreview} alt="Gewähltes Logo" width="48" height="48" style="width: 48px; height: 48px" />
            {:else}
              <ContractLogo contract={{ id: rec.id, title: rec.title, logo_version: rec.removeLogo ? 0 : rec.logoVersion }} size={48} />
            {/if}
            <label class="button" style="display: inline-flex; font-size: 14px; color: var(--text)">
              Bild wählen<input type="file" name="logo" accept="image/png,image/jpeg,image/gif,image/webp" hidden onchange={chooseLogo} />
            </label>
            {#if rec.logoPreview || (rec.logoVersion && !rec.removeLogo)}<button type="button" class="danger" onclick={removeLogo}>Entfernen</button>{/if}
          </div>
          <span class="hint">PNG, JPG, GIF oder WebP bis 5 MB – wird auf 128×128 px verkleinert.</span>
        </div>
        <div class="field-row">
          <label>{rec.id ? 'Erste Abbuchung' : 'Erste / nächste Abbuchung'}<DateInput name="starts_on" label="Erste Abbuchung" required bind:value={rec.first} /></label>
          <label>Bis (optional)<DateInput name="ends_on" label="Bis" min={rec.first} bind:value={rec.ends_on} /></label>
        </div>
        {#if rec.first}
          {@const parsed = parseMoney(rec.amount)}
          {@const when = intervalText({ interval_months: Number(rec.interval), day: rec.id && rec.first === rec.origFirst ? rec.day : Number(rec.first.slice(8)), starts_on: rec.first })}
          {@const share = parsed && Number(rec.interval) > 1 ? ` · ≈ ${money(Math.round(parsed / Number(rec.interval)))} pro Monat.` : when.endsWith('.') ? '' : '.'}
          <p class="hint">
            {when}{share}
            Fehlt der Tag in einem Monat (31.), wird am Monatsletzten gebucht.
            {#if !rec.id} Liegt das Datum in der Vergangenheit, werden die fälligen Abbuchungen gleich gebucht.{/if}
          </p>
        {/if}
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
