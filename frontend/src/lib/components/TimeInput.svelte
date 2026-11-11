<!--
  Uhrzeit immer im 24-Stunden-Format (<input type="time"> zeigt in einem
  englischen Browser AM/PM). value ist „09:30“. Eingaben wie 9, 930, 9.30 oder
  9:30 werden beim Verlassen des Felds übernommen.
-->
<script>
  import { untrack } from 'svelte';

  let { value = $bindable(''), name = undefined, id = undefined, required = false, label = 'Uhrzeit', onchange = undefined } = $props();

  let text = $state('');
  let input = $state();

  $effect(() => {
    const v = value;
    untrack(() => {
      if (parse(text) !== v) text = v || '';
    });
  });

  function parse(s) {
    s = (s || '').trim();
    if (!s) return '';
    const m = s.match(/^(\d{1,2})(?:[:.,h ]?(\d{2}))?\s*(?:uhr)?$/i);
    if (!m) return null;
    const h = Number(m[1]);
    const min = Number(m[2] ?? 0);
    if (h > 23 || min > 59) return null;
    return `${String(h).padStart(2, '0')}:${String(min).padStart(2, '0')}`;
  }

  function commit() {
    const t = parse(text);
    if (t === null) {
      input.setCustomValidity('Bitte als HH:MM eingeben, z. B. 18:30');
      return;
    }
    input.setCustomValidity('');
    text = t;
    if (t !== value) {
      const old = value;
      value = t;
      onchange?.(t, old);
    }
  }
</script>

<input
  bind:this={input}
  {id}
  {name}
  {required}
  type="text"
  inputmode="numeric"
  autocomplete="off"
  placeholder="HH:MM"
  maxlength="5"
  aria-label={id ? undefined : label}
  bind:value={text}
  oninput={() => input.setCustomValidity('')}
  onblur={commit}
  onkeydown={(e) => e.key === 'Enter' && commit()}
/>
