<!--
  Dialogfenster auf Basis von <dialog>. Schließt der Nutzer selbst (Escape),
  wird onclose aufgerufen – der Aufrufer setzt dann seinen Zustand zurück.
-->
<script>
  let { open, onclose, id, label, class: className = '', onkeydown, children } = $props();

  let dialog = $state();
  // Haben wir selbst geschlossen (weil open false wurde)? Dann ist das
  // close-Ereignis kein Abbruch durch den Nutzer. Wichtig: Der Browser meldet
  // es erst einen Moment später – womöglich, wenn schon die nächste Abfrage
  // offen ist. Ohne diese Marke würde es dann die neue Abfrage abbrechen.
  let closedByUs = false;

  $effect(() => {
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) {
      closedByUs = true;
      dialog.close();
    }
  });

  function handleClose() {
    if (closedByUs) {
      closedByUs = false;
      return;
    }
    onclose?.();
  }
</script>

<dialog bind:this={dialog} {id} class={className} aria-labelledby={label} onclose={handleClose} {onkeydown}>
  {#if open}
    {@render children()}
  {/if}
</dialog>
