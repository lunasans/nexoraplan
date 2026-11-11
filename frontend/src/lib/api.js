// Zugriff auf die Go-API. Fehler kommen als ApiError mit HTTP-Status und der
// Meldung aus {"fehler": "..."}.

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

export async function api(method, url, body) {
  const options = { method, headers: {} };
  if (body !== undefined) {
    options.headers['Content-Type'] = 'application/json';
    options.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(url, options);
  } catch {
    throw new ApiError(0, 'Server nicht erreichbar – Verbindung prüfen');
  }
  const data = res.status === 204 ? null : await res.json().catch(() => null);
  if (!res.ok) throw new ApiError(res.status, data?.fehler || `Fehler ${res.status}`);
  return data;
}

// localStorage kann im privaten Modus fehlen – dann eben ohne.
export const storage = {
  get(key) { try { return localStorage.getItem(key); } catch { return null; } },
  set(key, value) { try { localStorage.setItem(key, value); } catch {} },
};
