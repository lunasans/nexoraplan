<!--
  Termin anlegen, ändern oder löschen – eigene Termine und solche aus SOGo.
  Bei Serien gilt eine Änderung für die ganze Serie; ein einzelnes Vorkommen
  lässt sich streichen.
-->
<script>
  import { api } from '#lib/api.js';
  import { addDays, lastDay, shortDate, wallDay, wallTime, weekdayShort } from '#lib/format.js';
  import { DAY_CODES, DAY_LABELS, buildRule, dayCode, describe, emptyRule, nthLabel, parseRule } from '#lib/rrule.js';
  import { calendars, dialogs, handleError, loadCalendars, showToast } from '#lib/stores.svelte.js';
  import Icon from './Icon.svelte';
  import Modal from './Modal.svelte';

  const d = $derived(dialogs.event);
  let form = $state(null);
  let rule = $state(emptyRule());
  let occurrence = $state(null); // angeklicktes Vorkommen einer Serie
  let readOnly = $state(false);
  let busy = $state(false);
  let loadError = $state('');

  const writable = $derived(calendars.list.filter((c) => c.href === 'local' || !c.read_only));
  const calName = $derived(calendars.list.find((c) => c.href === form?.calendar)?.name ?? '');

  $effect(() => {
    if (d) open(d);
    else form = null;
  });

  async function open(data) {
    loadError = '';
    occurrence = null;
    readOnly = false;
    if (!calendars.loaded) await loadCalendars();
    if (!data.event) {
      const day = data.day;
      form = {
        key: '', calendar: writable.some((c) => c.href === calendars.default) ? calendars.default : 'local',
        title: '', location: '', note: '', all_day: false,
        startDay: day, startTime: '09:00', endDay: day, endTime: '10:00',
      };
      rule = emptyRule();
      return;
    }
    const ev = data.event;
    if (ev.recurring) occurrence = ev.occurrence;
    readOnly = ev.read_only;
    let src = ev;
    // Bei Serien den Hauptteil laden: geändert wird die Serie ab ihrem ersten Termin
    if (ev.recurring) {
      try {
        src = await api('GET', `/api/events?key=${encodeURIComponent(ev.key)}&calendar=${encodeURIComponent(ev.calendar)}`);
      } catch (err) {
        loadError = err.message;
      }
    }
    form = {
      key: ev.key, calendar: ev.calendar, title: src.title, location: src.location, note: src.note, all_day: src.all_day,
      startDay: wallDay(src.start), startTime: src.all_day ? '09:00' : wallTime(src.start),
      endDay: lastDay(src), endTime: src.all_day ? '10:00' : wallTime(src.end),
      occurrenceStart: ev.start,
    };
    rule = parseRule(src.rrule);
  }

  function close() {
    dialogs.event = null;
  }

  function done(message) {
    const cb = d?.onsaved;
    close();
    if (message) showToast(message);
    cb?.();
  }

  // Beginn verschoben: Ende mitziehen, damit die Dauer gleich bleibt
  function onStartDay(event) {
    const old = form.startDay;
    form.startDay = event.currentTarget.value;
    if (!form.startDay || !old) return;
    const shift = Math.round((new Date(form.startDay) - new Date(old)) / 86400000);
    if (form.endDay) form.endDay = addDays(form.endDay, shift);
  }

  function onStartTime(event) {
    const [oh, om] = form.startTime.split(':').map(Number);
    form.startTime = event.currentTarget.value;
    const [nh, nm] = form.startTime.split(':').map(Number);
    if (form.startDay !== form.endDay || isNaN(nh)) return;
    const [eh, em] = form.endTime.split(':').map(Number);
    const end = Math.min(23 * 60 + 59, eh * 60 + em + (nh * 60 + nm) - (oh * 60 + om));
    form.endTime = `${String(Math.floor(end / 60)).padStart(2, '0')}:${String(end % 60).padStart(2, '0')}`;
  }

  function toggleDay(code) {
    rule.byday = rule.byday.includes(code) ? rule.byday.filter((c) => c !== code) : [...rule.byday, code];
  }

  function onFreq(event) {
    rule.freq = event.currentTarget.value;
    rule.custom = '';
    if (rule.freq === 'WEEKLY' && !rule.byday.length) rule.byday = [dayCode(form.startDay)];
  }

  async function save(event) {
    event.preventDefault();
    busy = true;
    const body = {
      calendar: form.calendar, title: form.title, location: form.location, note: form.note, all_day: form.all_day,
      start: form.all_day ? form.startDay : `${form.startDay}T${form.startTime}`,
      end: form.all_day ? form.endDay : `${form.endDay}T${form.endTime}`,
      rrule: buildRule(rule, form.startDay),
    };
    try {
      if (form.key) await api('PUT', '/api/events', { ...body, key: form.key });
      else await api('POST', '/api/events', body);
      done(form.key ? 'Termin gespeichert' : 'Termin angelegt');
    } catch (err) {
      handleError(err);
    } finally {
      busy = false;
    }
  }

  async function remove(onlyThis) {
    busy = true;
    try {
      await api('POST', '/api/events/delete', {
        key: form.key, calendar: form.calendar, ...(onlyThis ? { occurrence } : {}),
      });
      done(onlyThis ? 'Termin gestrichen' : occurrence ? 'Serie gelöscht' : 'Termin gelöscht');
    } catch (err) {
      handleError(err);
    } finally {
      busy = false;
    }
  }
</script>

