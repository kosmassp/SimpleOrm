package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// trackingCaseSpec is the conformance/tracking-cases/*.json shape (spec/session.md
// "Conformance: tracking-cases/", Level 3 M1): a session mode and a step script
// replayed against named instances. conformance/flush-cases/*.json (spec/unit-of-work.md,
// Level 3 M2) is the same shape with more verbs; one step engine replays both.
type trackingCaseSpec struct {
	Name     string         `json:"name"`
	Tracking string         `json:"tracking"`
	Steps    []trackingStep `json:"steps"`
}

// trackingStep is one step: exactly one verb key is set, plus an optional
// expectError. Property names are spelled as the reference declares them
// (PascalCase, dotted owned members); the runner resolves them case-insensitively
// against the Go map ("Id" ↔ ID), the way the other runners map column names.
type trackingStep struct {
	Get *struct {
		Entity string          `json:"entity"`
		Key    json.RawMessage `json:"key"`
		KeyOf  string          `json:"keyOf"`
		As     string          `json:"as"`
	} `json:"get"`
	New *struct {
		Entity string                     `json:"entity"`
		As     string                     `json:"as"`
		Values map[string]json.RawMessage `json:"values"`
		// Navigations assigns earlier refs (or lists of refs) to navigation fields (flush-cases).
		Navigations map[string]json.RawMessage `json:"navigations"`
	} `json:"new"`
	Set *struct {
		Ref      string          `json:"ref"`
		Property string          `json:"property"`
		Value    json.RawMessage `json:"value"`
	} `json:"set"`
	Insert *struct {
		Ref string `json:"ref"`
	} `json:"insert"`
	Update *struct {
		Ref           string          `json:"ref"`
		ExpectWritten json.RawMessage `json:"expectWritten"`
	} `json:"update"`
	UpdateOnly *struct {
		Ref        string   `json:"ref"`
		Properties []string `json:"properties"`
	} `json:"updateOnly"`
	Delete *struct {
		Ref    string          `json:"ref"`
		Entity string          `json:"entity"`
		Key    json.RawMessage `json:"key"`
	} `json:"delete"`
	Refresh *struct {
		Ref string `json:"ref"`
	} `json:"refresh"`
	Load *struct {
		Ref        string `json:"ref"`
		Navigation string `json:"navigation"`
	} `json:"load"`
	Persist *struct {
		Ref string `json:"ref"`
	} `json:"persist"`
	Remove *struct {
		Ref string `json:"ref"`
	} `json:"remove"`
	// Detach and Merge are the Level 3 M3 verbs (spec/unit-of-work.md "Detach and merge"): detach forgets
	// the instance; merge copies a detached copy onto the session's instance and names what it returns.
	Detach *struct {
		Ref string `json:"ref"`
	} `json:"detach"`
	Merge *struct {
		Ref string `json:"ref"`
		As  string `json:"as"`
	} `json:"merge"`
	// Flush replays Db.Flush; expectStatements is the ordered list the dialect was asked to render
	// (absent: unchecked, [] : none), expectSummary the returned counts.
	Flush *struct {
		ExpectStatements json.RawMessage `json:"expectStatements"`
		ExpectSummary    *struct {
			Inserted int `json:"inserted"`
			Updated  int `json:"updated"`
			Deleted  int `json:"deleted"`
		} `json:"expectSummary"`
	} `json:"flush"`
	Begin    *struct{} `json:"begin"`
	Commit   *struct{} `json:"commit"`
	Rollback *struct{} `json:"rollback"`
	Expect   *struct {
		Pending   *bool    `json:"pending"`
		SameValue []string `json:"sameValue"`
		Count     *struct {
			Entity string `json:"entity"`
			Equals int    `json:"equals"`
		} `json:"count"`
		Same    []string `json:"same"`
		NotSame []string `json:"notSame"`
		Dirty   *struct {
			Ref        string   `json:"ref"`
			Properties []string `json:"properties"`
		} `json:"dirty"`
		Value *struct {
			Ref      string          `json:"ref"`
			Property string          `json:"property"`
			Equals   json.RawMessage `json:"equals"`
		} `json:"value"`
		Tracked *struct {
			Ref string `json:"ref"`
			Is  bool   `json:"is"`
		} `json:"tracked"`
	} `json:"expect"`
	ExpectError string `json:"expectError"`
}

