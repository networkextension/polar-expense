// /expense.html — 家庭账本 L1 single-page UI.
//
// Two tabs: 流水 (CRUD over the workspace's expenses) + 分类 (manage
// the per-workspace category dictionary). Both seeded on first load:
// listExpenseCategories backend bootstraps 7 presets if empty.
//
// OCR/LLM extract → L2 (separate PR).

import {
  createExpense,
  createExpenseCategory,
  deleteExpense,
  deleteExpenseCategory,
  fetchExpenseCategories,
  fetchExpenses,
  updateExpense,
  updateExpenseCategory,
  uploadExpenseImage,
  type Expense,
  type ExpenseCategory,
  type ExpenseListFilter,
} from "./api/expense.js";
import { logout } from "./api/session.js";
import { byId } from "./lib/dom.js";
import { hydrateSidebarFoot, hydrateSiteBrand } from "./lib/site.js";
import { bindThemeSync, initStoredTheme } from "./lib/theme.js";

initStoredTheme();
bindThemeSync();

// ---- DOM refs ----
const tabExpensesBtn = byId<HTMLButtonElement>("tabExpenses");
const tabDraftsBtn = byId<HTMLButtonElement>("tabDrafts");
const tabCategoriesBtn = byId<HTMLButtonElement>("tabCategories");
const paneExpenses = byId<HTMLElement>("paneExpenses");
const paneDrafts = byId<HTMLElement>("paneDrafts");
const paneCategories = byId<HTMLElement>("paneCategories");
const draftBadge = byId<HTMLElement>("draftBadge");
const draftCount = byId<HTMLElement>("draftCount");
const draftTable = byId<HTMLTableElement>("draftTable");
const draftTbody = byId<HTMLTableSectionElement>("draftTbody");
const draftEmpty = byId<HTMLElement>("draftEmpty");
const draftRefreshBtn = byId<HTMLButtonElement>("draftRefreshBtn");
const uploadReceiptBtn = byId<HTMLButtonElement>("uploadReceiptBtn");
const uploadReceiptInput = byId<HTMLInputElement>("uploadReceiptInput");

const expenseTable = byId<HTMLTableElement>("expenseTable");
const expenseTbody = byId<HTMLTableSectionElement>("expenseTbody");
const expenseEmpty = byId<HTMLElement>("expenseEmpty");
const expenseLoading = byId<HTMLElement>("expenseLoading");
const expenseCount = byId<HTMLElement>("expenseCount");
const expenseTotal = byId<HTMLElement>("expenseTotal");
const filterPeriod = byId<HTMLSelectElement>("filterPeriod");
const filterCategory = byId<HTMLSelectElement>("filterCategory");
const expenseRefreshBtn = byId<HTMLButtonElement>("expenseRefreshBtn");
const newExpenseBtn = byId<HTMLButtonElement>("newExpenseBtn");

const expenseModal = byId<HTMLElement>("expenseModal");
const expenseModalTitle = byId<HTMLElement>("expenseModalTitle");
const expenseModalCloseBtn = byId<HTMLButtonElement>("expenseModalCloseBtn");
const expenseModalCancelBtn = byId<HTMLButtonElement>("expenseModalCancelBtn");
const expenseModalSubmitBtn = byId<HTMLButtonElement>("expenseModalSubmitBtn");
const expenseAmount = byId<HTMLInputElement>("expenseAmount");
const expenseMerchant = byId<HTMLInputElement>("expenseMerchant");
const expenseCategorySel = byId<HTMLSelectElement>("expenseCategory");
const expenseConsumeTime = byId<HTMLInputElement>("expenseConsumeTime");
const expenseDateOnly = byId<HTMLInputElement>("expenseDateOnly");
const expenseRegion = byId<HTMLInputElement>("expenseRegion");
const expenseRemark = byId<HTMLTextAreaElement>("expenseRemark");
const expenseFormError = byId<HTMLElement>("expenseFormError");

