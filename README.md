# nexora/plan

Wochenplaner mit Terminen und Budget, geschrieben in Go und Svelte, als App für
NexoraPanel. Sie läuft als einzelne Binary hinter nginx; die Oberfläche ist in die
Binary eingebettet.

```
Browser ──HTTPS──▶ nginx (NexoraPanel) ──Unix-Socket──▶ nexoraplan (systemd)
                                                           │          │
                                                       MariaDB     SOGo (CalDAV, optional)
```

## Was die App kann

**Woche**
- Ansicht nach Kalenderwoche (`KW 46 · 09.11. – 15.11.2026`), Montag bis Sonntag, mit Blättern und „Heute“.
- Pro Tag stehen dort Termine, Aufgaben und Ausgaben mit Tagessumme.
- Ausgaben der Woche siehst du gegen ihren Anteil am frei verfügbaren Monatsbudget.

**Aufgaben**
- Je Kalenderwoche, entweder einem Tag zugeordnet oder für die ganze Woche.
- Abhaken, umbenennen, auf einen anderen Tag legen, löschen – direkt in der Zeile.
- „Offene in KW 47“ schiebt alles Unerledigte in die nächste Woche. Montag bleibt dabei Montag.

**Termine**
- Eigene Termine, mit Uhrzeit oder ganztägig, auch über mehrere Tage.
- Serien: täglich, wöchentlich (mit Wochentagen), monatlich (am 31. oder „am letzten Freitag“), jährlich. Jeweils mit Intervall und Ende nach Datum oder Anzahl.
- Einzelne Vorkommen einer Serie lassen sich streichen.
- Gerechnet wird in Wanduhrzeit: Ein Termin um 9:00 bleibt nach der Zeitumstellung um 9:00.

**SOGo-Kalender** (oder jeder andere CalDAV-Server)
- Einstellungen → „Kalender aus SOGo“: Adresse (das Panel trägt sie vor), Postfach und App-Passwort.
  - Das Passwort liegt AES-256-GCM-verschlüsselt in der Datenbank, der Schlüssel nur in der Env-Datei.
- Kalender werden automatisch gefunden (`current-user-principal` → `calendar-home-set`), samt Farben aus SOGo.
  - Schreibgeschützte Kalender erkennt die App; Kalender lassen sich einzeln ausblenden.
- Termine in SOGo lassen sich anlegen, ändern und löschen, Serien eingeschlossen.
  - Gestrichene (`EXDATE`) und verschobene Einzeltermine (`RECURRENCE-ID`) werden richtig aufgelöst.
- Beim Ändern bleibt erhalten, was die App nicht anfasst: Teilnehmer, Erinnerungen, Felder anderer Programme.
  - Geschrieben wird nur, wenn der Termin seit dem Laden unverändert ist (ETag).
- Zugangsdaten gehen nur an den eingestellten Host, auch bei Weiterleitungen oder fremden Adressen in Serverantworten.

**Budget**
- **Pockets** sind Unterkonten, wie die Pockets der C24 Bank. Jedes hat einen Betrag, der monatlich draufgeht, und die **Summe ist das Gesamtbudget**.
  - Abos und Verträge werden von einem Pocket abgebucht, Kategorien gehören zu einem Pocket.
  - **Auffüllungen werden gebucht:** am Tag des Dauerauftrags als „Auffüllung Pocket …“, ab dem Monat, in dem das Pocket angelegt wurde.
    - Sie zählen als Einnahme des Pockets, im Monat aber als Umbuchung, also weder als Einnahme noch als Ausgabe. Das Gehalt zählt dadurch nicht doppelt.
    - Gelöschte Auffüllungen kommen nicht wieder. Betrag und Tag einer Auffüllung lassen sich ändern.
  - **Einkommensverteiler** (z. B. in der C24-Banking-App eingestellt): Ein Pocket lässt sich an eine feste Einnahme (Gehalt) koppeln.
    - Ist das Gehalt gebucht, folgt am selben Tag die Auffüllung, höchstens eine je Monat. Den Betrag trägst du wie in der Bank-App fest ein.
    - Wird die Gehaltsbuchung verschoben, zieht die Auffüllung mit. Wird sie gelöscht, fällt die Auffüllung weg.
    - Die Vorschau rechnet mit den Gehaltstagen.
    - Ohne Kopplung kommt die Auffüllung am festen Tag (Dauerauftrag).
  - Je Pocket rechnet die App: drauf − Abos & Verträge (anteilig) = frei − Ausgaben = übrig.
  - Reicht der Betrag nicht für die Verträge, warnt sie und nennt den fehlenden Betrag, z. B. „Dauerauftrag auf 920 € erhöhen“.
  - **Vorschau mit Stand (optional):** Stand und Datum eintragen, dazu den Tag des Dauerauftrags.
    - Die App rechnet 12 Monate voraus: +monatlicher Betrag, − jede Abbuchung der zugeordneten Verträge am Fälligkeitstag, − Ausgaben der Kategorien des Pockets seit dem Stand.
    - Sie warnt beim ersten Engpass, auch wenn der Monatsdurchschnitt reicht, z. B. „Am 15.01.2027 fehlen 185 € (Haftpflicht)“, und zeigt sonst den niedrigsten Stand.
    - Die Liste aller Posten lässt sich aufklappen.
  - Kontostände ruft die App nicht ab. Den Stand trägst du von Hand ein.
