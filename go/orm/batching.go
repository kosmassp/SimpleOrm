package orm

import (
	"context"
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// Statement batching — the plan (Level 4 item 3, ADR-0044, spec/session.md "Batching"). A message is what one round
// trip sends; the statements of one call — a Flush, the Send of an explicit batch — are formed into messages by three
// rules, walking them in order, and the same plan serves every dialect and every driver:
//
//  1. a statement that depends on the message in progress starts the next one — decided before it is prepared,
//     because preparing reads what the earlier statements produce: the message holds a statement on the same
//     instance, or on another instance of the same type with the same set key (1a — the identity gate would
//     otherwise read a map the message has not yet updated), or the statement's instance has a non-null many-to-one
//     navigation, or in a flush a cascade owner it takes its key from, pointing at an instance whose insert is in
//     the message and whose key the database generates (1b);
//  2. a statement that reads a result starts the next one after a registry command (whose own result sets would
//     otherwise come first);
//  3. a statement that would overflow starts the next one: the dialect caps a message's parameters and this
//     statement's would take it over the cap (a statement alone over the cap is a message of its own).
//
// A message that holds MaxStatementsPerMessage statements goes out as soon as it is full — before the next statement is
// prepared, like a rule-1 cut; rule 2 and the parameter cap are decided after the statement is prepared. Under Off the cap
// is one: every statement is prepared and run before the next is prepared. A message is realized by the dialect's
// StatementBatcher (one round trip) or, where there is none, one statement at a time reusing one prepared command per
// distinct statement text within the call; its statements are then judged and applied in order exactly as their
// immediate operations would be.

// batchCall is one call's plan in progress: the message being formed, the undo journal of everything the call has
// done in memory, and the realization in force.
type batchCall struct {
	db      *Db
	journal *undoJournal
	auto    bool
	limit   int
	// parameterCap is the dialect's parameters per message, 0 for none.
	parameterCap int
	// runner runs a statement on its own: the direct path under Off, a prepared-command cache under Auto.
	runner   statementRunner
	prepared *preparedRunner
	observer core.MessageObserver

	current  message
	messages int
}

// message is the statements formed so far, with the facts the plan's rules read.
type message struct {
	writes     []*preparedWrite
	instances  map[any]bool // rule 1a: the instances a statement of the message writes
	keys       []messageKey // rule 1a: the set keys the message's statements are about, as they were prepared
	generated  map[any]bool // rule 1b: the instances whose insert is in the message with a database-generated key
	hasCommand bool         // rule 2: a registry command is in the message
	parameters int          // rule 3: the bound parameters of the message
}

func newMessage() message {
	return message{instances: map[any]bool{}, generated: map[any]bool{}}
}

// newCall starts the plan of one flush or explicit batch send. The journal takes the undo of the call's effects.
func (db *Db) newCall(journal *undoJournal) *batchCall {
	call := &batchCall{
		db:           db,
		journal:      journal,
		auto:         db.options.Batching == BatchingAuto,
		limit:        db.maxStatements,
		parameterCap: db.options.Dialect.MaxParametersPerMessage(),
		current:      newMessage(),
	}
	call.observer, _ = db.options.Dialect.(core.MessageObserver)
	return call
}

// statementRunner is how a statement runs on its own: straight on the session's executor under Off, through a cache
// of prepared commands under Auto where the dialect does not send the message itself. Built at first use, inside the
// call's transaction, so the commands belong to it.
func (c *batchCall) statementRunner() statementRunner {
	if c.runner == nil {
		if c.auto {
			c.prepared = newPreparedRunner(c.db.exec())
			c.runner = c.prepared
		} else {
			c.runner = directRunner{c.db.exec()}
		}
	}
	return c.runner
}

// subject is what the plan's rule 1 asks about the operation it is about to prepare (spec/session.md "Batching").
type subject struct {
	// m is the entity map of the type the operation writes; nil for a registry command.
	m *core.EntityMap
	// instance is the caller's *E the operation writes; nil for a delete by key and for a registry command.
	instance any
	// depends are the instances the operation reads a generated key from — its non-null many-to-one targets and, in
	// a flush, the cascade owners it takes its key from (rule 1b).
	depends []any
	// key is a delete by key's key argument, validated (rule 1a's key clause); nil otherwise and when it does not validate.
	key core.KeyTuple
}

// keyNow is the key the operation's statement is about, as it stands: the instance's key if it is set (not null, not a
// database-generated key at its default, not an empty client GUID — the "unset key" of unit-of-work.md "Detach and
// merge"), or a delete by key's argument; nil when there is none.
func (s subject) keyNow() core.KeyTuple {
	if s.m == nil {
		return nil
	}
	if s.instance == nil {
		return s.key
	}
	values, err := s.m.KeyValues(s.instance)
	if err != nil {
		return nil
	}
	return setKey(s.m, values)
}

// setKey is values as a key tuple if every part is set, else nil.
func setKey(m *core.EntityMap, values []any) core.KeyTuple {
	if len(values) == 0 {
		return nil
	}
	for _, value := range values {
		if unsetKeyPart(m, value) {
			return nil
		}
	}
	return core.KeyTuple(values)
}

// unsetKeyPart: null, a database-generated key at its default, or an empty client-generated GUID; a natural key's
// default value is a key (spec/unit-of-work.md "Clarifications on detach and merge").
func unsetKeyPart(m *core.EntityMap, value any) bool {
	return value == nil || (m.KeyStrategy != core.KeyNatural && reflect.ValueOf(value).IsZero())
}

// messageKey is a key a statement of the message is about, with its entity type: rule 1a's key clause.
type messageKey struct {
	entityType reflect.Type
	key        core.KeyTuple
}

// before applies rule 1 ahead of preparing the operation s: if the message in progress holds a statement it depends
// on, the message is sent first, so its statements are complete — the generated keys written back, the identity map
// updated — when the operation prepares. It depends on the message when the message holds a statement on the same
// instance (1a), on another instance of the same type with the same set key (1a: the identity gate would otherwise
// read a map the message has not yet updated), or when one of its depends is inserted in the message with a
// database-generated key (1b). The new operation's key is taken as it stands before its preparation.
func (c *batchCall) before(ctx context.Context, s subject) error {
	if len(c.current.writes) == 0 {
		return nil
	}
	cut := s.instance != nil && c.current.instances[s.instance]
	for _, parent := range s.depends {
		if c.current.generated[parent] {
			cut = true
		}
	}
	if !cut {
		if key := s.keyNow(); key != nil {
			for _, held := range c.current.keys {
				if held.entityType == s.m.Type && held.key.Compare(key) == 0 {
					cut = true
					break
				}
			}
		}
	}
	if !cut {
		return nil
	}
	return c.send(ctx)
}

// add puts a prepared statement of the operation s into the message in progress. Rules 2 and 3's parameter cap are
// decided now that the statement's parameters are bound; a message that holds MaxStatementsPerMessage statements goes
// out as soon as it is full — before the next statement is prepared, like a rule-1 cut — and under Off, whose cap is
// one, every message does. The message's keys are taken as the statement was prepared.
func (c *batchCall) add(ctx context.Context, write *preparedWrite, s subject) error {
	if len(c.current.writes) > 0 {
		overflow := c.parameterCap > 0 && c.current.parameters+len(write.args) > c.parameterCap
		if overflow || (write.readsResult() && c.current.hasCommand) {
			if err := c.send(ctx); err != nil {
				return err
			}
		}
	}
	c.current.writes = append(c.current.writes, write)
	c.current.parameters += len(write.args)
	if write.instance != nil {
		c.current.instances[write.instance] = true
	}
	if write.generatesKey && write.instance != nil {
		c.current.generated[write.instance] = true
	}
	if write.command {
		c.current.hasCommand = true
	}
	if key := s.keyNow(); key != nil {
		c.current.keys = append(c.current.keys, messageKey{s.m.Type, key})
	}
	if !c.auto || len(c.current.writes) >= c.limit {
		return c.send(ctx)
	}
	return nil
}

// finish sends what is left of the plan.
func (c *batchCall) finish(ctx context.Context) error { return c.send(ctx) }

// close releases the call's prepared commands; it runs before the call's transaction ends.
func (c *batchCall) close() {
	if c.prepared != nil {
		c.prepared.close()
	}
}

// send realizes the message in progress and completes its statements in order. The first statement that fails its
// check fails the call (spec/session.md "Batching", Failure).
func (c *batchCall) send(ctx context.Context) error {
	writes := c.current.writes
	c.current = newMessage()
	if len(writes) == 0 {
		return nil
	}
	c.messages++
	if c.observer != nil {
		c.observer.ObserveMessage(len(writes))
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if c.db.batcher != nil {
		statements := make([]core.MessageStatement, len(writes))
		for i, write := range writes {
			statements[i] = core.MessageStatement{SQL: write.sql, Args: write.args, Result: write.kind}
		}
		results, err := c.db.batcher.SendMessage(ctx, c.db.connection, c.db.exec(), statements)
		if err != nil {
			return err
		}
		for i, write := range writes {
			if err := c.complete(write, writeResult{key: results[i].Key, affected: results[i].Affected}); err != nil {
				return err
			}
		}
		return nil
	}

	runner := c.statementRunner()
	for _, write := range writes {
		result, err := runWrite(ctx, runner, write)
		if err != nil {
			return err
		}
		if err := c.complete(write, result); err != nil {
			return err
		}
	}
	return nil
}

// complete judges one statement's result and applies it; a statement that read a count keeps the judged count for
// an explicit batch's result.
func (c *batchCall) complete(write *preparedWrite, result writeResult) error {
	if write.finish != nil {
		if err := write.finish(result, c.journal); err != nil {
			return err
		}
	}
	if write.kind == core.ResultReportedCount || write.kind == core.ResultDriverCount {
		write.affected = result.affected
	}
	return nil
}
