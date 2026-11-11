<!-- Wochenansicht: Termine, Aufgaben und Ausgaben je Tag, Budget der Woche -->
<script>
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { api } from '#lib/api.js';
  import { addDays, eventTime, money, shortDate, txLabel, weekdayShort } from '#lib/format.js';
  import { describe } from '#lib/rrule.js';
  import { categories, handleError, loadCalendars, loadCategories, loadPockets, openEvent, openTransaction, pockets, showToast } from '#lib/stores.svelte.js';
  import Icon from '#lib/components/Icon.svelte';
  import TaskItem from '#lib/components/TaskItem.svelte';

  let week = $state(null);
  let loading = $state(false);
  let newTask = $state({}); // Eingabe je Tag ('' = Woche)

  const date = $derived(page.url.searchParams.get('d') || '');
  const catById = $derived(Object.fromEntries(categories.list.map((c) => [c.id, c])));
  const pocketById = $derived(Object.fromEntries(pockets.list.map((p) => [p.id, p])));
  const share = $derived(week && week.budget.planned_cents > 0 ? week.budget.budgeted_cents / week.budget.planned_cents : 0);

  $effect(() => {
    load(date);
  });
  $effect(() => {
    loadCalendars();
    loadCategories();
    loadPockets();
  });

  async function load(d = date) {
    loading = true;
    try {
      week = await api('GET', `/api/week${d ? `?date=${d}` : ''}`);
    } catch (err) {
      handleError(err);
    } finally {
      loading = false;
    }
  }

  const reload = () => load();

  function go(delta) {
    const target = delta === 0 ? '' : addDays(week.start, delta * 7);
    goto(target ? `/?d=${target}` : '/', { keepFocus: true, noScroll: true });
  }

  async function addTask(event, day) {
    event.preventDefault();
    const key = day ?? '';
    const title = (newTask[key] || '').trim();
    if (!title) return;
    try {
      await api('POST', '/api/tasks', day ? { day, title } : { week: week.start, title });
      newTask[key] = '';
      await load();
    } catch (err) {
      handleError(err);
    }
  }

  async function carryOver() {
    try {
      const { moved } = await api('POST', '/api/tasks/carry-over', { week: week.start });
      showToast(moved === 0 ? 'Keine offenen Aufgaben' : `${moved} ${moved === 1 ? 'Aufgabe' : 'Aufgaben'} in KW ${week.iso_week === 53 ? 1 : week.iso_week + 1} verschoben`);
      await load();
    } catch (err) {
      handleError(err);
    }
  }
</script>