- **Gesamtbudget pro Monat** (ohne Pockets von Hand), z. B. 2.000 €.
  - Davon gehen Abos & Verträge **anteilig** ab: Ein Jahresvertrag über 240 € zählt jeden Monat mit 20 €.
  - Der Rest ist frei für Einkäufe, Freizeit usw.
  - Die Übersicht rechnet: Gesamtbudget − Abos & Verträge = frei verfügbar − Ausgaben = übrig.
- **Abos, Verträge & feste Einnahmen** (Miete, Streaming, Versicherungen, Gehalt) sind monatlich, vierteljährlich, halbjährlich oder jährlich.
  - Die App bucht sie automatisch am Fälligkeitstag. Fehlt der Tag in einem Monat (31.), bucht sie am Monatsletzten.
  - Ihre echten Abbuchungen zählen nicht noch einmal gegen den freien Teil.
  - Eine von Hand gelöschte Buchung kommt nicht wieder.
  - Jedes Abo und jeder Vertrag kann ein **Logo** haben (PNG, JPG, GIF, WebP bis 5 MB).
    - Der Server rechnet es neu auf 128×128 px als PNG und legt es in der Datenbank ab, also gleich mit im Backup.
    - SVG ist ausgeschlossen, weil es Skripte enthalten kann.
  - **Zahlungsart:** SEPA-Lastschrift, Dauerauftrag, Überweisung, Kreditkarte, PayPal, Bar oder Sonstige, dazu eine kurze Notiz wie „Visa …1234“.
    - Vollständige Karten- und Kontonummern (IBAN) lehnt die App ab.
    - Die Liste zeigt die Monatssumme je Zahlungsart.
- **Kategorien** mit Farbe ordnen Buchungen zu (optional einem Pocket) – ein eigenes Budget haben sie nicht.
- **Buchungen** (Ausgabe/Einnahme) mit Datum, Kategorie und Notiz.
  - Die Monatsübersicht zeigt Einnahmen, gebuchte Ausgaben und Saldo.
- **In der Woche** steht der Anteil des freien Budgets, und auch über den Monatswechsel stimmt die Rechnung.

**Telegram-Benachrichtigungen** (alles aus, bis du es einschaltest)
- **Eigener Bot:** In Telegram @BotFather → `/newbot`, das Token unter Einstellungen → Telegram eintragen.
  - Danach dem Bot „Starten“ schicken und „Verknüpfen“ tippen. Die App findet den Chat selbst.
  - Das Token liegt verschlüsselt in der Datenbank (Schlüssel `PLAN_ENCRYPTION_KEY`).
- **Einmal täglich zur eingestellten Uhrzeit** (Standard 07:00, nach einem Neustart am selben Tag nachgeholt), jede Nachricht einzeln:
  - **Tagesübersicht:** Termine (eigene und SOGo), Aufgaben, heutige Abbuchungen, Rest im Budget.
  - **Abbuchungen vorher:** 1, 3 oder 7 Tage vorher, mit Pocket und Zahlungsart.
  - **Engpässe & Budget:** Engpass aus der Pocket-Vorschau, Pocket im Monat zu knapp, Monatsbudget überschritten.
    - Jedes Ereignis kommt nur einmal.
    - Schlägt das Senden fehl, kommt es beim nächsten Mal.
- **Ausgehende Firewall:** Ist sie im Panel gesperrt, muss `api.telegram.org` erlaubt sein.

