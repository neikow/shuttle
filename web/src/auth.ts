const KEY = "shuttle.token";

let token: string | null = localStorage.getItem(KEY);

export function getToken(): string | null {
  return token;
}

export function setToken(t: string) {
  token = t;
  localStorage.setItem(KEY, t);
}

export function clearToken() {
  token = null;
  localStorage.removeItem(KEY);
}

export function authHeaders(): HeadersInit {
  return token ? { Authorization: `Bearer ${token}` } : {};
}
