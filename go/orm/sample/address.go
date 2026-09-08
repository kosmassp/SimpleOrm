package sample

// Address is the owned-type fixture (ADR-0030): a value object with no table
// of its own, stored as columns of UserProfile under the address_ prefix
// (address_street, address_city, address_postal_code). Only column fields;
// implementing orm.OwnedType is what keeps it out of the entity set.
type Address struct {
	Street     string  `orm:"column"`
	City       string  `orm:"column"`
	PostalCode *string `orm:"column"`
}

// OwnedType marks Address as an owned value type (ADR-0030).
func (Address) OwnedType() {}
