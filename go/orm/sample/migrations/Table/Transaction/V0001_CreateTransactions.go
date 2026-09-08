// Package transaction holds the transactions table's migration steps,
// mirroring dotnet/samples/SimpleOrm.Sample/Migrations/Table/Transaction
// (CLAUDE.md §7.22).
package transaction

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0001_CreateTransactions is frozen to literal SQL when V0003 changed the
// table (ADR-0013/0016): the shape transactions had at V0001.
type V0001_CreateTransactions struct {
	orm.TableMigration[sample.Transaction]
}

func (V0001_CreateTransactions) Action(actions *orm.TableActions) {
	actions.SQL(`create table if not exists transactions (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL,
    status      TEXT NOT NULL,
    amount      TEXT NOT NULL,
    version     INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT
) STRICT`)
	actions.SQL("create index if not exists ix_transactions_user_id on transactions (user_id)")
	actions.SQL("create index if not exists ix_transactions_status_created on transactions (status, created_at desc)")
}
