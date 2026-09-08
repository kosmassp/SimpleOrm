package sample

import (
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
)

// A handful of registry entries mirroring
// dotnet/samples/SimpleOrm.Sample/Repositories/UserRepository.cs and
// TransactionRepository.cs: those repositories reach the criteria core for
// these same reads (Level 1's `Query().Where(...)`); this port's registry
// entries are inline SQL instead (ADR-0009/ADR-0027), so SchemaGuard and the
// CLI have real entries to validate. Column lists are explicit (VAL-021) and
// match each entity's mapped, non-navigation columns exactly (§7.7).

// UsersByEmailArgs binds UsersByEmail's @Email placeholder.
type UsersByEmailArgs struct {
	Email string
}

// UsersByEmail mirrors UserRepository.GetByEmailAsync.
var UsersByEmail = orm.Inline[UsersByEmailArgs, User](
	"select id, name, email, display_name, created_at, updated_at from users where email = @Email")

// UsersByIDsArgs binds UsersByIDs's @IDs placeholder (expanded to a generated
// IN list at bind time, §7.12).
type UsersByIDsArgs struct {
	IDs []int64
}

// UsersByIDs mirrors UserRepository.GetByIdsAsync.
var UsersByIDs = orm.Inline[UsersByIDsArgs, User](
	"select id, name, email, display_name, created_at, updated_at from users where id in (@IDs) order by id")

// TransactionsByUserArgs binds TransactionsByUser's @UserID placeholder.
type TransactionsByUserArgs struct {
	UserID int64
}

// TransactionsByUser mirrors TransactionRepository.GetByUserAsync.
var TransactionsByUser = orm.Inline[TransactionsByUserArgs, Transaction](
	"select id, user_id, status, amount, version, note, created_at, updated_at " +
		"from transactions where user_id = @UserID order by id")

// TransactionsByStatusArgs binds TransactionsByStatus's @Status placeholder.
type TransactionsByStatusArgs struct {
	Status TransactionStatus
}

// TransactionsByStatus mirrors TransactionRepository.GetByStatusAsync.
var TransactionsByStatus = orm.Inline[TransactionsByStatusArgs, Transaction](
	"select id, user_id, status, amount, version, note, created_at, updated_at " +
		"from transactions where status = @Status order by id")

// MarkTransactionStatusArgs binds MarkTransactionStatus's placeholders.
type MarkTransactionStatusArgs struct {
	ID        int64
	Status    TransactionStatus
	UpdatedAt time.Time
}

// MarkTransactionStatus is the one legitimate hand-SQL write from
// TransactionRepository.SetStatusAsync: a partial update (§7.15) — the
// generated Update writes every mapped column, so a status-only change is
// hand SQL by design.
var MarkTransactionStatus = orm.InlineCommand[MarkTransactionStatusArgs](
	"update transactions set status = @Status, updated_at = @UpdatedAt where id = @ID")
