<!-- Reiter der Budget-Seiten -->
<script>
  import { page } from '$app/state';

  const tabs = [
    { href: '/budget', label: 'Übersicht' },
    { href: '/budget/pockets', label: 'Pockets' },
    { href: '/budget/kategorien', label: 'Kategorien, Abos & Verträge' },
  ];
</script>

<nav class="tabs" aria-label="Budget">
  {#each tabs as t (t.href)}
    <a href={t.href} class:active={page.url.pathname === t.href} aria-current={page.url.pathname === t.href ? 'page' : undefined}>{t.label}</a>
  {/each}
</nav>
