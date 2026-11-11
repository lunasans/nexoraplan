import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [
    sveltekit({
      // Die fertige Oberfläche landet in web/dist und wird von Go per
      // go:embed in die Binary eingebettet. Es gibt keinen Node-Server:
      // index.html ist die Einstiegsseite für alle Pfade (Single Page App).
      adapter: adapter({
        pages: '../web/dist',
        assets: '../web/dist',
        fallback: 'index.html',
      }),
      // Absolute Pfade zu /_app/…, damit Skripte auch unter /budget/2026-11 laden.
      paths: { relative: false },
      // Jede Minute (und wenn der Tab wieder aktiv wird) prüfen, ob auf dem
      // Server eine neue Version läuft – dann bietet die App "Neu laden" an.
      // Der Name ist die Version aus release.ps1 (Build-Datum), sonst ein Zeitstempel.
      version: { name: process.env.PLAN_VERSION || undefined, pollInterval: 60_000 },
      // Content-Security-Policy: Skripte nur von der eigenen Domain. Das
      // Startskript in index.html bekommt seinen Hash (mode: 'hash') – so
      // läuft eingeschleuster Code selbst dann nicht, wenn er in die Seite
      // gelangt. frame-ancestors setzt der Go-Server als Header (im
      // <meta>-Tag wirkt es nicht).
      csp: {
        mode: 'hash',
        directives: {
          'default-src': ['self'],
          'script-src': ['self'],
          'style-src': ['self', 'unsafe-inline'], // style="…" in Komponenten
          'img-src': ['self', 'data:'],
          'font-src': ['self', 'data:'],
          'connect-src': ['self'],
          'object-src': ['none'],
          'base-uri': ['self'],
          'form-action': ['self'],
        },
      },
    }),
  ],
  server: {
    // "npm run dev": API-Anfragen an den laufenden Go-Server weiterreichen.
    proxy: { '/api': 'http://localhost:8090' },
  },
});