const categoryTable = byId<HTMLTableElement>("categoryTable");
const categoryTbody = byId<HTMLTableSectionElement>("categoryTbody");
const categoryEmpty = byId<HTMLElement>("categoryEmpty");
const categoryCount = byId<HTMLElement>("categoryCount");
const categoryRefreshBtn = byId<HTMLButtonElement>("categoryRefreshBtn");
const newCategoryBtn = byId<HTMLButtonElement>("newCategoryBtn");

const categoryModal = byId<HTMLElement>("categoryModal");
const categoryModalTitle = byId<HTMLElement>("categoryModalTitle");
const categoryModalCloseBtn = byId<HTMLButtonElement>("categoryModalCloseBtn");
const categoryModalCancelBtn = byId<HTMLButtonElement>("categoryModalCancelBtn");
const categoryModalSubmitBtn = byId<HTMLButtonElement>("categoryModalSubmitBtn");
const categoryNameInput = byId<HTMLInputElement>("categoryName");
const categoryIconInput = byId<HTMLInputElement>("categoryIcon");
const categoryColorInput = byId<HTMLInputElement>("categoryColor");
const categorySortOrderInput = byId<HTMLInputElement>("categorySortOrder");
const categoryFormError = byId<HTMLElement>("categoryFormError");

const logoutBtn = byId<HTMLButtonElement>("logoutBtn");

// ---- state ----
let categories: ExpenseCategory[] = [];
let editingExpenseID: number | null = null;
let editingCategoryID: number | null = null;

// ---- format helpers ----
function fmtAmount(amount: number, currency: string): string {
  const symbol = currency === "CNY" ? "¥" : currency === "USD" ? "$" : currency === "JPY" ? "¥" : "";
  return `${symbol}${amount.toFixed(2)}`;
}

function fmtConsumeTime(iso: string, hasDetail: boolean): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const y = d.getFullYear();
  const mo = String(d.getMonth() + 1).padStart(2, "0");
  const da = String(d.getDate()).padStart(2, "0");
  if (!hasDetail) return `${y}-${mo}-${da}`;
  const h = String(d.getHours()).padStart(2, "0");
  const mi = String(d.getMinutes()).padStart(2, "0");
  return `${y}-${mo}-${da} ${h}:${mi}`;
}

