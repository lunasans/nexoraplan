<!-- Buchung (Ausgabe oder Einnahme) anlegen, ändern oder löschen -->
<script>
  import { api } from '#lib/api.js';
  import { moneyInput, parseMoney } from '#lib/format.js';
  import { categories, dialogs, handleError, loadCategories, loadPockets, pockets, showToast } from '#lib/stores.svelte.js';
  import DateInput from './DateInput.svelte';
  import Icon from './Icon.svelte';
  import Modal from './Modal.svelte';

  const d = $derived(dialogs.transaction);
  let form = $state(null);
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (d) open(d);
    else form = null;
  });

  async function open(data) {
    error = '';
    if (!categories.loaded) await loadCategories();
    if (data.transaction?.kind === 'topup' && !pockets.loaded) await loadPockets();
    const t = data.transaction;
    form = t
      ? { id: t.id, kind: t.kind, amount: moneyInput(t.amount_cents), day: t.day, category: t.category_id ?? '', note: t.note, recurring: !!t.recurring_id, pocket: t.pocket_id }
      : { id: 0, kind: data.kind ?? 'expense', amount: '', day: data.day, category: '', note: '', recurring: false };
  }

  function close() {
    dialogs.transaction = null;
  }

  function done(message) {
    const cb = d?.onsaved;
    close();
    showToast(message);
    cb?.();
  }

  async function save(event) {
    event.preventDefault();
    const cents = parseMoney(form.amount);
    if (cents === null) {
      error = 'Bitte einen Betrag wie 12,50 eingeben';
      return;
    }
    busy = true;
    const body = { kind: form.kind, amount_cents: cents, day: form.day, category_id: form.category === '' ? null : Number(form.category), note: form.note };
    try {
      if (form.id) await api('PUT', `/api/transactions/${form.id}`, body);
      else await api('POST', '/api/transactions', body);
      done(form.id ? 'Buchung gespeichert' : form.kind === 'expense' ? 'Ausgabe gebucht' : 'Einnahme gebucht');
    } catch (err) {
      handleError(err);
    } finally {
      busy = false;
    }
  }

  async function remove() {
    busy = true;
    try {
      await api('DELETE', `/api/transactions/${form.id}`);
      done('Buchung gelöscht');
    } catch (err) {
      handleError(err);
    } finally {
      busy = false;
    }
  }
</script>

<Modal open={!!d} onclose={close} id="tx-dialog" label="tx-dialog-title">
  {#if form}
    <form class="form" onsubmit={save} novalidate>
      <div class="dialog-head">
        <h2 id="tx-dialog-title">{form.kind === 'topup' ? 'Auffüllung bearbeiten' : form.id ? 'Buchung bearbeiten' : form.kind === 'expense' ? 'Neue Ausgabe' : 'Neue Einnahme'}</h2>
        <button type="button" class="ghost icon-button" aria-label="Schließen" onclick={close}><Icon name="close" /></button>
      </div>
      <div class="dialog-body">
        {#if form.kind === 'topup'}
          <p class="small">Auffüllung von <strong>Pocket {pockets.list.find((p) => p.id === form.pocket)?.name ?? '–'}</strong>.
            Sie zählt als Einnahme des Pockets, im Monat aber nicht noch einmal als Einnahme (Umbuchung).</p>
        {:else}
          <div class="seg" role="group" aria-label="Art">
            <button type="button" class:on={form.kind === 'expense'} aria-pressed={form.kind === 'expense'} onclick={() => (form.kind = 'expense')}>Ausgabe</button>
            <button type="button" class:on={form.kind === 'income'} aria-pressed={form.kind === 'income'} onclick={() => (form.kind = 'income')}>Einnahme</button>
          </div>
        {/if}
        <div class="field-row">
          <label>Betrag in €
            <!-- svelte-ignore a11y_autofocus -->
            <input name="amount" inputmode="decimal" autocomplete="off" placeholder="0,00" autofocus={!form.id} bind:value={form.amount} />
          </label>
          <label>Datum<DateInput name="day" label="Datum" required bind:value={form.day} /></label>
        </div>
        <label hidden={form.kind === 'topup'}>Kategorie
          <select name="category" bind:value={form.category}>
            <option value="">Ohne Kategorie</option>
            {#each categories.list as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
          </select>
          {#if !categories.list.length}<span class="hint">Kategorien legst du unter Budget → Kategorien an.</span>{/if}
        </label>
        <label>Notiz<input name="note" maxlength="500" placeholder="z. B. Wochenmarkt" bind:value={form.note} /></label>
        {#if form.recurring}<p class="hint"><Icon name="repeat" /> Automatisch aus einer festen Buchung – Änderungen gelten nur für diesen Monat.</p>{/if}
        {#if error}<p class="error">{error}</p>{/if}
      </div>
      <div class="dialog-foot actions">
        {#if form.id}<button type="button" class="danger spacer" disabled={busy} onclick={remove}><Icon name="trash" />Löschen</button>{/if}
        <button type="button" onclick={close}>Abbrechen</button>
        <button type="submit" class="primary" disabled={busy}>Speichern</button>
      </div>
    </form>
  {/if}
</Modal>
