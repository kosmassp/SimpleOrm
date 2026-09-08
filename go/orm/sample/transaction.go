package sample

import "github.com/kosmassp/SimpleOrm/go/orm"

// Transaction is table transactions (STRICT). Key: id, database-generated;
// user_id references users. Carries the version column — the fixture entity
// for optimistic concurrency (§7.16).
type Transaction struct {
	BaseModel
	ID     int64 `orm:"column,key,generated"`
	UserID int64 `orm:"column"`
	// User is populated only by the library (Level 2 loading); it can never disagree with UserID.
	User    *User             `orm:"many_to_one=UserID"`
	Status  TransactionStatus `orm:"column"`
	Amount  orm.Decimal       `orm:"column"`
	Version int64             `orm:"column,version"`
	// Note was added by migration V0003.
	Note *string `orm:"column"`
	// Details is populated only by the library (Level 2 loading); never a column, never written.
	Details []*TransactionDetail `orm:"one_to_many=TransactionID"`
}

func (Transaction) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:      orm.Table("transactions"),
		ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[User]("UserID")},
		Indexes: []orm.IndexDef{
			orm.Index("UserID"),
			orm.Index("Status", "CreatedAtUtc", orm.Desc).Named("ix_transactions_status_created"),
		},
	}
}