// trackingOps is one entity type's session surface, type-erased over `any`
// (instances are *T), dispatched once in trackingOpsFor like crudOps.
type trackingOps struct {
	typ        reflect.Type
	new        func() any
	get        func(ctx context.Context, db *orm.Db, key any) (any, error)
	insert     func(ctx context.Context, db *orm.Db, entity any) error
	update     func(ctx context.Context, db *orm.Db, entity any) error
	updateOnly func(ctx context.Context, db *orm.Db, entity any, properties []string) error
	delKey     func(ctx context.Context, db *orm.Db, key any) error
	delEntity  func(ctx context.Context, db *orm.Db, entity any) error
	refresh    func(ctx context.Context, db *orm.Db, entity any) error
	load       func(ctx context.Context, db *orm.Db, entity any, navigation string) error
	changes    func(db *orm.Db, entity any) ([]string, error)
	persist    func(db *orm.Db, entity any) error
	remove     func(db *orm.Db, entity any) error
	detach     func(db *orm.Db, entity any) error
	merge      func(ctx context.Context, db *orm.Db, entity any) (any, error)
}

func trackingOpsOf[T any]() trackingOps {
	return trackingOps{
		typ: reflect.TypeFor[T](),
		new: func() any { return new(T) },
		get: func(ctx context.Context, db *orm.Db, key any) (any, error) {
			return orm.Get[*T](ctx, db, key) // the pointer shape: the session's own instance
		},
		insert: func(ctx context.Context, db *orm.Db, entity any) error {
			return orm.Insert(ctx, db, entity.(*T))
		},
		update: func(ctx context.Context, db *orm.Db, entity any) error {
			return orm.Update(ctx, db, entity.(*T))
		},
		updateOnly: func(ctx context.Context, db *orm.Db, entity any, properties []string) error {
			return orm.UpdateOnly(ctx, db, entity.(*T), properties...)
		},
		delKey: func(ctx context.Context, db *orm.Db, key any) error {
			return orm.Delete[T](ctx, db, key)
		},
		delEntity: func(ctx context.Context, db *orm.Db, entity any) error {
			return orm.DeleteEntity(ctx, db, entity.(*T))
		},
		refresh: func(ctx context.Context, db *orm.Db, entity any) error {
			return orm.Refresh(ctx, db, entity.(*T))
		},
		load: func(ctx context.Context, db *orm.Db, entity any, navigation string) error {
			return orm.Load(ctx, db, entity.(*T), navigation)
		},
		changes: func(db *orm.Db, entity any) ([]string, error) {
			return orm.ChangesOf(db, entity.(*T))
		},
		persist: func(db *orm.Db, entity any) error { return orm.Persist(db, entity.(*T)) },
		remove:  func(db *orm.Db, entity any) error { return orm.Remove(db, entity.(*T)) },
		detach:  func(db *orm.Db, entity any) error { return orm.Detach(db, entity.(*T)) },
		merge: func(ctx context.Context, db *orm.Db, entity any) (any, error) {
			return orm.Merge(ctx, db, entity.(*T))
		},
	}
}

