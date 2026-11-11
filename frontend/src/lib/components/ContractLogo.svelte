<!-- Logo eines Abos/Vertrags; ohne Logo der Anfangsbuchstabe -->
<script>
  let { contract, size = 32 } = $props();
  const initial = $derived((contract.title || '?').trim().charAt(0).toUpperCase());
</script>

{#if contract.logo_version}
  <img class="contract-logo" src="/api/recurring/{contract.id}/logo?v={contract.logo_version}" alt="" width={size} height={size} style="width: {size}px; height: {size}px" loading="lazy" />
{:else}
  <span class="contract-logo initial" style="width: {size}px; height: {size}px; font-size: {Math.round(size * 0.45)}px" aria-hidden="true">{initial}</span>
{/if}