function escapeHTML(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// ---- filter -> date range ----
function currentPeriodRange(): { start?: string; end?: string } {
  const v = filterPeriod.value;
  const now = new Date();
  const fmt = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  if (v === "month") {
    const start = new Date(now.getFullYear(), now.getMonth(), 1);
    const end = new Date(now.getFullYear(), now.getMonth() + 1, 1);
    return { start: fmt(start), end: fmt(end) };
  }
  if (v === "last_month") {
    const start = new Date(now.getFullYear(), now.getMonth() - 1, 1);
    const end = new Date(now.getFullYear(), now.getMonth(), 1);
    return { start: fmt(start), end: fmt(end) };
  }
  if (v === "30d") {
    const start = new Date();
    start.setDate(start.getDate() - 30);
    return { start: fmt(start) };
  }
  return {};
}

// ---- tab switching ----
function switchTab(tab: "expenses" | "drafts" | "categories"): void {
  paneExpenses.hidden = tab !== "expenses";
  paneDrafts.hidden = tab !== "drafts";
  paneCategories.hidden = tab !== "categories";
  tabExpensesBtn.classList.toggle("lp-tab-active", tab === "expenses");
  tabDraftsBtn.classList.toggle("lp-tab-active", tab === "drafts");
  tabCategoriesBtn.classList.toggle("lp-tab-active", tab === "categories");
  if (tab === "expenses") void loadExpenses();
  else if (tab === "drafts") void loadDrafts();
  else if (tab === "categories") renderCategoryTable();
}

tabExpensesBtn.addEventListener("click", () => switchTab("expenses"));
tabDraftsBtn.addEventListener("click", () => switchTab("drafts"));
tabCategoriesBtn.addEventListener("click", () => switchTab("categories"));

// ---- expenses ----

async function loadExpenses(): Promise<void> {
  expenseLoading.hidden = false;
  expenseTable.hidden = true;
  expenseEmpty.hidden = true;
  const range = currentPeriodRange();
  // 流水 tab shows confirmed only; drafts have their own tab so the
  // main list isn't polluted by half-extracted rows.
  const filter: ExpenseListFilter = { ...range, status: 1, limit: 200 };
  const catVal = filterCategory.value;
  if (catVal) filter.category_id = parseInt(catVal, 10);
  try {
    const { data } = await fetchExpenses(filter);
    const items = data.expenses ?? [];
    expenseTbody.innerHTML = "";
    items.forEach((e) => expenseTbody.appendChild(renderExpenseRow(e)));
    const totalAmt = items.reduce((s, e) => s + e.amount, 0);
    expenseCount.textContent = `${data.total} 条`;
    expenseTotal.textContent = `合计 ¥${totalAmt.toFixed(2)}`;
    expenseTable.hidden = items.length === 0;
    expenseEmpty.hidden = items.length !== 0;
  } catch (err) {
    expenseEmpty.hidden = false;
    expenseEmpty.textContent = `加载失败：${(err as Error).message}`;
  } finally {
    expenseLoading.hidden = true;
  }
}

function renderExpenseRow(e: Expense): HTMLTableRowElement {
  const tr = document.createElement("tr");
  if (e.status === 0) tr.style.background = "rgba(245, 158, 11, 0.06)"; // 待确认 amber tint

  const tdTime = document.createElement("td");
  tdTime.textContent = fmtConsumeTime(e.consume_time, e.has_detail_time);
  tdTime.style.whiteSpace = "nowrap";

  const tdMerchant = document.createElement("td");
  tdMerchant.textContent = e.merchant || "—";

  const tdCat = document.createElement("td");
  const cat = e.category_id != null ? categories.find((c) => c.id === e.category_id) : null;
  if (cat) {
    tdCat.innerHTML = `<span style="display:inline-flex; align-items:center; gap:4px;"><span>${escapeHTML(cat.icon)}</span><span>${escapeHTML(cat.name)}</span></span>`;
  } else {
    tdCat.innerHTML = '<span class="meta-subtitle">未分类</span>';
  }

  const tdAmt = document.createElement("td");
  tdAmt.style.textAlign = "right";
  tdAmt.style.fontWeight = "600";
  tdAmt.textContent = fmtAmount(e.amount, e.currency);

  const tdRegion = document.createElement("td");
  tdRegion.textContent = e.region || "—";

  const tdRemark = document.createElement("td");
  tdRemark.textContent = e.remark || "";
  tdRemark.style.maxWidth = "200px";
  tdRemark.style.overflow = "hidden";
  tdRemark.style.textOverflow = "ellipsis";
  tdRemark.style.whiteSpace = "nowrap";
  if (e.remark) tdRemark.title = e.remark;

  const tdAct = document.createElement("td");
  const editBtn = document.createElement("button");
  editBtn.type = "button";
  editBtn.className = "btn-inline btn-secondary";
  editBtn.textContent = "✎";
  editBtn.title = "编辑";
  editBtn.style.marginRight = "4px";
  editBtn.addEventListener("click", () => openExpenseModal(e));
  const delBtn = document.createElement("button");
  delBtn.type = "button";
  delBtn.className = "btn-inline btn-secondary";
  delBtn.textContent = "🗑";
  delBtn.title = "删除";
  delBtn.addEventListener("click", () => onDeleteExpense(e));
  tdAct.append(editBtn, delBtn);

  tr.append(tdTime, tdMerchant, tdCat, tdAmt, tdRegion, tdRemark, tdAct);
  return tr;
}

async function onDeleteExpense(e: Expense): Promise<void> {
  if (!confirm(`确认删除 ${fmtAmount(e.amount, e.currency)} · ${e.merchant || "—"}?`)) return;
  try {
    await deleteExpense(e.id);
    void loadExpenses();
  } catch (err) {
    alert(`删除失败：${(err as Error).message}`);
  }
}

function openExpenseModal(e: Expense | null): void {
  editingExpenseID = e ? e.id : null;
  expenseModalTitle.textContent = e ? "编辑流水" : "手工记账";
  expenseFormError.textContent = "";
  expenseAmount.value = e ? String(e.amount) : "";
  expenseMerchant.value = e?.merchant ?? "";
  expenseCategorySel.value = e?.category_id != null ? String(e.category_id) : "";
  expenseRegion.value = e?.region ?? "";
  expenseRemark.value = e?.remark ?? "";
  // Default new entry to "now"; preserve existing time on edit.
  const t = e ? new Date(e.consume_time) : new Date();
  expenseDateOnly.checked = e ? !e.has_detail_time : false;
  expenseConsumeTime.value = toDatetimeLocalString(t);
  expenseModal.hidden = false;
}

function toDatetimeLocalString(d: Date): string {
  // input[type=datetime-local] wants "YYYY-MM-DDTHH:mm" in LOCAL time
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

async function submitExpense(): Promise<void> {
  expenseFormError.textContent = "";
  const amount = parseFloat(expenseAmount.value);
  if (!Number.isFinite(amount) || amount <= 0) {
    expenseFormError.textContent = "金额必填，且 > 0";
    return;
  }
  if (!expenseConsumeTime.value) {
    expenseFormError.textContent = "请选消费时间";
    return;
  }
  const consumeISO = new Date(expenseConsumeTime.value).toISOString();
  const catStr = expenseCategorySel.value;
  const categoryID = catStr ? parseInt(catStr, 10) : null;

  try {
    if (editingExpenseID == null) {
      await createExpense({
        amount,
        merchant: expenseMerchant.value.trim(),
        category_id: categoryID,
        consume_time: consumeISO,
        has_detail_time: !expenseDateOnly.checked,
        region: expenseRegion.value.trim(),
        remark: expenseRemark.value.trim(),
      });
    } else {
      await updateExpense(editingExpenseID, {
        amount,
        merchant: expenseMerchant.value.trim(),
        category_id: categoryID ?? undefined,
        clear_category: categoryID == null,
        consume_time: consumeISO,
        has_detail_time: !expenseDateOnly.checked,
        region: expenseRegion.value.trim(),
        remark: expenseRemark.value.trim(),
      });
    }
    expenseModal.hidden = true;
    void loadExpenses();
  } catch (err) {
    expenseFormError.textContent = `保存失败：${(err as Error).message}`;
  }
}

expenseRefreshBtn.addEventListener("click", () => void loadExpenses());
newExpenseBtn.addEventListener("click", () => openExpenseModal(null));
filterPeriod.addEventListener("change", () => void loadExpenses());
filterCategory.addEventListener("change", () => void loadExpenses());
expenseModalCloseBtn.addEventListener("click", () => (expenseModal.hidden = true));
expenseModalCancelBtn.addEventListener("click", () => (expenseModal.hidden = true));
expenseModalSubmitBtn.addEventListener("click", () => void submitExpense());

// ---- categories ----

async function loadCategories(): Promise<void> {
  const { data } = await fetchExpenseCategories();
  categories = data.categories ?? [];
  // Sync the filter dropdown + the form dropdown
  filterCategory.innerHTML = '<option value="">全部</option>';
  expenseCategorySel.innerHTML = '<option value="">未分类</option>';
  for (const c of categories) {
    if (!c.is_hidden) {
      const opt = document.createElement("option");
      opt.value = String(c.id);
      opt.textContent = `${c.icon} ${c.name}`;
      filterCategory.appendChild(opt);
      expenseCategorySel.appendChild(opt.cloneNode(true));
    }
  }
}

function renderCategoryTable(): void {
  categoryCount.textContent = `${categories.length} 个`;
  categoryTbody.innerHTML = "";
  if (categories.length === 0) {
    categoryTable.hidden = true;
    categoryEmpty.hidden = false;
    return;
  }
  categoryTable.hidden = false;
  categoryEmpty.hidden = true;
  for (const c of categories) {
    const tr = document.createElement("tr");
    if (c.is_hidden) tr.style.opacity = "0.5";

    const tdIcon = document.createElement("td");
    tdIcon.style.fontSize = "20px";
    tdIcon.textContent = c.icon || "•";

    const tdName = document.createElement("td");
    tdName.textContent = c.name;

    const tdColor = document.createElement("td");
    tdColor.innerHTML = `<span style="display:inline-block; width:24px; height:16px; background:${c.color}; border-radius:3px;"></span>`;

    const tdSort = document.createElement("td");
    tdSort.textContent = String(c.sort_order);

    const tdHidden = document.createElement("td");
    tdHidden.textContent = c.is_hidden ? "✓" : "";

    const tdAct = document.createElement("td");
    const editBtn = document.createElement("button");
    editBtn.type = "button";
    editBtn.className = "btn-inline btn-secondary";
    editBtn.textContent = "✎";
    editBtn.title = "编辑";
    editBtn.style.marginRight = "4px";
    editBtn.addEventListener("click", () => openCategoryModal(c));
    const toggleBtn = document.createElement("button");
    toggleBtn.type = "button";
    toggleBtn.className = "btn-inline btn-secondary";
    toggleBtn.textContent = c.is_hidden ? "显示" : "隐藏";
    toggleBtn.style.marginRight = "4px";
    toggleBtn.addEventListener("click", async () => {
      try {
        await updateExpenseCategory(c.id, { is_hidden: !c.is_hidden });
        await loadCategories();
        renderCategoryTable();
      } catch (err) {
        alert(`操作失败：${(err as Error).message}`);
      }
    });
    const delBtn = document.createElement("button");
    delBtn.type = "button";
    delBtn.className = "btn-inline btn-secondary";
    delBtn.textContent = "🗑";
    delBtn.title = "删除（已关联的流水会变成未分类）";
    delBtn.addEventListener("click", () => onDeleteCategory(c));
    tdAct.append(editBtn, toggleBtn, delBtn);

    tr.append(tdIcon, tdName, tdColor, tdSort, tdHidden, tdAct);
    categoryTbody.appendChild(tr);
  }
}

async function onDeleteCategory(c: ExpenseCategory): Promise<void> {
  if (!confirm(`删除分类「${c.name}」?该分类下的流水会变成「未分类」。`)) return;
  try {
    await deleteExpenseCategory(c.id);
    await loadCategories();
    renderCategoryTable();
  } catch (err) {
    alert(`删除失败：${(err as Error).message}`);
  }
}

function openCategoryModal(c: ExpenseCategory | null): void {
  editingCategoryID = c ? c.id : null;
  categoryModalTitle.textContent = c ? "编辑分类" : "新建分类";
  categoryFormError.textContent = "";
  categoryNameInput.value = c?.name ?? "";
  categoryIconInput.value = c?.icon ?? "";
  categoryColorInput.value = c?.color ?? "#888888";
  categorySortOrderInput.value = String(c?.sort_order ?? 100);
  categoryModal.hidden = false;
}

async function submitCategory(): Promise<void> {
  categoryFormError.textContent = "";
  const name = categoryNameInput.value.trim();
  if (!name) {
    categoryFormError.textContent = "名称必填";
    return;
  }
  const sortOrder = parseInt(categorySortOrderInput.value, 10) || 100;
  try {
    if (editingCategoryID == null) {
      await createExpenseCategory({
        name,
        icon: categoryIconInput.value.trim(),
        color: categoryColorInput.value,
        sort_order: sortOrder,
      });
    } else {
      await updateExpenseCategory(editingCategoryID, {
        name,
        icon: categoryIconInput.value.trim(),
        color: categoryColorInput.value,
        sort_order: sortOrder,
      });
    }
    categoryModal.hidden = true;
    await loadCategories();
    renderCategoryTable();
  } catch (err) {
    categoryFormError.textContent = `保存失败：${(err as Error).message}`;
  }
}

categoryRefreshBtn.addEventListener("click", async () => {
  await loadCategories();
  renderCategoryTable();
});
newCategoryBtn.addEventListener("click", () => openCategoryModal(null));
categoryModalCloseBtn.addEventListener("click", () => (categoryModal.hidden = true));
categoryModalCancelBtn.addEventListener("click", () => (categoryModal.hidden = true));
categoryModalSubmitBtn.addEventListener("click", () => void submitCategory());

// ---- L2: upload receipt → OCR → LLM draft ----

async function onUploadReceipt(file: File): Promise<void> {
  if (!file) return;
  // Quick client-side guards; backend re-validates.
  if (file.size > 25 * 1024 * 1024) {
    alert("图片超过 25 MiB 上限");
    return;
  }
  uploadReceiptBtn.disabled = true;
  const original = uploadReceiptBtn.textContent;
  uploadReceiptBtn.textContent = "⏳ 抽取中...";
  try {
    const { data } = await uploadExpenseImage(file);
    if (data.extract_error_kind === "quota_exhausted") {
      // Targeted prompt + deep link to billing page. Confirm() doubles
      // as a "go now or later" branch; pressing OK opens the page.
      const goToBilling = confirm(
        `本月 LLM 截图额度用完了。\n\n${data.extract_error?.slice(0, 200) ?? ""}\n\n` +
          `点确定到「LLM 计费」上调本月预算或充值；点取消先把这条草稿手填。`,
      );
      if (goToBilling) window.open("/llm-billing.html", "_blank");
    } else if (data.extract_error) {
      alert(`已落草稿（抽取失败：${data.extract_error.slice(0, 80)}），请到「待确认」手填确认。`);
    } else {
      alert(`已落草稿（金额 ¥${data.expense.amount.toFixed(2)}，置信度 ${data.expense.confidence}），到「待确认」核对。`);
    }
    await refreshDraftBadge();
    switchTab("drafts");
  } catch (err) {
    alert(`上传失败：${(err as Error).message}`);
  } finally {
    uploadReceiptBtn.disabled = false;
    uploadReceiptBtn.textContent = original;
    uploadReceiptInput.value = ""; // allow re-pick of same file
  }
}

uploadReceiptBtn.addEventListener("click", () => uploadReceiptInput.click());
uploadReceiptInput.addEventListener("change", () => {
  const f = uploadReceiptInput.files?.[0];
  if (f) void onUploadReceipt(f);
});

// Drag-drop on the entire expense pane
paneExpenses.addEventListener("dragover", (ev) => {
  ev.preventDefault();
  paneExpenses.style.outline = "2px dashed var(--accent, #3b82f6)";
  paneExpenses.style.outlineOffset = "-8px";
});
paneExpenses.addEventListener("dragleave", () => {
  paneExpenses.style.outline = "";
});
paneExpenses.addEventListener("drop", (ev) => {
  ev.preventDefault();
  paneExpenses.style.outline = "";
  const f = ev.dataTransfer?.files?.[0];
  if (f && f.type.startsWith("image/")) void onUploadReceipt(f);
});

async function refreshDraftBadge(): Promise<void> {
  try {
    const { data } = await fetchExpenses({ status: 0, limit: 1 });
    const n = data.total;
    if (n > 0) {
      draftBadge.hidden = false;
      draftBadge.textContent = String(n);
    } else {
      draftBadge.hidden = true;
    }
  } catch {
    /* silent */
  }
}

async function loadDrafts(): Promise<void> {
  const { data } = await fetchExpenses({ status: 0, limit: 100 });
  const items = data.expenses ?? [];
  draftCount.textContent = `${items.length} 条`;
  draftTbody.innerHTML = "";
  for (const e of items) draftTbody.appendChild(renderDraftRow(e));
  draftTable.hidden = items.length === 0;
  draftEmpty.hidden = items.length !== 0;
}

function renderDraftRow(e: Expense): HTMLTableRowElement {
  const tr = document.createElement("tr");
  const failed = (e.remark || "").startsWith("自动抽取失败");

  const tdImg = document.createElement("td");
  if (e.raw_image_id) {
    const img = document.createElement("img");
    img.src = `/api/expenses/${e.id}/image`;
    img.style.cssText = "width:40px; height:40px; object-fit:cover; border-radius:4px; cursor:zoom-in;";
    img.title = "点击查看原图";
    img.addEventListener("click", () => window.open(`/api/expenses/${e.id}/image`, "_blank"));
    tdImg.appendChild(img);
  } else {
    tdImg.innerHTML = '<span class="meta-subtitle">—</span>';
  }

  const tdTime = document.createElement("td");
  tdTime.textContent = fmtConsumeTime(e.consume_time, e.has_detail_time);
  tdTime.style.whiteSpace = "nowrap";

  const tdMerchant = document.createElement("td");
  tdMerchant.textContent = e.merchant || "—";

  const tdCat = document.createElement("td");
  const cat = e.category_id != null ? categories.find((c) => c.id === e.category_id) : null;
  tdCat.textContent = cat ? `${cat.icon} ${cat.name}` : "未分类";

  const tdAmt = document.createElement("td");
  tdAmt.style.textAlign = "right";
  tdAmt.style.fontWeight = "600";
  tdAmt.textContent = e.amount > 0 ? fmtAmount(e.amount, e.currency) : "—";

  const tdConf = document.createElement("td");
  const conf = e.confidence;
  const confColor = conf >= 80 ? "#16a34a" : conf >= 50 ? "#d97706" : "#dc2626";
  tdConf.innerHTML = `<span style="color:${confColor}; font-weight:600;">${conf}%</span>`;

  const tdRemark = document.createElement("td");
  tdRemark.textContent = e.remark || "";
  if (failed) tdRemark.style.color = "var(--danger, #c00)";
  tdRemark.style.maxWidth = "260px";
  tdRemark.style.overflow = "hidden";
  tdRemark.style.textOverflow = "ellipsis";
  tdRemark.style.whiteSpace = "nowrap";
  if (e.remark) tdRemark.title = e.remark;

  const tdAct = document.createElement("td");
  const confirmBtn = document.createElement("button");
  confirmBtn.type = "button";
  confirmBtn.className = "btn-inline btn-primary";
  confirmBtn.textContent = "✓ 确认";
  confirmBtn.style.marginRight = "4px";
  confirmBtn.disabled = !(e.amount > 0);
  confirmBtn.title = e.amount > 0 ? "确认入正式账" : "金额为 0，先编辑";
  confirmBtn.addEventListener("click", async () => {
    try {
      await updateExpense(e.id, { status: 1 });
      await refreshDraftBadge();
      void loadDrafts();
    } catch (err) {
      alert(`确认失败：${(err as Error).message}`);
    }
  });
  const editBtn = document.createElement("button");
  editBtn.type = "button";
  editBtn.className = "btn-inline btn-secondary";
  editBtn.textContent = "✎";
  editBtn.style.marginRight = "4px";
  editBtn.title = "编辑";
  editBtn.addEventListener("click", () => openExpenseModal(e));
  const delBtn = document.createElement("button");
  delBtn.type = "button";
  delBtn.className = "btn-inline btn-secondary";
  delBtn.textContent = "🗑";
  delBtn.title = "放弃";
  delBtn.addEventListener("click", async () => {
    if (!confirm("放弃此草稿？")) return;
    try {
      await deleteExpense(e.id);
      await refreshDraftBadge();
      void loadDrafts();
    } catch (err) {
      alert(`删除失败：${(err as Error).message}`);
    }
  });
  tdAct.append(confirmBtn, editBtn, delBtn);

  tr.append(tdImg, tdTime, tdMerchant, tdCat, tdAmt, tdConf, tdRemark, tdAct);
  return tr;
}

draftRefreshBtn.addEventListener("click", () => {
  void refreshDraftBadge();
  void loadDrafts();
});

// ---- bootstrap ----

logoutBtn?.addEventListener("click", () => {
  void logout().then(() => {
    window.location.href = "/login.html";
  });
});

async function bootstrap(): Promise<void> {
  hydrateSiteBrand();
  hydrateSidebarFoot();
  await loadCategories(); // also bootstraps backend presets if empty
  await Promise.all([loadExpenses(), refreshDraftBadge()]);
}

void bootstrap();
