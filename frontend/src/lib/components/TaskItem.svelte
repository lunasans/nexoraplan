<!--
  Eine Aufgabe: abhaken; Klick auf den Titel öffnet die Bearbeitung direkt in
  der Zeile (umbenennen, auf einen anderen Tag legen, löschen).
-->
<script>
  import { api } from '#lib/api.js';
  import { addDays, shortDate, weekdayShort } from '#lib/format.js';
  import { handleError } from '#lib/stores.svelte.js';
  import Icon from './Icon.svelte';

  let { task, weekStart, onchange } = $props();
  let editing = $state(false);
  let title = $state('');
  let day = $state('');

  const days = $derived(Array.from({ length: 7 }, (_, i) => addDays(weekStart, i)));

  function start() {
    title = task.title;
    day = task.day ?? '';
    editing = true;
  }

  async function patch(body) {
    try {
      await api('PATCH', `/api/tasks/${task.id}`, body);
      onchange();
    } catch (err) {
      handleError(err);
    }
  }

  async function save(event) {
    event.preventDefault();
    if (!title.trim()) return remove();
    editing = false;
    const body = { title };
    if (day !== (task.day ?? '')) Object.assign(body, day ? { day } : { clear_day: true });
    await patch(body);
  }

  async function remove() {
    editing = false;
    try {
      await api('DELETE', `/api/tasks/${task.id}`);
      onchange();
    } catch (err) {
      handleError(err);
    }
  }

  function onkeydown(event) {
    if (event.key === 'Escape') {
      event.stopPropagation();
      editing = false;
    }
  }
</script>

{#if editing}
  <form class="form" style="gap: 6px; padding: 4px 0" onsubmit={save}>
    <!-- svelte-ignore a11y_autofocus -->
    <input aria-label="Titel" maxlength="500" autofocus {onkeydown} bind:value={title} />
    <div style="display: flex; gap: 6px; flex-wrap: wrap">
      <select aria-label="Tag" style="flex: 1 1 110px" bind:value={day}>
        <option value="">Ganze Woche</option>
        {#each days as d (d)}<option value={d}>{weekdayShort(d)} {shortDate(d)}</option>{/each}
      </select>
      <button type="button" class="danger icon-button" aria-label="Aufgabe löschen" onclick={remove}><Icon name="trash" /></button>
      <button type="submit" class="primary">OK</button>
    </div>
  </form>
{:else}
  <div class="task" class:done={task.done}>
    <input type="checkbox" checked={task.done} aria-label="Erledigt: {task.title}" onchange={() => patch({ done: !task.done })} />
    <span class="title" role="button" tabindex="0" title="Bearbeiten" onclick={start} onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), start())}>{task.title}</span>
  </div>
{/if}
