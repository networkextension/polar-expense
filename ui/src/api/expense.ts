// Typed wrappers for /api/expenses + /api/expense-categories.

import { request, requestJson } from "./http.js";

export interface ExpenseCategory {
  id: number;
  workspace_id: string;
  name: string;
  icon: string;
  color: string;
  sort_order: number;
  is_hidden: boolean;
  created_at: string;
}

export interface Expense {
  id: number;
  workspace_id: string;
  created_by_user_id: string;
  amount: number;
  currency: string;
  merchant: string;
  category_id?: number | null;
  consume_time: string;
  has_detail_time: boolean;
  region: string;
  raw_image_id?: string | null;
  status: number;
  confidence: number;
  remark: string;
  created_at: string;
  updated_at: string;
}

// ---- categories ----

export async function fetchExpenseCategories() {
  return requestJson<{ categories: ExpenseCategory[] }>("/api/expense-categories");
}

export async function createExpenseCategory(body: {
  name: string;
  icon?: string;
  color?: string;
  sort_order?: number;
}) {
  return requestJson<{ category: ExpenseCategory }>("/api/expense-categories", {
    method: "POST",
    body,
  });
}

export async function updateExpenseCategory(
  id: number,
  body: Partial<{ name: string; icon: string; color: string; sort_order: number; is_hidden: boolean }>,
) {
  return requestJson<{ ok: boolean }>(`/api/expense-categories/${id}`, {
    method: "PUT",
    body,
  });
}

export async function deleteExpenseCategory(id: number) {
  return request(`/api/expense-categories/${id}`, { method: "DELETE" });
}

// ---- expenses ----

export interface ExpenseListFilter {
  start?: string;
  end?: string;
  category_id?: number;
  status?: number;
  limit?: number;
  offset?: number;
}

export async function fetchExpenses(filter: ExpenseListFilter = {}) {
  const q = new URLSearchParams();
  if (filter.start) q.set("start", filter.start);
  if (filter.end) q.set("end", filter.end);
  if (filter.category_id != null) q.set("category_id", String(filter.category_id));
  if (filter.status != null) q.set("status", String(filter.status));
  if (filter.limit != null) q.set("limit", String(filter.limit));
  if (filter.offset != null) q.set("offset", String(filter.offset));
  const s = q.toString();
  return requestJson<{ expenses: Expense[]; total: number }>(
    `/api/expenses${s ? "?" + s : ""}`,
  );
}

export async function createExpense(body: {
  amount: number;
  currency?: string;
  merchant?: string;
  category_id?: number | null;
  consume_time?: string;
  has_detail_time?: boolean;
  region?: string;
  remark?: string;
}) {
  return requestJson<{ expense: Expense }>("/api/expenses", {
    method: "POST",
    body,
  });
}

export async function updateExpense(
  id: number,
  body: Partial<{
    amount: number;
    currency: string;
    merchant: string;
    category_id: number | null;
    clear_category: boolean;
    consume_time: string;
    has_detail_time: boolean;
    region: string;
    status: number;
    remark: string;
  }>,
) {
  return requestJson<{ ok: boolean }>(`/api/expenses/${id}`, {
    method: "PUT",
    body,
  });
}

export async function deleteExpense(id: number) {
  return request(`/api/expenses/${id}`, { method: "DELETE" });
}

// L2 — upload receipt → Apple Vision OCR → LLM structuring →
// status=0 draft Expense. extract_error is non-empty when OCR or LLM
// fell over; the draft is still created so the user can fill it in.
export interface ExpenseFromImageResponse {
  expense: Expense;
  extract_error?: string;
  // "quota_exhausted" when the team's marketplace quota / prepaid
  // balance for the multimodal LLM is drained — UI shows a deep link
  // to /llm-billing.html instead of the generic alert.
  extract_error_kind?: "quota_exhausted" | string;
}

export async function uploadExpenseImage(file: File): Promise<{ data: ExpenseFromImageResponse }> {
  const fd = new FormData();
  fd.append("file", file);
  const result = await requestJson<ExpenseFromImageResponse | { error?: string }>(
    "/api/expenses/from-image",
    { method: "POST", body: fd },
  );
  // requestJson doesn't throw on non-2xx — body is parsed as-is. For
  // upload, surface auth/validation errors as a real Error so callers
  // don't blow up with "Cannot read properties of undefined (amount)".
  if (!result.response.ok) {
    const errBody = result.data as { error?: string };
    const reason = errBody?.error || result.response.statusText || String(result.response.status);
    if (result.response.status === 401) {
      throw new Error("登录已过期，请刷新页面重新登录");
    }
    throw new Error(reason);
  }
  return { data: result.data as ExpenseFromImageResponse };
}

export function expenseImageURL(id: number): string {
  return `/api/expenses/${id}/image`;
}
