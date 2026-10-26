package orm

import (
	"context"
	"fmt"
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/params"
)

// The explicit batch (Level 4 item 3, ADR-0044, spec/session.md "The explicit batch"): a queue of writes and registry
// commands bound to the session, sent in one visible call. Queuing does no I/O, binds no value, and checks nothing but
// the batch's own state; Send runs the queued operations in queue order, as messages by the plan, and each behaves
// exactly as its immediate call would at that point of the sequence — the same checks, the same statement, the same
// effects on the instance and the session. Keys do not flow between queued operations (the immediate calls never flow
// them); the unit of work is the form that flows keys. Reads are not batched.
//
// Go shape (CODING-STANDARD §10): generics cannot be methods, so the operations that name a type are package
// functions beside the immediate calls' — BatchInsert, BatchUpdate, BatchUpdateOnly, BatchDelete, BatchDeleteEntity,
// BatchExecute — and Send is a method. There is no disposal in Go: a batch that is not sent is simply dropped, and
// Discard is the explicit form — either way nothing is ever sent implicitly.

// Batch is a queue of writes bound to one session. It is spent by its Send (sent or failed) or by Discard; a spent
// batch takes nothing more (TX-003). A session is one connection and not safe for concurrent use, and neither is a batch.
type Batch struct {
	db    *Db
	ops   []batchOperation
	spent bool
}

// BatchResult is what a send did: Affected holds one count per queued operation, in queue order — an insert 1, a
// generated update or delete its judged count, a registry command the driver's count, an operation that issued no
// statement (a clean tracked update) 0 — and Messages the number of messages the plan formed.
type BatchResult struct {
	Affected []int64
	Messages int
}

// batchOperation is one queued operation, prepared at send time.
type batchOperation struct {
	// entityType and instance identify the entity an operation writes (instance is the caller's *E): the plan's rule 1
	// and the undo journal read them. Both are nil for a registry command; a delete by key has the type only.
	entityType reflect.Type
	instance   any
	// key is a delete by key's key argument, as the caller passed it: rule 1a compares it with the keys in the message.
	key any
	// prepare builds the statement, or nothing for an operation that issues none.
	prepare func() (*preparedWrite, error)
}

// Batch starts an explicit batch on the session. Queuing on it does no I/O.
func (db *Db) Batch() *Batch { return &Batch{db: db} }

// Discard drops the batch unsent: it is spent, and queuing on it or sending it is TX-003 — the analog of disposing an
// unsent batch, which sends nothing. Discarding a spent batch is a no-op.
func (b *Batch) Discard() { b.spent = true; b.ops = nil }

// queue appends one operation; TX-003 once the batch is spent.
func (b *Batch) queue(operation batchOperation) error {
	if b.spent {
		return b.spentError("queuing an operation on")
	}
	b.ops = append(b.ops, operation)
	return nil
}

func (b *Batch) spentError(what string) error {
	return core.Errorf("TX-003", "batch", "%s a batch that was sent or discarded; a batch is spent by its send — start another with db.Batch()", what)
}

// BatchInsert queues Insert(entity) (spec/crud.md): a generated key lands on the entity when the batch is sent.
func BatchInsert[T any](b *Batch, entity *T) error {
	if entity == nil {
		return fmt.Errorf("orm.BatchInsert: entity is nil")
	}
	entityType := reflect.TypeFor[T]()
	return b.queue(batchOperation{entityType: entityType, instance: entity,
		prepare: func() (*preparedWrite, error) { return b.db.prepareInsert(entityType, entity) }})
}

// BatchUpdate queues Update(entity): the full row, or a tracked instance's dirty columns (nothing, for a clean one).
func BatchUpdate[T any](b *Batch, entity *T) error {
	if entity == nil {
		return fmt.Errorf("orm.BatchUpdate: entity is nil")
	}
	entityType := reflect.TypeFor[T]()
	return b.queue(batchOperation{entityType: entityType, instance: entity,
		prepare: func() (*preparedWrite, error) { return b.db.prepareUpdate(entityType, entity) }})
}

// BatchUpdateOnly queues UpdateOnly(entity, properties...); the list is validated at Send.
func BatchUpdateOnly[T any](b *Batch, entity *T, properties ...string) error {
	if entity == nil {
		return fmt.Errorf("orm.BatchUpdateOnly: entity is nil")
	}
	entityType := reflect.TypeFor[T]()
	return b.queue(batchOperation{entityType: entityType, instance: entity,
		prepare: func() (*preparedWrite, error) { return b.db.prepareUpdateOnly(entityType, entity, properties) }})
}

