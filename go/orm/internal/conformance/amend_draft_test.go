package conformance

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture/models"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// `diff --amend` keeps the draft marker of a root that already carries it, with or without --draft, and a reviewed
// root stays reviewed (spec/migrations.md "Draft migrations" Clarifications, ADR-0038). The shared amend-cases
// fixture has no marker, so these cases lay the same tree out with one.

const amendWidgetSnapshotV1 = `{"object":"amend_widgets","asOfVersion":1,` +
	`"columns":[{"column":"id","type":"INTEGER","nullable":false,"key":true,"generated":true},` +
	`{"column":"name","type":"TEXT","nullable":false}],"indexes":[]}`

func amendRootAfter(t *testing.T, markedBefore, draftFlag bool) string {
	t.Helper()
	dir := t.TempDir()
	stepRefs := []migrations.RootStepRef{amendStepRef("Table/AmendWidget/V0002_AddNote")}

	emit := migrations.EmitRoot
	if markedBefore {
		emit = migrations.EmitDraftRoot
	}
	root, err := emit(amendFixturePackage, 2, stepRefs, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"V0002.go":                            root,
		"Table/AmendWidget/V0002_AddNote.go":  generatedStepStub,
		"Table/AmendWidget/V0001.schema.json": amendWidgetSnapshotV1,
	}
	for relative, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
			t.Fatal(err)
		}
	}

	set, err := amendfixture.Set()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := migrations.ExecuteDiff(context.Background(), migrations.DiffOptions{
		Set:          set,
		EntityTypes:  []reflect.Type{reflect.TypeFor[models.AmendWidget]()},
		OutDir:       dir,
		Package:      amendFixturePackage,
		Dialect:      sqlite.New(),
		DialectLabel: "sqlite",
		Name:         "AddRemark",
		Amend:        true,
		Draft:        draftFlag,
	}, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("amend: exit %d, stderr=%s", exit, stderr.String())
	}
	amended, err := os.ReadFile(filepath.Join(dir, "V0002.go"))
	if err != nil {
		t.Fatal(err)
	}
	return string(amended)
}

func TestAmend_KeepsTheDraftMarkerWithOrWithoutTheFlag(t *testing.T) {
	for _, draftFlag := range []bool{false, true} {
		if root := amendRootAfter(t, true, draftFlag); !migrations.HasDraftMarker(root) {
			t.Errorf("--draft=%v: the amended root lost its marker:\n%s", draftFlag, root)
		}
	}
}

func TestAmend_AReviewedRootStaysReviewedWithoutTheFlag(t *testing.T) {
	if root := amendRootAfter(t, false, false); migrations.HasDraftMarker(root) {
		t.Errorf("an amend must not mark a reviewed version a draft:\n%s", root)
	}
}

func TestAmend_TheFlagMarksAReviewedRoot(t *testing.T) {
	if root := amendRootAfter(t, false, true); !migrations.HasDraftMarker(root) {
		t.Errorf("--draft marks the amended root:\n%s", root)
	}
}
