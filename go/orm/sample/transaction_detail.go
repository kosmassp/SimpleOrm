package sample

import "github.com/kosmassp/SimpleOrm/go/orm"

// TransactionDetail is table transaction_details (STRICT). Key: id,
// database-generated; transaction_id references transactions. The child side
// of the json_group_array nesting pattern (§7.10).
type TransactionDetail struct {
	BaseModel
	ID            int64 `orm:"column,key,generated"`
	TransactionID int64 `orm:"column"`
	// Transaction is populated only by the library (Level 2 loading); it can never disagree with TransactionID.
	Transaction *Transaction `orm:"many_to_one=TransactionID"`
	Description string       `orm:"column"`
	Quantity    int32        `orm:"column"`
	UnitPrice   orm.Decimal  `orm:"column"`
}

func (TransactionDetail) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:      orm.Table("transaction_details"),
		ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[Transaction]("TransactionID")},
		Indexes:     []orm.IndexDef{orm.Index("TransactionID")},
	}
}
