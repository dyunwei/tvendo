import { reactive } from "vue";
export interface User {
  currency: "CNY";
  id: number;
  email: string;
  name: string;
  role: string;
  balance: number;
}
export interface Endpoint {
  mcp_name?: string;
  input_schema?: unknown;
  disabled?: boolean;
  missing?: boolean;
  schema_changed?: boolean;
  price_override?: boolean;
  id: string;
  name: string;
  path: string;
  method: string;
  description: string;
  price_micros: number;
  parameters: unknown;
}
export interface Tool {
  upstream_type?: "http" | "mcp" | "mcp_sse";
  default_price_micros?: number;
  revision?: number;
  last_synced_at?: string | null;
  id: number;
  slug: string;
  name: string;
  category: string;
  description: string;
  website: string;
  docs: string;
  icon: string;
  base_url?: string;
  auth_header?: string;
  auth_prefix?: string;
  has_secret?: boolean;
  secret?: string;
  enabled: boolean;
  endpoints: Endpoint[];
  resources: { name: string; description: string; price: string }[];
}
export const session = reactive({
  user: null as User | null,
  csrf: "",
  ready: false,
});
export async function api<T = any>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch(path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": session.csrf,
      ...options.headers,
    },
    credentials: "same-origin",
  });
  const data = await response
    .json()
    .catch(() => ({ error: "服务返回无效，请稍后重试" }));
  if (!response.ok) {
    if (response.status === 401 && !path.includes("/auth/login"))
      session.user = null;
    throw new Error(data.error || "请求失败");
  }
  return data;
}
export async function refreshSession() {
  try {
    const d = await api("/api/auth/me");
    session.user = d.user;
    session.csrf = d.csrf;
  } catch {
    session.user = null;
  } finally {
    session.ready = true;
  }
}
export function money(micros: number, digits = 2) {
  return (Number(micros || 0) / 1e6).toLocaleString("zh-CN", {
    style: "currency",
    currency: "CNY",
    minimumFractionDigits: digits,
    maximumFractionDigits: Math.max(digits, 6),
  });
}
export function date(v: string | null) {
  return v
    ? new Date(v).toLocaleString("zh-CN", { hour12: false })
    : "从未使用";
}