// BatchDelete queues Delete[T](key): the plain form by key (a value, or []any for a composite key).
func BatchDelete[T any](b *Batch, key any) error {
	entityType := reflect.TypeFor[T]()
	return b.queue(batchOperation{entityType: entityType, key: key,
		prepare: func() (*preparedWrite, error) { return b.db.prepareDeleteByKey(entityType, key) }})
}

// BatchDeleteEntity queues DeleteEntity(entity): the version-checked form.
func BatchDeleteEntity[T any](b *Batch, entity *T) error {
	if entity == nil {
		return fmt.Errorf("orm.BatchDeleteEntity: entity is nil")
	}
	entityType := reflect.TypeFor[T]()
	return b.queue(batchOperation{entityType: entityType, instance: entity,
		prepare: func() (*preparedWrite, error) { return b.db.prepareDeleteEntity(entityType, entity) }})
}

// BatchExecute queues Execute(command, args): a registry command, counted as the driver counts it. Its args bind at Send.
func BatchExecute[TArgs any](b *Batch, command CommandEntry[TArgs], args TArgs) error {
	return b.queue(batchOperation{prepare: func() (*preparedWrite, error) {
		sqlText, err := command.Source().SQL()
		if err != nil {
			return nil, err
		}
		bound, err := params.Bind(sqlText, args, b.db.converter, b.db.options.Dialect, command.Source().Description())
		if err != nil {
			return nil, err
		}
		return &preparedWrite{sql: bound.SQL, args: bound.Args, kind: core.ResultDriverCount, command: true}, nil
	}})
}

// Send runs the queued operations in queue order, as messages by the plan, and returns their counts. It always runs
// in a transaction: the session's when one is active (left open), else one it begins and commits after the last
// message, and rolls back on failure so nothing applies. A failed send undoes every in-memory effect of every
// statement it prepared — keys, versions, stamps, flags, tracking — so the instances are as they were before the
// call; inside a caller's transaction the rows of the statements that ran, possibly some after the failing one in
// its message, are the caller's to roll back. The batch is spent either way (TX-003 on a second send); an empty
// batch returns an empty result and begins no transaction.
func (b *Batch) Send(ctx context.Context) (BatchResult, error) {
	if b.spent {
		return BatchResult{}, b.spentError("sending")
	}
	b.spent = true
	operations := b.ops
	b.ops = nil
	if len(operations) == 0 {
		return BatchResult{Affected: []int64{}}, nil
	}
	if err := ctx.Err(); err != nil {
		return BatchResult{}, err
	}

	db := b.db
	var tx *Tx
	if db.tx == nil {
		var err error
		if tx, err = db.Begin(ctx); err != nil {
			return BatchResult{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }() // a no-op once committed; the failure path's rollback
	}

	journal := &undoJournal{}
	call := db.newCall(journal)
	writes, err := b.run(ctx, call, operations)
	call.close() // the prepared commands belong to the transaction that is about to end
	if err == nil && tx != nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		journal.rollback()
		return BatchResult{}, err
	}

	result := BatchResult{Affected: make([]int64, len(writes)), Messages: call.messages}
	for i, write := range writes {
		if write != nil {
			result.Affected[i] = write.affected
		}
	}
	return result, nil
}

// run prepares each queued operation in order and feeds its statement to the call; the writes come back in queue
// order, nil for an operation that issued no statement.
func (b *Batch) run(ctx context.Context, call *batchCall, operations []batchOperation) ([]*preparedWrite, error) {
	db := b.db
	writes := make([]*preparedWrite, len(operations))
	for i, operation := range operations {
		s := subject{instance: operation.instance}
		if operation.entityType != nil {
			var err error
			if s.m, err = db.maps.Load(operation.entityType); err != nil {
				return writes, err
			}
			switch {
			case operation.instance != nil:
				s.depends = m2oTargets(s.m, operation.instance)
			case operation.key != nil:
				// A delete by key compares its key argument; one that does not validate is refused when it prepares.
				if values, err := validateKey(s.m, operation.key); err == nil {
					s.key = setKey(s.m, values)
				}
			}
		}
		// Rule 1 is decided before the operation prepares: preparing reads what the earlier statements produce.
		if err := call.before(ctx, s); err != nil {
			return writes, err
		}
		if operation.instance != nil {
			call.journal.recordWrite(db, s.m, operation.instance)
		}
		write, err := operation.prepare()
		if err != nil {
			return writes, err
		}
		if write == nil {
			continue // no statement: a clean tracked update adds nothing to the message
		}
		writes[i] = write
		if err := call.add(ctx, write, s); err != nil {
			return writes, err
		}
	}
	return writes, call.finish(ctx)
}
