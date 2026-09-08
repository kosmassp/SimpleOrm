package sample

import (
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
)

// UserActivityReport is procedure user_activity_report (ADR-0008 addenda):
// self-contained — name, body SQL, and parameter contract in the descriptor.
// Read-only and keyless. Dormant on SQLite (no stored procedures): creation and
// invocation arrive with a Level 4 dialect; the export is still pinned.
type UserActivityReport struct {
	UserID               int64       `orm:"column"`
	UserName             string      `orm:"column"`
	TransactionCount     int32       `orm:"column"`
	TotalAmount          orm.Decimal `orm:"column"`
	LastTransactionAtUtc *time.Time  `orm:"column"`
}

func (UserActivityReport) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Procedure("user_activity_report", `
    select u.id                as user_id,
           u.name              as user_name,
           count(t.id)         as transaction_count,
           coalesce(sum(t.amount), 0) as total_amount,
           max(t.created_at)   as last_transaction_at_utc
    from users u
    left join transactions t on t.user_id = u.id and t.created_at >= @since
    group by u.id, u.name
    `, orm.Param[time.Time]("since"))}
}
