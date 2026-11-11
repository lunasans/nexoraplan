// Gemeinsamer Zustand: angemeldeter Nutzer, Meldungen, Kalender und Kategorien.
import { api } from './api.js';

export const session = $state({ checked: false, user: null, setupNeeded: false, sso: false });

export const toast = $state({ text: '', error: false });
let toastTimer;

export function showToast(text, error = false) {
  toast.text = text;
  toast.error = error;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.text = ''), error ? 7000 : 3500);
}

export function handleError(err) {
  if (err?.status === 401) {
    session.user = null;
    return;
  }
  showToast(err?.message || String(err), true);
}

// Kalender (eigene + SOGo) – für die Auswahl beim Anlegen von Terminen.
export const calendars = $state({ list: [], default: 'local', error: '', loaded: false });

export async function loadCalendars(fresh = false) {
  try {
    const res = await api('GET', `/api/calendars${fresh ? '?fresh=1' : ''}`);
    calendars.list = res.calendars;
    calendars.default = res.default;
    calendars.error = res.calendar_error;
    calendars.loaded = true;
  } catch (err) {
    handleError(err);
  }
}

export const categories = $state({ list: [], loaded: false });

export async function loadCategories() {
  try {
    categories.list = await api('GET', '/api/categories');
    categories.loaded = true;
  } catch (err) {
    handleError(err);
  }
}

// Dialoge: wer sie öffnet, legt hier die Daten ab; onsaved lädt danach neu.
export const dialogs = $state({ event: null, transaction: null });

export function openEvent(data, onsaved) {
  dialogs.event = { ...data, onsaved };
}

export function openTransaction(data, onsaved) {
  dialogs.transaction = { ...data, onsaved };
}

// Pockets (Unterkonten, z. B. C24) – für Auswahlfelder in Dialogen
export const pockets = $state({ list: [], loaded: false });

export async function loadPockets() {
  try {
    pockets.list = await api('GET', '/api/pockets');
    pockets.loaded = true;
  } catch (err) {
    handleError(err);
  }
}
