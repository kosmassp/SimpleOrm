package sample

import (
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
)

// UserTransactionTotal is view user_transaction_totals (ADR-0008 add.3):
// read-only, keyed by user so read-by-key works, never generated — nothing is
// written to a view. The defining SELECT lives in the descriptor and
// CreateView generates the CREATE VIEW. Not a BaseModel: projections carry no
// audit columns.
type UserTransactionTotal struct {
	UserID           int64       `orm:"column,key"`
	UserName         string      `orm:"column"`
	TransactionCount int32       `orm:"column"`
	TotalAmount      orm.Decimal `orm:"column"`
	// LastTransactionAtUtc was added by migration V0006; null for users with no transactions.
	LastTransactionAtUtc *time.Time `orm:"column=last_transaction_at"`
}

func (UserTransactionTotal) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("user_transaction_totals", `
    select u.id              as user_id,
           u.name            as user_name,
           count(t.id)       as transaction_count,
           coalesce(sum(t.amount), 0) as total_amount,
           max(t.created_at) as last_transaction_at
    from users u
    left join transactions t on t.user_id = u.id
    group by u.id, u.name
    `)}
}
