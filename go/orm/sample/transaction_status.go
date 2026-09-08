package sample

// TransactionStatus is stored as TEXT by name, matched case-insensitively on
// read (§7.9); a string-kinded enum, so the name is the value. Declaring
// EnumNames is what makes it an enum to the loader (orm.Enum).
type TransactionStatus string

const (
	Pending   TransactionStatus = "Pending"
	Completed TransactionStatus = "Completed"
	Cancelled TransactionStatus = "Cancelled"
)

// EnumNames lists the members in declaration order.
func (TransactionStatus) EnumNames() []string { return []string{"Pending", "Completed", "Cancelled"} }
