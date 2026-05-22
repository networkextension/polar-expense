package expense

// Storage layer for the expense module (家庭账本 / 老婆大人专用).
// Tables are declared in store.go: expense_categories + expenses.
// L1 scope: manual CRUD only. OCR/LLM extraction → L2 (separate PR).

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ExpenseStatusDraft     = 0 // 待确认 (OCR draft awaiting user review)
	ExpenseStatusConfirmed = 1 // 已确认
)

type ExpenseCategory struct {
	ID          int64     `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Icon        string    `json:"icon"`
	Color       string    `json:"color"`
	SortOrder   int       `json:"sort_order"`
	IsHidden    bool      `json:"is_hidden"`
	CreatedAt   time.Time `json:"created_at"`
}

type Expense struct {
	ID              int64     `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	CreatedByUserID string    `json:"created_by_user_id"`
	Amount          float64   `json:"amount"`
	Currency        string    `json:"currency"`
	Merchant        string    `json:"merchant"`
	CategoryID      *int64    `json:"category_id,omitempty"`
	ConsumeTime     time.Time `json:"consume_time"`
	HasDetailTime   bool      `json:"has_detail_time"`
	Region          string    `json:"region"`
	RawImageID      *string   `json:"raw_image_id,omitempty"`
	Status          int       `json:"status"`
	Confidence      int       `json:"confidence"`
	Remark          string    `json:"remark"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ---- categories ----

// defaultExpenseCategories are seeded on first read for a workspace that
// has none. Names + icons match the design doc (生活/教育/医疗/...). We
// don't run this as a global INSERT in the schema block because the
// trigger needs to know *which* workspace — easier to lazily bootstrap.
var defaultExpenseCategories = []struct {
	Name  string
	Icon  string
	Color string
}{
	{"生活", "🛒", "#3b82f6"},
	{"教育", "📚", "#8b5cf6"},
	{"医疗", "🏥", "#ef4444"},
	{"交通", "🚗", "#06b6d4"},
	{"娱乐", "🎮", "#ec4899"},
	{"大件", "📦", "#f59e0b"},
	{"其他", "📝", "#6b7280"},
}

// bootstrapExpenseCategories ensures a workspace has the 7 presets.
// Idempotent via the UNIQUE (workspace_id, name) index — concurrent
// calls won't double-insert.
func (p *Plugin) bootstrapExpenseCategories(workspaceID string) error {
	for i, c := range defaultExpenseCategories {
		_, err := p.DB.Exec(
			`INSERT INTO expense_categories (workspace_id, name, icon, color, sort_order)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (workspace_id, name) DO NOTHING`,
			workspaceID, c.Name, c.Icon, c.Color, i,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *Plugin) listExpenseCategories(workspaceID string, includeHidden bool) ([]ExpenseCategory, error) {
	q := `SELECT id, workspace_id, name, icon, color, sort_order, is_hidden, created_at
	        FROM expense_categories WHERE workspace_id = $1`
	if !includeHidden {
		q += ` AND is_hidden = FALSE`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := p.DB.Query(q, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ExpenseCategory, 0)
	for rows.Next() {
		var c ExpenseCategory
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Icon, &c.Color, &c.SortOrder, &c.IsHidden, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Plugin) createExpenseCategory(workspaceID, name, icon, color string, sortOrder int) (*ExpenseCategory, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("category name required")
	}
	if color == "" {
		color = "#888"
	}
	var c ExpenseCategory
	err := p.DB.QueryRow(
		`INSERT INTO expense_categories (workspace_id, name, icon, color, sort_order)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, workspace_id, name, icon, color, sort_order, is_hidden, created_at`,
		workspaceID, name, icon, color, sortOrder,
	).Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Icon, &c.Color, &c.SortOrder, &c.IsHidden, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

type ExpenseCategoryUpdate struct {
	Name      *string
	Icon      *string
	Color     *string
	SortOrder *int
	IsHidden  *bool
}

func (p *Plugin) updateExpenseCategory(id int64, workspaceID string, u ExpenseCategoryUpdate) error {
	sets := []string{}
	args := []any{}
	idx := 1
	add := func(col string, v any) {
		sets = append(sets, fmt.Sprintf("%s = $%d", col, idx))
		args = append(args, v)
		idx++
	}
	if u.Name != nil {
		add("name", strings.TrimSpace(*u.Name))
	}
	if u.Icon != nil {
		add("icon", *u.Icon)
	}
	if u.Color != nil {
		add("color", *u.Color)
	}
	if u.SortOrder != nil {
		add("sort_order", *u.SortOrder)
	}
	if u.IsHidden != nil {
		add("is_hidden", *u.IsHidden)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id, workspaceID)
	q := fmt.Sprintf(
		`UPDATE expense_categories SET %s WHERE id = $%d AND workspace_id = $%d`,
		strings.Join(sets, ", "), idx, idx+1,
	)
	_, err := p.DB.Exec(q, args...)
	return err
}

func (p *Plugin) deleteExpenseCategory(id int64, workspaceID string) error {
	_, err := p.DB.Exec(
		`DELETE FROM expense_categories WHERE id = $1 AND workspace_id = $2`,
		id, workspaceID,
	)
	return err
}

// ---- expenses ----

func scanExpense(rs interface {
	Scan(...any) error
}) (*Expense, error) {
	var e Expense
	var categoryID sql.NullInt64
	var rawImageID sql.NullString
	if err := rs.Scan(
		&e.ID, &e.WorkspaceID, &e.CreatedByUserID, &e.Amount, &e.Currency,
		&e.Merchant, &categoryID, &e.ConsumeTime, &e.HasDetailTime, &e.Region,
		&rawImageID, &e.Status, &e.Confidence, &e.Remark, &e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if categoryID.Valid {
		v := categoryID.Int64
		e.CategoryID = &v
	}
	if rawImageID.Valid {
		v := rawImageID.String
		e.RawImageID = &v
	}
	return &e, nil
}

const expenseSelectCols = `id, workspace_id, created_by_user_id, amount, currency,
		merchant, category_id, consume_time, has_detail_time, region,
		raw_image_id, status, confidence, remark, created_at, updated_at`

type ExpenseListFilter struct {
	Start      *time.Time // inclusive lower bound on consume_time
	End        *time.Time // exclusive upper bound
	CategoryID *int64
	Status     *int    // nil = both
	Limit      int     // 0 = default 100
	Offset     int
}

func (p *Plugin) listExpenses(workspaceID string, f ExpenseListFilter) ([]Expense, int, error) {
	conds := []string{"workspace_id = $1"}
	args := []any{workspaceID}
	idx := 2
	if f.Start != nil {
		conds = append(conds, fmt.Sprintf("consume_time >= $%d", idx))
		args = append(args, *f.Start)
		idx++
	}
	if f.End != nil {
		conds = append(conds, fmt.Sprintf("consume_time < $%d", idx))
		args = append(args, *f.End)
		idx++
	}
	if f.CategoryID != nil {
		conds = append(conds, fmt.Sprintf("category_id = $%d", idx))
		args = append(args, *f.CategoryID)
		idx++
	}
	if f.Status != nil {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, *f.Status)
		idx++
	}
	where := strings.Join(conds, " AND ")

	var total int
	if err := p.DB.QueryRow(`SELECT COUNT(*) FROM expenses WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit, f.Offset)
	q := `SELECT ` + expenseSelectCols + ` FROM expenses WHERE ` + where +
		fmt.Sprintf(` ORDER BY consume_time DESC, id DESC LIMIT $%d OFFSET $%d`, idx, idx+1)
	rows, err := p.DB.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Expense, 0)
	for rows.Next() {
		e, err := scanExpense(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *e)
	}
	return out, total, rows.Err()
}