## Anmeldung über nexora/auth

Optional meldet man sich über die zentrale Anmeldung [nexora/auth](https://github.com/lunasans/nexoraauth) an
(Knopf „Mit nexora/auth anmelden“). Im NexoraPanel auf der Seite dieser App **Mit nexora/auth anmelden →
Verbinden** – das Panel legt dort die Anwendung an und trägt diese Werte ein:

```
PLAN_OIDC_ISSUER=https://auth.example.de
PLAN_OIDC_CLIENT_ID=…
PLAN_OIDC_CLIENT_SECRET=nxa_…
```

Rücksprung-Adresse: `<PLAN_PUBLIC_URL>/api/oidc/callback`. Zugeordnet wird über die von nexora/auth bestätigte E-Mail-Adresse des Kontos. Konten legt die Anmeldung nie neu an, und das bisherige Passwort
bleibt als Notzugang. Wer sich anmelden darf, steuert nexora/auth über Gruppen (Verwaltung → Anwendungen).

## Projektstruktur

| Pfad | Inhalt |
|---|---|
| `cmd/server/` | Einstiegspunkt, Socket-Aktivierung, Befehle `genkey`, `reset-password`, `version` |
| `internal/config/` | Einstellungen aus Umgebungsvariablen (`PLAN_*`) bzw. `.env` |
| `internal/db/` | MariaDB-Zugriff und Migrationen (`migrations/*.sql`) |
| `internal/server/` | HTTP-Router, JSON-API, Anmeldung, Woche, Termine, Budget |
| `internal/recur/` | Serienregeln (RRULE nach RFC 5545) |
| `internal/ical/` | iCalendar lesen und verlustfrei schreiben, Serien auflösen, VTIMEZONE |
| `internal/caldav/` | CalDAV-Client (RFC 4791) für SOGo |
| `internal/secret/` | Verschlüsselung von Kalender-Passwort und Bot-Token |
| `internal/telegram/` | Telegram Bot API (getMe, getUpdates, sendMessage) |
| `frontend/` | Oberfläche (SvelteKit 3 / Svelte 5), gebaut nach `web/dist` |

## Lokal entwickeln

1. Go ≥ 1.26, Node.js ≥ 22 und eine MariaDB installieren.
2. Datenbank anlegen und `.env.example` nach `.env` kopieren und ausfüllen.
   - Den Schlüssel erzeugt `go run ./cmd/server genkey`.
3. Oberfläche bauen:
   ```bash
   cd frontend && npm install && npm run build && cd ..
   ```
4. Starten: `go run ./cmd/server`, danach http://localhost:8090 öffnen.
   - Den Einrichtungscode für das Konto schreibt die App ins Log.

An der Oberfläche arbeiten: Bei laufendem Go-Server startet `npm run dev` (in `frontend`) einen
Entwicklungsserver auf http://localhost:5173, der API-Anfragen an Port 8090 weiterreicht.

**Prüfen**
- Im Ordner `frontend`: `npm run check`.
- Backend: `go test ./...`
- Die Tests mit Datenbank laufen zusätzlich mit einer leeren Testdatenbank (sie wird dabei geleert):
  ```bash
  PLAN_TEST_DSN='plan:plan@unix(/run/mysqld/mysqld.sock)/nexoraplan_test' go test ./...
  ```

## Mit NexoraPanel installieren

1. Release veröffentlichen – geht auch vom Handy:
   - **Automatisch:** Jeder Push bzw. gemergte PR auf `main` startet die Action „Release“. Sie prüft alles (Oberfläche, `go vet`, Tests mit MariaDB) und legt den GitHub-Release `v<Datum>` an (`nexoraplan-linux-amd64` + `.sha256`).
   - **Von Hand:** GitHub (auch in der App) → Actions → „Release“ → *Run workflow*.
   - Am Windows-Rechner geht weiterhin `.\release.ps1`.
   - Pull Requests werden von derselben Action nur geprüft, nicht veröffentlicht.
2. Im Panel einmalig unter **Einstellungen → App-Quelle** das Repo `lunasans/nexoraplan` und ein Lese-Token eintragen.
   - Das Token fein-granular anlegen, nur für dieses Repo, Berechtigung *Contents: Read*.
3. Domain → Reiter **Apps** → nexora/plan → *Von GitHub installieren*.
   - Das Panel legt Datenbank, Env-Datei mit Schlüssel und den Dienst `nexoraplan-<domain>` an.
   - Ist SOGo eingerichtet, trägt es die Kalender-Adresse als Vorschlag ein.
4. Beim ersten Aufruf den Einrichtungscode aus dem Log eingeben (Apps → Log).

Updates: nach `main` mergen (oder die Action von Hand starten), dann im Panel *Auf neueste Version aktualisieren*.

**Passwort vergessen:** im Panel unter Apps → *Passwort zurücksetzen*. Oder als root:

```bash
/var/www/vhosts/<domain>/nexoraplan/nexoraplan reset-password -env /etc/nexora/apps/nexoraplan-<domain>.env <email>
```

## API

Alle Endpunkte außer `health`, `setup`, `login` und `logout` brauchen eine Sitzung (Cookie
`nexora_plan_session`). Änderungen nur mit `Content-Type: application/json`. Fehler kommen als
`{"fehler": "..."}`. Tage stehen als `2026-11-11`, Zeitpunkte als RFC 3339 in der Zeitzone der App.

| Methode | Pfad | Zweck |
|---|---|---|
| GET | `/api/health` | Status, Version, `setup_needed` |
| GET/POST | `/api/setup` | Einrichtung nötig? / Konto anlegen `{email, password, setup_code}` |
| POST | `/api/login`, `/api/logout` | An- und Abmelden |
| GET | `/api/me` | angemeldetes Konto |
| POST | `/api/password` | Passwort ändern, andere Sitzungen enden |
| GET | `/api/week?date=` | Woche mit Terminen, Aufgaben, Buchungen und Budget |
| POST | `/api/tasks` | Aufgabe `{week \| day, title}` |
| PATCH/DELETE | `/api/tasks/{id}` | ändern `{title, done, day, clear_day}` / löschen |
| POST | `/api/tasks/carry-over` | offene Aufgaben `{week}` in die nächste Woche |
| GET | `/api/events?from=&to=` | Termine im Zeitraum; mit `?key=&calendar=` ein Termin (Serie: Hauptteil) |
| POST/PUT | `/api/events` | Termin anlegen/ändern `{calendar, key, title, start, end, all_day, rrule, …}` |
| POST | `/api/events/delete` | `{key, calendar, occurrence?}` – mit `occurrence` nur dieses Vorkommen |
| GET | `/api/calendars` | Kalender mit Sichtbarkeit, Standardkalender |
| PUT | `/api/calendars/visibility`, `/api/calendars/default` | Kalender ein-/ausblenden, Standard setzen |
| GET/PUT/DELETE | `/api/caldav` | Kalenderserver anzeigen / verbinden (prüft zuerst) / trennen |
| GET | `/api/budget?month=2026-11` | Monatsübersicht mit Plan (Gesamtbudget, Abos & Verträge anteilig, frei, übrig) |
| PUT | `/api/budget/total` | Gesamtbudget pro Monat `{total_cents}`, 0 entfernt es (nur ohne Pockets) |
| GET/POST, PUT/DELETE | `/api/pockets`, `/api/pockets/{id}` | Pockets `{name, color, monthly_cents, deposit_day, income_id, balance_cents, balance_on}`; Kategorien und Abos haben `pocket_id` |
| GET | `/api/pockets/{id}/forecast` | Vorschau 12 Monate mit allen Posten und erstem Engpass |
| GET/PUT | `/api/notifications` | Telegram-Stand und Einstellungen `{digest, payments, payment_days, alerts, time}` |
| PUT/DELETE | `/api/notifications/telegram` | Bot-Token prüfen und speichern / Verbindung trennen |
| POST | `/api/notifications/telegram/link` | Chat verknüpfen (nach „/start“ an den Bot) |
| POST | `/api/notifications/send` | `{kind: digest\|payments\|alerts}` jetzt senden (zum Ausprobieren) |
| GET/POST, PUT/DELETE | `/api/categories`, `/api/categories/{id}` | Kategorien |
| POST, PUT/DELETE | `/api/transactions`, `/api/transactions/{id}` | Buchungen |
| GET/POST, PUT/DELETE | `/api/recurring`, `/api/recurring/{id}` | Abos, Verträge, feste Einnahmen `{…, interval_months: 1\|3\|6\|12, payment_method, payment_note}` |
| PUT/DELETE, GET | `/api/recurring/{id}/logo` | Logo hochladen (Body = Bild, `Content-Type: image/…`) / entfernen / abrufen (`?v=<logo_version>`) |