{#if week}
  <header class="page-head">
    <div class="page-title">
      <h1 id="week-title">KW {week.iso_week}</h1>
      <span class="muted">{shortDate(week.start)} – {shortDate(week.end, true)}</span>
    </div>
    <div class="nav-group">
      <button type="button" class="icon-button" aria-label="Vorige Woche" onclick={() => go(-1)}><Icon name="left" /></button>
      <button type="button" onclick={() => go(0)} disabled={week.today >= week.start && week.today <= week.end}>Heute</button>
      <button type="button" class="icon-button" aria-label="Nächste Woche" onclick={() => go(1)}><Icon name="right" /></button>
    </div>
    <button type="button" class="primary" onclick={() => openEvent({ day: week.today >= week.start && week.today <= week.end ? week.today : week.start }, reload)}>
      <Icon name="plus" />Termin
    </button>
  </header>

  {#if week.calendar_error}
    <div class="banner" role="status">
      <span>Kalenderserver: {week.calendar_error}</span>
      <a href="/einstellungen" class="small">Einstellungen</a>
    </div>
  {/if}

  <div class="field-row" style="grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); align-items: start">
    <section class="card" aria-labelledby="week-tasks-title">
      <div class="card-head">
        <h2 id="week-tasks-title">Aufgaben der Woche</h2>
        {#if week.open_tasks > 0}
          <button type="button" class="ghost" onclick={carryOver} title="Offene Aufgaben in die nächste Woche schieben"><Icon name="forward" />Offene in KW {week.iso_week === 53 ? 1 : week.iso_week + 1}</button>
        {/if}
      </div>
      <div class="card-body day-section">
        {#each week.week_tasks as t (t.id)}
          <TaskItem task={t} weekStart={week.start} onchange={reload} />
        {/each}
        <form class="task-add" onsubmit={(e) => addTask(e, null)}>
          <input placeholder="+ Aufgabe für die Woche" aria-label="Neue Aufgabe für die Woche" maxlength="500" bind:value={newTask['']} />
        </form>
      </div>
    </section>

    <section class="card week-budget" aria-label="Budget der Woche">
      <div class="line">
        <h2>Ausgaben der Woche</h2>
        <span class="mono">{money(week.budget.spent_cents)}</span>
      </div>
      {#if week.budget.planned_cents}
        <span class="meter" class:over={share > 1}><span style="width: {Math.min(share, 1) * 100}%"></span></span>
        <span class="small muted">
          Budget: {money(week.budget.budgeted_cents)} von {money(week.budget.planned_cents)} ·
          {share > 1 ? `${money(week.budget.budgeted_cents - week.budget.planned_cents)} drüber` : `noch ${money(week.budget.planned_cents - week.budget.budgeted_cents)} übrig`}
        </span>
      {:else}
        <span class="small muted">Lege unter <a href="/budget">Budget</a> ein Gesamtbudget pro Monat fest – die Woche zeigt dann ihren Anteil nach Abos &amp; Verträgen.</span>
      {/if}
    </section>
  </div>

  <div class="days" aria-busy={loading}>
    {#each week.days as day (day.date)}
      {@const wd = weekdayShort(day.date)}
      <section class="day" class:weekend={wd === 'Sa' || wd === 'So'} class:today={day.date === week.today} aria-label="{wd} {shortDate(day.date)}">
        <div class="day-head">
          <span class="name">{wd}</span>
          <span class="date">{shortDate(day.date)}</span>
          {#if day.date === week.today}<span class="chip">heute</span>{/if}
        </div>

        <div class="day-section">
          <div class="section-label">Termine
            <button type="button" class="add" aria-label="Termin am {shortDate(day.date)}" onclick={() => openEvent({ day: day.date }, reload)}><Icon name="plus" /></button>
          </div>
          {#each day.events as e (e.key + e.start)}
            <button type="button" class="event" class:allday={e.all_day} onclick={() => openEvent({ event: e }, reload)}>
              <span class="bar" style="background: {e.color}"></span>
              <span>
                <span class="time">{eventTime(e, day.date)}{#if e.recurring}&nbsp;· <span title={describe(e.rrule)}>Serie</span>{/if}</span>
                <span class="title">{e.title}</span>
                {#if e.location}<span class="where">{e.location}</span>{/if}
              </span>
            </button>
          {:else}
            <span class="empty">–</span>
          {/each}
        </div>

        <div class="day-section">
          <div class="section-label">Aufgaben</div>
          {#each day.tasks as t (t.id)}
            <TaskItem task={t} weekStart={week.start} onchange={reload} />
          {/each}
          <form class="task-add" onsubmit={(e) => addTask(e, day.date)}>
            <input placeholder="+ Aufgabe" aria-label="Neue Aufgabe am {shortDate(day.date)}" maxlength="500" bind:value={newTask[day.date]} />
          </form>
        </div>

        <div class="day-section">
          <div class="section-label">Ausgaben
            <button type="button" class="add" aria-label="Ausgabe am {shortDate(day.date)}" onclick={() => openTransaction({ day: day.date }, reload)}><Icon name="plus" /></button>
          </div>
          {#each day.transactions as t (t.id)}
            <button type="button" class="tx" class:income={t.kind === 'income'} class:topup={t.kind === 'topup'} onclick={() => openTransaction({ transaction: t }, reload)}>
              {#if t.category_id && catById[t.category_id]}<span class="dot" style="background: {catById[t.category_id].color}"></span>{/if}
              {#if t.kind === 'topup'}<span class="dot" style="background: {pocketById[t.pocket_id]?.color ?? 'var(--faint)'}"></span>{/if}
              <span class="note">{txLabel(t, catById[t.category_id], (id) => pocketById[id]?.name)}</span>
              <span class="amount">{t.kind === 'expense' ? '−' : '+'}{money(t.amount_cents)}</span>
            </button>
          {/each}
          {#if day.spent_cents}
            <div class="day-sum"><span>Summe</span><span class="mono">{money(day.spent_cents)}</span></div>
          {/if}
        </div>
      </section>
    {/each}
  </div>
{:else}
  <p class="muted">Lade Woche …</p>
{/if}