func (p *Plugin) getExpense(id int64, workspaceID string) (*Expense, error) {
	row := p.DB.QueryRow(
		`SELECT `+expenseSelectCols+` FROM expenses WHERE id = $1 AND workspace_id = $2`,
		id, workspaceID,
	)
	e, err := scanExpense(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (p *Plugin) createExpense(e *Expense) error {
	if e == nil {
		return errors.New("createExpense: nil")
	}
	if e.Currency == "" {
		e.Currency = "CNY"
	}
	if e.Confidence == 0 {
		e.Confidence = 100
	}
	if e.ConsumeTime.IsZero() {
		e.ConsumeTime = time.Now().UTC()
	}
	return p.DB.QueryRow(
		`INSERT INTO expenses (workspace_id, created_by_user_id, amount, currency,
		   merchant, category_id, consume_time, has_detail_time, region,
		   raw_image_id, status, confidence, remark)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id, created_at, updated_at`,
		e.WorkspaceID, e.CreatedByUserID, e.Amount, e.Currency,
		e.Merchant, e.CategoryID, e.ConsumeTime, e.HasDetailTime, e.Region,
		e.RawImageID, e.Status, e.Confidence, e.Remark,
	).Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
}

type ExpenseUpdate struct {
	Amount        *float64
	Currency      *string
	Merchant      *string
	CategoryID    *int64 // pointer-to-pointer semantics: set to *(int64)(0) to clear
	ClearCategory bool
	ConsumeTime   *time.Time
	HasDetailTime *bool
	Region        *string
	Status        *int
	Remark        *string
}

func (p *Plugin) updateExpense(id int64, workspaceID string, u ExpenseUpdate) error {
	sets := []string{}
	args := []any{}
	idx := 1
	add := func(col string, v any) {
		sets = append(sets, fmt.Sprintf("%s = $%d", col, idx))
		args = append(args, v)
		idx++
	}
	if u.Amount != nil {
		add("amount", *u.Amount)
	}
	if u.Currency != nil {
		add("currency", *u.Currency)
	}
	if u.Merchant != nil {
		add("merchant", *u.Merchant)
	}
	if u.ClearCategory {
		add("category_id", nil)
	} else if u.CategoryID != nil {
		add("category_id", *u.CategoryID)
	}
	if u.ConsumeTime != nil {
		add("consume_time", *u.ConsumeTime)
	}
	if u.HasDetailTime != nil {
		add("has_detail_time", *u.HasDetailTime)
	}
	if u.Region != nil {
		add("region", *u.Region)
	}
	if u.Status != nil {
		add("status", *u.Status)
	}
	if u.Remark != nil {
		add("remark", *u.Remark)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, fmt.Sprintf("updated_at = $%d", idx))
	args = append(args, time.Now().UTC())
	idx++
	args = append(args, id, workspaceID)
	q := fmt.Sprintf(
		`UPDATE expenses SET %s WHERE id = $%d AND workspace_id = $%d`,
		strings.Join(sets, ", "), idx, idx+1,
	)
	_, err := p.DB.Exec(q, args...)
	return err
}

func (p *Plugin) deleteExpense(id int64, workspaceID string) error {
	_, err := p.DB.Exec(`DELETE FROM expenses WHERE id = $1 AND workspace_id = $2`, id, workspaceID)
	return err
}
