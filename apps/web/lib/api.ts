// The console holds no credential. The session is an HttpOnly cookie the API
// set; this module only echoes back the companion CSRF value.
export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export function csrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)onboard_csrf=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : "";
}

type Options = {
  method?: "GET" | "POST" | "PATCH" | "DELETE";
  body?: unknown;
  idempotencyKey?: string;
};

export async function apiFetch<T = unknown>(path: string, options: Options = {}): Promise<T> {
  const method = options.method ?? "GET";
  const headers: Record<string, string> = { Accept: "application/json" };

  if (method !== "GET") {
    headers["Content-Type"] = "application/json";
    headers["X-CSRF-Token"] = csrfToken();
    if (options.idempotencyKey) headers["Idempotency-Key"] = options.idempotencyKey;
  }

  const response = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });

  if (response.status === 401) {
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination -- This API endpoint starts an OAuth redirect chain and requires a full document navigation.
    location.assign("/api/auth/login");
    throw new ApiError("session expired", 401);
  }

  if (response.status === 204) return null as T;

  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    const detail =
      (payload as { detail?: string; title?: string } | null)?.detail ??
      (payload as { title?: string } | null)?.title ??
      `request failed with ${response.status}`;
    throw new ApiError(detail, response.status);
  }
  return payload as T;
}

export function newIdempotencyKey(): string {
  return crypto.randomUUID();
}
