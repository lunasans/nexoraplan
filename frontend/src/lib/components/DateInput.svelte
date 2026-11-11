<!--
  Datumsfeld, das immer TT.MM.JJJJ zeigt – unabhängig von der Sprache des
  Browsers (<input type="date"> folgt ihr, ein englischer Chrome zeigt MM/DD/YYYY).
  value ist intern „2026-11-11“. Eingaben wie 11.11.2026, 11-11-2026, 11/11/26
  oder 111126 werden beim Verlassen des Felds übernommen. Der Knopf öffnet den
  Kalender des Browsers.
-->
<script>
  import { untrack } from 'svelte';
  import Icon from './Icon.svelte';

  let { value = $bindable(''), name = undefined, id = undefined, required = false, min = undefined, label = 'Datum', onchange = undefined } = $props();

  let text = $state('');
  let input = $state();
  let picker = $state();

  // Text nachziehen, wenn sich der Wert von außen ändert
  $effect(() => {
    const v = value;
    untrack(() => {
      if (parse(text) !== v) text = format(v);
    });
  });

  function format(iso) {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(iso || '')) return '';
    const [y, m, d] = iso.split('-');
    return `${d}.${m}.${y}`;
  }

  // parse: Text → „2026-11-11“, ungültig → null, leer → ''
  function parse(s) {
    s = (s || '').trim();
    if (!s) return '';
    let d, m, y;
    let match = s.match(/^(\d{1,2})[.\-/ ](\d{1,2})[.\-/ ](\d{2}|\d{4})$/);
    if (match) [, d, m, y] = match;
    else if ((match = s.match(/^(\d{2})(\d{2})(\d{2}|\d{4})$/))) [, d, m, y] = match;
    else if ((match = s.match(/^(\d{4})-(\d{2})-(\d{2})$/))) [, y, m, d] = match;
    else return null;
    y = Number(y.length === 2 ? `20${y}` : y);
    const date = new Date(y, Number(m) - 1, Number(d));
    if (date.getFullYear() !== y || date.getMonth() !== Number(m) - 1 || date.getDate() !== Number(d)) return null;
    return `${y}-${String(m).padStart(2, '0')}-${String(d).padStart(2, '0')}`;
  }

  function set(iso) {
    if (iso === value) return;
    const old = value;
    value = iso;
    onchange?.(iso, old);
  }

  function commit() {
    const iso = parse(text);
    if (iso === null) {
      input.setCustomValidity('Bitte als TT.MM.JJJJ eingeben, z. B. 24.12.2026');
      return;
    }
    if (iso && min && iso < min) {
      input.setCustomValidity(`Frühestens ${format(min)}`);
      return;
    }
    input.setCustomValidity('');
    text = format(iso);
    set(iso);
  }

  function openPicker() {
    try {
      picker.showPicker();
    } catch {
      input.focus();
    }
  }

  function onPick(event) {
    const iso = event.currentTarget.value;
    if (!iso) return;
    text = format(iso);
    input.setCustomValidity('');
    set(iso);
  }
</script>

<span class="date-input">
  <input
    bind:this={input}
    {id}
    {name}
    {required}
    type="text"
    inputmode="numeric"
    autocomplete="off"
    placeholder="TT.MM.JJJJ"
    maxlength="10"
    aria-label={id ? undefined : label}
    bind:value={text}
    oninput={() => input.setCustomValidity('')}
    onblur={commit}
    onkeydown={(e) => e.key === 'Enter' && commit()}
  />
  <button type="button" class="ghost icon-button" aria-label="Kalender öffnen" tabindex="-1" onclick={openPicker}><Icon name="calendar" /></button>
  <input bind:this={picker} class="date-picker" type="date" tabindex="-1" aria-hidden="true" value={value} {min} onchange={onPick} />
</span>
