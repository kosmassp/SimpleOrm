package conformance

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
)

// TestBatchCases_BehaveAsSpecified is the conformance/batch-cases/ runner (Level 4 item 3, ADR-0044,
// spec/session.md "Batching" and "The step scripts"): the step engine of the other session folders with a
// `batching` header, the `batch` and `batchAgain` verbs, and expectMessages — the plan's grouping of a step's
// statements, formed under the SQLite dialect's facts (no reported counts, no parameter cap) whether or not a message
// was realized as one round trip. On SQLite it never is: the plan's messages run one statement at a time, reusing
// one prepared command per distinct statement text within the call. Every case also runs with the recording dialect
// behaving as a batching one (OneRoundTrip: a message goes to SendMessage and its results are judged afterwards), so the
// session's batched branch meets the same expectations against a real database.
func TestBatchCases_BehaveAsSpecified(t *testing.T) {
	runStepCases(t, "batch-cases",
		stepVariant{name: "as written"},
		stepVariant{name: "one round trip per message", oneRoundTrip: true})
}

// batchStep is the `batch` verb: queue every op on one explicit batch, then send it ("The explicit batch").
type batchStep struct {
	Ops              []batchOpSpec   `json:"ops"`
	As               string          `json:"as"`
	ExpectStatements json.RawMessage `json:"expectStatements"`
	ExpectMessages   json.RawMessage `json:"expectMessages"`
	ExpectAffected   json.RawMessage `json:"expectAffected"`
}

// batchAgainStep is the `batchAgain` verb: use a kept batch again — queue `op` on it, or, without `op`, send it.
type batchAgainStep struct {
	Ref string       `json:"ref"`
	Op  *batchOpSpec `json:"op"`
}

// batchOpSpec is one queued operation: exactly one key — insert, update, updateOnly, delete (fields as the steps of
// those names), or execute (`command`, a fixture command by name, and `args`).
type batchOpSpec struct {
	Insert     *struct{ Ref string } `json:"insert"`
	Update     *struct{ Ref string } `json:"update"`
	UpdateOnly *struct {
		Ref        string
		Properties []string
	} `json:"updateOnly"`
	Delete *struct {
		Ref    string
		Entity string
		Key    json.RawMessage
	} `json:"delete"`
	Execute *struct {
		Command string
		Args    map[string]json.RawMessage
	} `json:"execute"`
}

// setDisplayNameArgs and setDisplayName are the fixture command of spec/session.md "The step scripts": the registry
// command `update users set display_name = @DisplayName where id = @Id`, args Id (int64) and DisplayName (string,
// nullable).
type setDisplayNameArgs struct {
	ID          int64
	DisplayName *string
}

var setDisplayName = orm.InlineCommand[setDisplayNameArgs]("update users set display_name = @DisplayName where id = @Id")

// queue puts one op on the batch.
func (r *trackingRun) queue(i int, b *orm.Batch, op batchOpSpec) error {
	r.t.Helper()
	switch {
	case op.Insert != nil:
		entity := r.ref(i, op.Insert.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.batchInsert(b, entity)
	case op.Update != nil:
		entity := r.ref(i, op.Update.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.batchUpdate(b, entity)
	case op.UpdateOnly != nil:
		entity := r.ref(i, op.UpdateOnly.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.batchUpdateOnly(b, entity, op.UpdateOnly.Properties)
	case op.Delete != nil && op.Delete.Ref != "":
		entity := r.ref(i, op.Delete.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.batchDelEntity(b, entity)
	case op.Delete != nil:
		ops, err := trackingOpsFor(op.Delete.Entity)
		if err != nil {
			return err
		}
		key, err := decodeKey(op.Delete.Key)
		if err != nil {
			return err
		}
		return ops.batchDelKey(b, key)
	case op.Execute != nil:
		return r.queueCommand(b, op.Execute.Command, op.Execute.Args)
	}
	r.fatalf(i, "no known op in the batch operation")
	return nil
}

// queueCommand queues a fixture command by name, decoding its args through their declared types.
func (r *trackingRun) queueCommand(b *orm.Batch, name string, raw map[string]json.RawMessage) error {
	if name != "SetDisplayName" {
		return fmt.Errorf("batch_cases_test: unknown fixture command %q", name)
	}
	var args setDisplayNameArgs
	for field, value := range raw {
		switch strings.ToLower(field) {
		case "id":
			var id int64
			if err := json.Unmarshal(value, &id); err != nil {
				return err
			}
			args.ID = id
		case "displayname":
			var text *string
			if err := json.Unmarshal(value, &text); err != nil {
				return err
			}
			args.DisplayName = text
		default:
			return fmt.Errorf("SetDisplayName has no argument %q", field)
		}
	}
	return orm.BatchExecute(b, setDisplayName, args)
}

func (r *trackingRun) performBatch(i int, step *batchStep) error {
	b := r.db.Batch()
	if step.As != "" {
		r.batches[step.As] = b
	}
	for _, op := range step.Ops {
		if err := r.queue(i, b, op); err != nil {
			return err
		}
	}
	result, err := b.Send(r.ctx)
	r.result = result
	return err
}

func (r *trackingRun) performBatchAgain(i int, step *batchAgainStep) error {
	b, ok := r.batches[step.Ref]
	if !ok {
		r.fatalf(i, "no kept batch named %q (a batch step keeps its batch with `as`)", step.Ref)
	}
	if step.Op != nil {
		return r.queue(i, b, *step.Op)
	}
	_, err := b.Send(r.ctx)
	return err
}

// checkBatch judges a batch step's expectStatements, expectMessages, and expectAffected (each absent: unchecked)
// against what the dialect rendered, the session planned, and what Send returned.
func (r *trackingRun) checkBatch(i int, step *batchStep) {
	r.t.Helper()
	r.checkStatements(i, step.ExpectStatements)
	r.checkMessages(i, step.ExpectMessages)
	if len(step.ExpectAffected) == 0 {
		return
	}
	var want []int64
	if err := json.Unmarshal(step.ExpectAffected, &want); err != nil {
		r.fatalf(i, "expectAffected is not a list of counts: %s", step.ExpectAffected)
	}
	if !reflect.DeepEqual(append([]int64{}, r.result.Affected...), append([]int64{}, want...)) {
		r.fatalf(i, "Send counted %v, want %v", r.result.Affected, want)
	}
}