func trackingOpsFor(entity string) (trackingOps, error) {
	switch entity {
	case "User":
		return trackingOpsOf[sample.User](), nil
	case "Transaction":
		return trackingOpsOf[sample.Transaction](), nil
	case "UserProfile":
		return trackingOpsOf[sample.UserProfile](), nil
	case "Role":
		return trackingOpsOf[sample.Role](), nil
	case "UserRole":
		return trackingOpsOf[sample.UserRole](), nil
	case "TransactionDetail":
		return trackingOpsOf[sample.TransactionDetail](), nil
	}
	return trackingOps{}, fmt.Errorf("tracking_cases_test: unknown entity %q", entity)
}

// trackingOpsForInstance dispatches on a captured instance's runtime type.
func trackingOpsForInstance(instance any) (trackingOps, error) {
	return trackingOpsFor(reflect.TypeOf(instance).Elem().Name())
}

// TestTrackingCases_BehaveAsSpecified is the conformance/tracking-cases/ runner
// (Level 3 M1): the seeded fixture database of the other runners, opened in the
// case's mode with a recording dialect, then the step script.
func TestTrackingCases_BehaveAsSpecified(t *testing.T) {
	runStepCases(t, "tracking-cases")
}

// runStepCases replays every case file of a step-script folder (tracking-cases,
// flush-cases): one engine, so the two folders cannot drift apart.
func runStepCases(t *testing.T, folder string) {
	for _, name := range testsupport.ConformanceCases(t, folder) {
		t.Run(name, func(t *testing.T) {
			var spec trackingCaseSpec
			if err := json.Unmarshal(testsupport.ReadConformance(t, folder, name), &spec); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}

			ctx := context.Background()
			path := testsupport.TempDatabase(t)
			buildCaseFixture(t, ctx, path)

			mode := orm.TrackingUntracked
			switch spec.Tracking {
			case "tracked":
				mode = orm.TrackingTracked
			case "untracked", "":
			default:
				t.Fatalf("%s: unknown tracking mode %q", spec.Name, spec.Tracking)
			}
			dialect := testsupport.NewRecordingDialect()
			db, err := orm.Open(ctx, path, orm.Options{Dialect: dialect, Tracking: mode, Clock: testsupport.FixedClock})
			if err != nil {
				t.Fatalf("%s: open: %v", spec.Name, err)
			}
			defer db.Close()

			run := &trackingRun{t: t, ctx: ctx, db: db, dialect: dialect, path: path, name: spec.Name, refs: map[string]any{}}
			for i, step := range spec.Steps {
				run.step(i, step)
			}
		})
	}
}

// trackingRun is one case's state: the named instances and the recording dialect.
type trackingRun struct {
	t       *testing.T
	ctx     context.Context
	db      *orm.Db
	dialect *testsupport.RecordingDialect
	path    string // the database file, for a row count read outside the session
	tx      *orm.Tx
	summary orm.FlushSummary
	name    string
	refs    map[string]any
}

func (r *trackingRun) fatalf(i int, format string, args ...any) {
	r.t.Helper()
	r.t.Fatalf("%s: step %d: %s", r.name, i+1, fmt.Sprintf(format, args...))
}

// step replays one step and judges its error against expectError.
func (r *trackingRun) step(i int, step trackingStep) {
	r.t.Helper()
	r.dialect.Reset()
	err := r.perform(i, step)
	if step.ExpectError != "" {
		if core.CodeOf(err) != step.ExpectError {
			r.fatalf(i, "expected error %s, got %v", step.ExpectError, err)
		}
		return
	}
	if err != nil {
		r.fatalf(i, "unexpected error: %v", err)
	}
	if step.Update != nil {
		r.checkWritten(i, step.Update.ExpectWritten)
	}
	if step.Flush != nil {
		r.checkFlush(i, step)
	}
}

