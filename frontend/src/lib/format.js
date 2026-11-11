// Formatierung für Geld, Datum und Uhrzeit (deutsch). Tage sind überall
// Zeichenketten „2026-11-11“ – so verschiebt keine Zeitzone einen Tag.

const euro = new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' });

export function money(cents) {
  return euro.format((cents || 0) / 100);
}

// parseMoney: „12,50“, „12.50“, „1.234,56“, „1234“ → Cent; ungültig → null.
export function parseMoney(input) {
  let s = String(input ?? '').trim().replace(/\s|€/g, '');
  if (!s) return null;
  if (s.includes(',')) s = s.replace(/\./g, '').replace(',', '.');
  else if (/^\d{1,3}(\.\d{3})+$/.test(s)) s = s.replace(/\./g, '');
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return null;
  const cents = Math.round(parseFloat(s) * 100);
  return cents > 0 ? cents : null;
}

// Betrag fürs Eingabefeld: 1250 → „12,50“
export function moneyInput(cents) {
  return cents ? (cents / 100).toFixed(2).replace('.', ',') : '';
}

export function parseDay(day) {
  const [y, m, d] = day.split('-').map(Number);
  return new Date(y, m - 1, d);
}

export function dayString(date) {
  const p = (n) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${p(date.getMonth() + 1)}-${p(date.getDate())}`;
}

export function addDays(day, n) {
  const d = parseDay(day);
  d.setDate(d.getDate() + n);
  return dayString(d);
}

const WEEKDAYS = ['So', 'Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa'];
const WEEKDAYS_LONG = ['Sonntag', 'Montag', 'Dienstag', 'Mittwoch', 'Donnerstag', 'Freitag', 'Samstag'];
export const MONTHS = ['Januar', 'Februar', 'März', 'April', 'Mai', 'Juni', 'Juli', 'August', 'September', 'Oktober', 'November', 'Dezember'];

export function weekdayShort(day) {
  return WEEKDAYS[parseDay(day).getDay()];
}

export function weekdayLong(day) {
  return WEEKDAYS_LONG[parseDay(day).getDay()];
}

// „11.11.“ bzw. mit Jahr „11.11.2026“
export function shortDate(day, withYear = false) {
  const [y, m, d] = day.split('-');
  return `${d}.${m}.${withYear ? y : ''}`;
}

export function monthTitle(month) {
  const [y, m] = month.split('-').map(Number);
  return `${MONTHS[m - 1]} ${y}`;
}

export function addMonths(month, n) {
  const [y, m] = month.split('-').map(Number);
  const d = new Date(y, m - 1 + n, 1);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
}

// Zeitstempel vom Server („2026-11-11T09:00:00+01:00“) → Wanduhrzeit der App.
// Der Server liefert schon in seiner Zeitzone; Datum und Uhrzeit stehen im Text.
export function wallDay(ts) {
  return ts.slice(0, 10);
}

export function wallTime(ts) {
  return ts.slice(11, 16);
}

// Bei ganztägigen Terminen ist das Ende ausschließlich (Folgetag 0:00).
export function lastDay(event) {
  return event.all_day ? addDays(wallDay(event.end), -1) : wallDay(event.end);
}

// Zeile für einen Termin an einem bestimmten Tag: „9:00–9:30“, „ganztägig“,
// mehrtägig „ab 18:00“ / „bis 10:00“.
export function eventTime(event, day) {
  if (event.all_day) return 'ganztägig';
  const sd = wallDay(event.start);
  const ed = wallDay(event.end);
  const st = wallTime(event.start);
  const et = wallTime(event.end);
  if (sd === day && ed === day) return st === et ? st : `${st}–${et}`;
  if (sd === day) return `ab ${st}`;
  if (ed === day && et !== '00:00') return `bis ${et}`;
  return 'ganztägig';
}

// Abstände von Abos und Verträgen (Monate)
export const INTERVALS = [
  { months: 1, label: 'Monatlich' },
  { months: 3, label: 'Vierteljährlich' },
  { months: 6, label: 'Halbjährlich' },
  { months: 12, label: 'Jährlich' },
];

// intervalText: „monatlich am 5.“, „jährlich am 15.03.“, „vierteljährlich am 1. (Jan, Apr, Jul, Okt)“
export function intervalText(r) {
  const months = r.interval_months || 1;
  const label = INTERVALS.find((i) => i.months === months)?.label.toLowerCase() ?? `alle ${months} Monate`;
  const startMonth = Number(r.starts_on.slice(5, 7));
  if (months === 12) return `${label} am ${String(r.day).padStart(2, '0')}.${String(startMonth).padStart(2, '0')}.`;
  if (months === 1) return `${label} am ${r.day}.`;
  const short = ['Jan', 'Feb', 'Mär', 'Apr', 'Mai', 'Jun', 'Jul', 'Aug', 'Sep', 'Okt', 'Nov', 'Dez'];
  const list = [];
  for (let m = startMonth; list.length < 12 / months; m += months) list.push(short[(m - 1) % 12]);
  return `${label} am ${r.day}. (${list.join(', ')})`;
}
