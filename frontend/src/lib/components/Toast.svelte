<script>
  import { toast } from '#lib/stores.svelte.js';
</script>

{#if toast.text}
  <div class="toast" class:error={toast.error} role={toast.error ? 'alert' : 'status'}>
    <span>{toast.text}</span>
    <button type="button" onclick={() => (toast.text = '')}>OK</button>
  </div>
{/if}
