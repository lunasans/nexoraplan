// Serienregeln (RRULE) für das Formular: lesen, was die Oberfläche darstellen
// kann, und wieder schreiben. Alles andere bleibt als „eigene Regel“ unverändert.

export const DAY_CODES = ['MO', 'TU', 'WE', 'TH', 'FR', 'SA', 'SU'];
export const DAY_LABELS = ['Mo', 'Di', 'Mi', 'Do', 'Fr', 'Sa', 'So'];
const JS_DAY = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA'];

export function emptyRule() {
  return { freq: '', interval: 1, byday: [], monthly: 'day', end: 'never', until: '', count: 10, custom: '' };
}

// dayCode: Wochentag eines Tages „2026-11-11“ → „WE“
export function dayCode(day) {
  const [y, m, d] = day.split('-').map(Number);
  return JS_DAY[new Date(y, m - 1, d).getDay()];
}

// nthWeekday: „2MO“ bzw. „-1FR“ für den Tag im Monat
export function nthWeekday(day) {
  const [y, m, d] = day.split('-').map(Number);
  const last = new Date(y, m, 0).getDate();
  const n = d + 7 > last ? -1 : Math.ceil(d / 7);
  return `${n}${dayCode(day)}`;
}

export function parseRule(rrule) {
  const r = emptyRule();
  if (!rrule) return r;
  const parts = Object.fromEntries(rrule.split(';').filter(Boolean).map((p) => p.split('=')));
  const known = ['FREQ', 'INTERVAL', 'BYDAY', 'COUNT', 'UNTIL', 'WKST'];
  const simple = Object.keys(parts).every((k) => known.includes(k));
  r.freq = parts.FREQ || '';
  r.interval = Number(parts.INTERVAL || 1);
  if (parts.COUNT) {
    r.end = 'count';
    r.count = Number(parts.COUNT);
  } else if (parts.UNTIL) {
    r.end = 'until';
    const u = parts.UNTIL;
    r.until = `${u.slice(0, 4)}-${u.slice(4, 6)}-${u.slice(6, 8)}`;
  }
  if (parts.BYDAY) {
    const days = parts.BYDAY.split(',');
    if (r.freq === 'WEEKLY' && days.every((d) => DAY_CODES.includes(d))) r.byday = days;
    else if (r.freq === 'MONTHLY' && days.length === 1 && /^-?[1-5][A-Z]{2}$/.test(days[0])) r.monthly = 'weekday';
    else r.custom = rrule;
  }
  if (!simple || !['DAILY', 'WEEKLY', 'MONTHLY', 'YEARLY'].includes(r.freq)) r.custom = rrule;
  return r;
}

// buildRule: Formular → RRULE; startDay bestimmt Wochentag bzw. „2. Montag“.
export function buildRule(r, startDay) {
  if (r.custom) return r.custom;
  if (!r.freq) return '';
  const parts = [`FREQ=${r.freq}`];
  if (r.interval > 1) parts.push(`INTERVAL=${r.interval}`);
  if (r.freq === 'WEEKLY' && r.byday.length) {
    parts.push(`BYDAY=${DAY_CODES.filter((d) => r.byday.includes(d)).join(',')}`);
  }
  if (r.freq === 'MONTHLY' && r.monthly === 'weekday') parts.push(`BYDAY=${nthWeekday(startDay)}`);
  if (r.end === 'count' && r.count > 0) parts.push(`COUNT=${r.count}`);
  if (r.end === 'until' && r.until) parts.push(`UNTIL=${r.until.replaceAll('-', '')}`);
  return parts.join(';');
}

const ORD = { 1: 'ersten', 2: 'zweiten', 3: 'dritten', 4: 'vierten', 5: 'fünften', '-1': 'letzten' };
const LONG = { MO: 'Montag', TU: 'Dienstag', WE: 'Mittwoch', TH: 'Donnerstag', FR: 'Freitag', SA: 'Samstag', SU: 'Sonntag' };

export function nthLabel(day) {
  const code = nthWeekday(day);
  return `am ${ORD[code.slice(0, -2)]} ${LONG[code.slice(-2)]}`;
}

// describe: kurze deutsche Beschreibung für die Anzeige („jeden Montag“)
export function describe(rrule) {
  if (!rrule) return '';
  const r = parseRule(rrule);
  if (r.custom) return 'Serie';
  const every = r.interval > 1 ? `alle ${r.interval} ` : '';
  let text = {
    DAILY: r.interval > 1 ? `${every}Tage` : 'täglich',
    WEEKLY: r.interval > 1 ? `${every}Wochen` : 'wöchentlich',
    MONTHLY: r.interval > 1 ? `${every}Monate` : 'monatlich',
    YEARLY: r.interval > 1 ? `${every}Jahre` : 'jährlich',
  }[r.freq];
  if (r.byday.length) text += ` (${r.byday.map((d) => DAY_LABELS[DAY_CODES.indexOf(d)]).join(', ')})`;
  if (r.end === 'count') text += `, ${r.count}-mal`;
  if (r.end === 'until') text += ` bis ${r.until.split('-').reverse().join('.')}`;
  return text;
}