func (r *trackingRun) perform(i int, step trackingStep) error {
	switch {
	case step.Get != nil:
		return r.performGet(i, step)
	case step.New != nil:
		ops, err := trackingOpsFor(step.New.Entity)
		if err != nil {
			return err
		}
		entity := ops.new()
		for property, raw := range step.New.Values {
			if err := r.setValue(entity, property, raw); err != nil {
				return err
			}
		}
		for navigation, raw := range step.New.Navigations {
			if err := r.setNavigation(i, entity, navigation, raw); err != nil {
				return err
			}
		}
		r.refs[step.New.As] = entity
		return nil
	case step.Set != nil:
		return r.setValue(r.ref(i, step.Set.Ref), step.Set.Property, step.Set.Value)
	case step.Insert != nil:
		entity := r.ref(i, step.Insert.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.insert(r.ctx, r.db, entity)
	case step.Update != nil:
		entity := r.ref(i, step.Update.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.update(r.ctx, r.db, entity)
	case step.UpdateOnly != nil:
		entity := r.ref(i, step.UpdateOnly.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.updateOnly(r.ctx, r.db, entity, step.UpdateOnly.Properties)
	case step.Delete != nil:
		return r.performDelete(i, step)
	case step.Refresh != nil:
		entity := r.ref(i, step.Refresh.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.refresh(r.ctx, r.db, entity)
	case step.Load != nil:
		entity := r.ref(i, step.Load.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.load(r.ctx, r.db, entity, step.Load.Navigation)
	case step.Persist != nil:
		entity := r.ref(i, step.Persist.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.persist(r.db, entity)
	case step.Remove != nil:
		entity := r.ref(i, step.Remove.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.remove(r.db, entity)
	case step.Detach != nil:
		entity := r.ref(i, step.Detach.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.detach(r.db, entity)
	case step.Merge != nil:
		entity := r.ref(i, step.Merge.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		merged, err := ops.merge(r.ctx, r.db, entity)
		if err != nil {
			return err
		}
		r.refs[step.Merge.As] = merged
		return nil
	case step.Flush != nil:
		summary, err := r.db.Flush(r.ctx)
		r.summary = summary
		return err
	case step.Begin != nil:
		tx, err := r.db.Begin(r.ctx)
		r.tx = tx
		return err
	case step.Commit != nil:
		return r.tx.Commit(r.ctx)
	case step.Rollback != nil:
		return r.tx.Rollback(r.ctx)
	case step.Expect != nil:
		return r.expect(i, step)
	}
	r.fatalf(i, "no known verb in the step")
	return nil
}

func (r *trackingRun) performGet(i int, step trackingStep) error {
	ops, err := trackingOpsFor(step.Get.Entity)
	if err != nil {
		return err
	}
	var key any
	if step.Get.KeyOf != "" {
		key, err = r.keyOf(ops, r.ref(i, step.Get.KeyOf))
		if err != nil {
			return err
		}
	} else if key, err = decodeKey(step.Get.Key); err != nil {
		return err
	}
	entity, err := ops.get(r.ctx, r.db, key)
	if err != nil {
		return err
	}
	r.refs[step.Get.As] = entity
	return nil
}

func (r *trackingRun) performDelete(i int, step trackingStep) error {
	if step.Delete.Ref != "" {
		entity := r.ref(i, step.Delete.Ref)
		ops, err := trackingOpsForInstance(entity)
		if err != nil {
			return err
		}
		return ops.delEntity(r.ctx, r.db, entity)
	}
	ops, err := trackingOpsFor(step.Delete.Entity)
	if err != nil {
		return err
	}
	key, err := decodeKey(step.Delete.Key)
	if err != nil {
		return err
	}
	return ops.delKey(r.ctx, r.db, key)
}

// keyOf is an instance's key in the shape Get takes: bare for a single part, a tuple otherwise.
func (r *trackingRun) keyOf(ops trackingOps, entity any) (any, error) {
	m, err := r.db.Maps().Load(ops.typ)
	if err != nil {
		return nil, err
	}
	values, err := m.KeyValues(entity)
	if err != nil {
		return nil, err
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return values, nil
}

// decodeKey reads a step's "key": a number is a one-part key, an array a
// composite key's parts in key order.
func decodeKey(raw json.RawMessage) (any, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	switch v := decoded.(type) {
	case float64:
		return int64(v), nil
	case []any:
		parts := make([]any, len(v))
		for i, p := range v {
			number, ok := p.(float64)
			if !ok {
				return nil, fmt.Errorf("key part %d is not a number", i)
			}
			parts[i] = int64(number)
		}
		return parts, nil
	}
	return decoded, nil
}

// propertyOf resolves a reference-spelled property name ("Id", "Address.City")
// against the Go map, case-insensitively.
func propertyOf(m *core.EntityMap, name string) (*core.PropertyMap, bool) {
	for _, p := range m.Properties {
		if strings.EqualFold(p.PropertyName, name) {
			return p, true
		}
	}
	return nil, false
}

func (r *trackingRun) setValue(entity any, property string, raw json.RawMessage) error {
	m, err := r.db.Maps().Load(reflect.TypeOf(entity).Elem())
	if err != nil {
		return err
	}
	p, ok := propertyOf(m, property)
	if !ok {
		return fmt.Errorf("no mapped property %q on %s", property, m.EntityName())
	}
	value, err := decodeConformanceValue(raw, p)
	if err != nil {
		return err
	}
	return p.Set(entity, value)
}

var refPattern = regexp.MustCompile(`^(\w+)(?:\.(\w+))?(?:\[(\d+)\])?$`)

// ref resolves a name to its instance; the name may hop one navigation
// ("t.User") or index a loaded collection ("a.Transactions[0]").
func (r *trackingRun) ref(i int, expression string) any {
	r.t.Helper()
	match := refPattern.FindStringSubmatch(expression)
	if match == nil {
		r.fatalf(i, "malformed ref %q", expression)
	}
	base, ok := r.refs[match[1]]
	if !ok {
		r.fatalf(i, "no instance named %q", match[1])
	}
	if match[2] == "" {
		return base
	}
	field := reflect.ValueOf(base).Elem().FieldByName(match[2])
	if !field.IsValid() {
		r.fatalf(i, "%s has no field %q", reflect.TypeOf(base).Elem().Name(), match[2])
	}
	if match[3] != "" {
		index, _ := strconv.Atoi(match[3])
		if field.Kind() != reflect.Slice || index >= field.Len() {
			r.fatalf(i, "%s is not a loaded collection with an element %d", expression, index)
		}
		field = field.Index(index)
	}
	if field.Kind() != reflect.Pointer {
		r.fatalf(i, "%s is not a pointer; identity is a pointer property", expression)
	}
	return field.Interface()
}

func (r *trackingRun) expect(i int, step trackingStep) error {
	r.t.Helper()
	e := step.Expect
	if len(e.Same) == 2 {
		if r.ref(i, e.Same[0]) != r.ref(i, e.Same[1]) {
			r.fatalf(i, "expected %s and %s to be the same instance", e.Same[0], e.Same[1])
		}
	}
	if len(e.NotSame) == 2 {
		if r.ref(i, e.NotSame[0]) == r.ref(i, e.NotSame[1]) {
			r.fatalf(i, "expected %s and %s to be different instances", e.NotSame[0], e.NotSame[1])
		}
	}
	if e.Tracked != nil {
		if got := r.db.IsTracked(r.ref(i, e.Tracked.Ref)); got != e.Tracked.Is {
			r.fatalf(i, "IsTracked(%s) = %v, want %v", e.Tracked.Ref, got, e.Tracked.Is)
		}
	}
	if e.Dirty != nil {
		if err := r.expectDirty(i, e.Dirty.Ref, e.Dirty.Properties); err != nil {
			return err
		}
	}
	if e.Value != nil {
		r.expectValue(i, e.Value.Ref, e.Value.Property, e.Value.Equals)
	}
	if e.Pending != nil {
		if got := r.db.HasPendingWork(); got != *e.Pending {
			r.fatalf(i, "HasPendingWork() = %v, want %v", got, *e.Pending)
		}
	}
	if len(e.SameValue) == 2 {
		r.expectSameValue(i, e.SameValue[0], e.SameValue[1])
	}
	if e.Count != nil {
		r.expectCount(i, e.Count.Entity, e.Count.Equals)
	}
	return nil
}

// expectDirty compares ChangesOf with the listed properties; ChangesOf's own
// error (UOW-001) is returned for the step's expectError to judge.
func (r *trackingRun) expectDirty(i int, ref string, want []string) error {
	r.t.Helper()
	entity := r.ref(i, ref)
	ops, err := trackingOpsForInstance(entity)
	if err != nil {
		r.fatalf(i, "%v", err)
	}
	got, err := ops.changes(r.db, entity)
	if err != nil {
		return err
	}
	if !sameNames(got, want) {
		r.fatalf(i, "ChangesOf(%s) = %v, want %v", ref, got, want)
	}
	return nil
}

func sameNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !strings.EqualFold(got[i], want[i]) {
			return false
		}
	}
	return true
}

func (r *trackingRun) expectValue(i int, ref, property string, raw json.RawMessage) {
	r.t.Helper()
	entity := r.ref(i, ref)
	m, err := r.db.Maps().Load(reflect.TypeOf(entity).Elem())
	if err != nil {
		r.fatalf(i, "%v", err)
	}
	for _, owner := range m.OwnedTypes {
		if owner.PropertyName() == property {
			// An owned navigation named whole: null, or {} for "an owned object exists" (spec/session.md "The step scripts").
			gotNil := reflect.ValueOf(entity).Elem().FieldByIndex(owner.Index).IsNil()
			wantNil := strings.TrimSpace(string(raw)) == "null"
			if gotNil != wantNil {
				r.fatalf(i, "%s.%s nil = %v, want nil = %v", ref, property, gotNil, wantNil)
			}
			return
		}
	}
	p, ok := propertyOf(m, property)
	if !ok {
		r.fatalf(i, "no mapped property %q on %s", property, m.EntityName())
	}
	want, err := decodeConformanceValue(raw, p)
	if err != nil {
		r.fatalf(i, "%v", err)
	}
	gotText, gotNull := encodeForCompare(p.Get(entity), p.ColumnType)
	wantText, wantNull := encodeForCompare(want, p.ColumnType)
	if gotNull != wantNull || gotText != wantText {
		r.fatalf(i, "%s.%s = %q (null=%v), want %q (null=%v)", ref, property, gotText, gotNull, wantText, wantNull)
	}
}

// checkWritten judges an update step's expectWritten against what the dialect
// was asked to render: "all" is one full-row update, [] none at all, a list one
// column-list update of exactly those properties.
func (r *trackingRun) checkWritten(i int, raw json.RawMessage) {
	r.t.Helper()
	if len(raw) == 0 {
		return
	}
	var all string
	if json.Unmarshal(raw, &all) == nil {
		if all != "all" {
			r.fatalf(i, "expectWritten %q is not \"all\" or a list", all)
		}
		if updates := r.dialect.Updates(); len(updates) != 1 || !updates[0].All {
			r.fatalf(i, "expected one full-row update, the dialect rendered %+v", updates)
		}
		return
	}
	var want []string
	if err := json.Unmarshal(raw, &want); err != nil {
		r.fatalf(i, "expectWritten is neither \"all\" nor a list: %s", raw)
	}
	updates := r.dialect.Updates()
	if len(want) == 0 {
		if len(updates) != 0 {
			r.fatalf(i, "expected no update statement, the dialect rendered %+v", updates)
		}
		return
	}
	if len(updates) != 1 || updates[0].All || !sameNames(updates[0].Properties, want) {
		r.fatalf(i, "expected one column-list update of %v, the dialect rendered %+v", want, updates)
	}
}
