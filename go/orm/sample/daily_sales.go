package sample

import (
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
)

// DailySales is a statement-backed entity (ADR-0008 add.2, ADR-0010): the type
// IS the query — inline SQL plus the declared parameter contract. Read-only and
// keyless; SchemaGuard validates by preparing the statement; the `-- notnull:`
// comment lifts the expression columns' nullability (§7.19). Not a BaseModel:
// projections carry no audit columns.
type DailySales struct {
	SalesDate        time.Time   `orm:"column,type=date"`
	TransactionCount int32       `orm:"column"`
	TotalAmount      orm.Decimal `orm:"column"`
}

func (DailySales) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Statement(`
    -- notnull: sales_date, transaction_count, total_amount
    select date(created_at) as sales_date,
           count(id)        as transaction_count,
           sum(amount)      as total_amount
    from transactions
    where created_at >= @since
    group by date(created_at)
    order by sales_date desc
    `, orm.Param[time.Time]("since"))}
}

// DailySalesArgs is the args for executing DailySales (§7.12): its field binds
// to @since; the declared type must match (PRM-012).
type DailySalesArgs struct {
	Since time.Time
}
