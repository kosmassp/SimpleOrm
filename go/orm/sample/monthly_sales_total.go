package sample

import "github.com/kosmassp/SimpleOrm/go/orm"

// MonthlySalesTotal is materialized view monthly_sales_totals (ADR-0008 add.):
// one row per calendar month, read-only, carrying an index — the capability
// that distinguishes a materialized view from a plain view. Dormant on SQLite
// (no materialized views, DDL-002 on create): declaration-only metadata, still
// exported and pinned.
type MonthlySalesTotal struct {
	// SalesMonth is the calendar month as YYYY-MM.
	SalesMonth       string      `orm:"column,key"`
	TransactionCount int32       `orm:"column"`
	TotalAmount      orm.Decimal `orm:"column"`
}

func (MonthlySalesTotal) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source: orm.MaterializedView("monthly_sales_totals", `
    select strftime('%Y-%m', created_at) as sales_month,
           count(id)                     as transaction_count,
           sum(amount)                   as total_amount
    from transactions
    group by sales_month
    `),
		Indexes: []orm.IndexDef{orm.Index("SalesMonth").Unique()},
	}
}