<Modal open={!!d} onclose={close} id="event-dialog" label="event-dialog-title">
  {#if form}
    <form class="form" onsubmit={save}>
      <div class="dialog-head">
        <h2 id="event-dialog-title">{readOnly ? form.title : form.key ? 'Termin bearbeiten' : 'Neuer Termin'}</h2>
        <button type="button" class="ghost icon-button" aria-label="Schließen" onclick={close}><Icon name="close" /></button>
      </div>
      <div class="dialog-body">
        {#if loadError}<p class="error">{loadError}</p>{/if}
        {#if readOnly}
          <p><strong>{form.all_day ? 'ganztägig' : `${form.startTime}–${form.endTime}`}</strong>
            · {weekdayShort(form.startDay)} {shortDate(form.startDay, true)}{form.endDay !== form.startDay ? ` – ${shortDate(form.endDay, true)}` : ''}</p>
          {#if form.location}<p>{form.location}</p>{/if}
          {#if rule.freq}<p class="muted"><Icon name="repeat" /> {describe(buildRule(rule, form.startDay))}</p>{/if}
          {#if form.note}<p style="white-space: pre-wrap">{form.note}</p>{/if}
          <p class="hint"><Icon name="lock" /> {calName} ist schreibgeschützt.</p>
        {:else}
          <label>Titel
            <!-- svelte-ignore a11y_autofocus -->
            <input name="title" required maxlength="500" autofocus={!form.key} bind:value={form.title} />
          </label>
          <label>Kalender
            <select name="calendar" bind:value={form.calendar} disabled={!!form.key}>
              {#each writable as c (c.href)}<option value={c.href}>{c.name}</option>{/each}
            </select>
            {#if calendars.error && !form.key}<span class="hint error">Kalenderserver: {calendars.error}</span>{/if}
          </label>
          <label class="check"><input type="checkbox" name="all_day" bind:checked={form.all_day} />Ganztägig</label>
          <div class="field-row">
            <label>Beginn<input type="date" name="start_day" required value={form.startDay} onchange={onStartDay} /></label>
            {#if !form.all_day}<label>Uhrzeit<input type="time" name="start_time" required value={form.startTime} onchange={onStartTime} /></label>{/if}
          </div>
          <div class="field-row">
            <label>Ende<input type="date" name="end_day" required min={form.startDay} bind:value={form.endDay} /></label>
            {#if !form.all_day}<label>Uhrzeit<input type="time" name="end_time" required bind:value={form.endTime} /></label>{/if}
          </div>

          <label>Wiederholen
            <select name="freq" value={rule.custom ? 'CUSTOM' : rule.freq} onchange={onFreq}>
              <option value="">Nie</option>
              <option value="DAILY">Täglich</option>
              <option value="WEEKLY">Wöchentlich</option>
              <option value="MONTHLY">Monatlich</option>
              <option value="YEARLY">Jährlich</option>
              {#if rule.custom}<option value="CUSTOM">Eigene Regel (unverändert)</option>{/if}
            </select>
            {#if rule.custom}<span class="hint mono">{rule.custom}</span>{/if}
          </label>
          {#if rule.freq && !rule.custom}
            <div class="field-row">
              <label>Alle
                <input type="number" name="interval" min="1" max="99" bind:value={rule.interval} />
                <span class="hint">{{ DAILY: 'Tage', WEEKLY: 'Wochen', MONTHLY: 'Monate', YEARLY: 'Jahre' }[rule.freq]}</span>
              </label>
              {#if rule.freq === 'MONTHLY'}
                <label>Am
                  <select bind:value={rule.monthly}>
                    <option value="day">{Number(form.startDay.slice(8))}. des Monats</option>
                    <option value="weekday">{nthLabel(form.startDay)}</option>
                  </select>
                </label>
              {/if}
            </div>
            {#if rule.freq === 'WEEKLY'}
              <div class="weekdays" role="group" aria-label="Wochentage">
                {#each DAY_CODES as code, i (code)}
                  <button type="button" class:on={rule.byday.includes(code)} aria-pressed={rule.byday.includes(code)} onclick={() => toggleDay(code)}>{DAY_LABELS[i]}</button>
                {/each}
              </div>
            {/if}
            <div class="field-row">
              <label>Endet
                <select bind:value={rule.end}>
                  <option value="never">Nie</option>
                  <option value="until">Am Datum</option>
                  <option value="count">Nach Anzahl</option>
                </select>
              </label>
              {#if rule.end === 'until'}<label>Letzter Tag<input type="date" required min={form.startDay} bind:value={rule.until} /></label>{/if}
              {#if rule.end === 'count'}<label>Anzahl<input type="number" min="1" max="999" required bind:value={rule.count} /></label>{/if}
            </div>
          {/if}
          {#if occurrence}
            <p class="hint"><Icon name="repeat" /> Serientermin – Änderungen gelten für die ganze Serie (ab {shortDate(form.startDay, true)}).</p>
          {/if}

          <label>Ort<input name="location" maxlength="500" bind:value={form.location} /></label>
          <label>Notiz<textarea name="note" rows="3" bind:value={form.note}></textarea></label>
        {/if}
      </div>
      <div class="dialog-foot actions">
        {#if form.key && !readOnly}
          {#if occurrence}
            <button type="button" class="danger" disabled={busy} onclick={() => remove(true)}>
              Nur am {shortDate(wallDay(form.occurrenceStart))} streichen
            </button>
            <button type="button" class="danger spacer" disabled={busy} onclick={() => remove(false)}>Serie löschen</button>
          {:else}
            <button type="button" class="danger spacer" disabled={busy} onclick={() => remove(false)}><Icon name="trash" />Löschen</button>
          {/if}
        {/if}
        <button type="button" onclick={close}>{readOnly ? 'Schließen' : 'Abbrechen'}</button>
        {#if !readOnly}<button type="submit" class="primary" disabled={busy}>Speichern</button>{/if}
      </div>
    </form>
  {/if}
</Modal>
