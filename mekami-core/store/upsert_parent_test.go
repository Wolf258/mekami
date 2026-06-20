package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Wolf258/mekami-api/api/v1"
	"github.com/Wolf258/mekami-core/model"
	"github.com/Wolf258/mekami-core/store"
)

func int64Ptr(v int64) *int64 { return &v }

// TestUpsert_SymbolRoundTrip verifies that a freshly-inserted symbol
// with ParentSymbol=nil round-trips correctly through Scan, and that
// a symbol with ParentSymbol=ptr(N) round-trips as ptr(N) (not nil
// and not 0). The *int64 type makes the distinction explicit at the
// API level even if storage semantics don't exercise the difference.
func TestUpsert_SymbolRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.UpsertModule("m"); err != nil {
		t.Fatal(err)
	}
	pid, err := tx.UpsertPackage(model.Package{ModuleID: "m", PackageID: "m/p", Name: "p", Dir: "."})
	if err != nil {
		t.Fatal(err)
	}
	fid, err := tx.UpsertFile(model.File{Path: "a.go", Hash: "h", Mtime: 0, Size: 0, Lang: "go"})
	if err != nil {
		t.Fatal(err)
	}
	// Insert a top-level type. ParentSymbol=nil must stay nil.
	if _, err := tx.InsertSymbol(model.Symbol{
		FileID:        fid,
		PackageID:     pid,
		Kind:          string(api.KindType),
		Name:          "T",
		QualifiedName: "p.T",
		StartLine:     1,
		EndLine:       2,
		Exported:      true,
		ParentSymbol:  nil,
	}); err != nil {
		t.Fatal(err)
	}
	// Insert a child with an explicit parent. The pointer must survive.
	parentID, err := tx.InsertSymbol(model.Symbol{
		FileID:        fid,
		PackageID:     pid,
		Kind:          string(api.KindType),
		Name:          "U",
		QualifiedName: "p.U",
		StartLine:     5,
		EndLine:       6,
		Exported:      true,
		ParentSymbol:  nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.InsertSymbol(model.Symbol{
		FileID:        fid,
		PackageID:     pid,
		Kind:          string(api.KindMethod),
		Name:          "M",
		QualifiedName: "p.U.M",
		StartLine:     7,
		EndLine:       8,
		Exported:      true,
		ParentSymbol:  int64Ptr(parentID),
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Top-level: nil stays nil.
	gotT := mustFindSymbol(t, s, ctx, "p.T")
	if gotT.ParentSymbol != nil {
		t.Errorf("p.T: expected ParentSymbol=nil, got %d", *gotT.ParentSymbol)
	}
	// Child: pointer survives round-trip.
	gotM := mustFindSymbol(t, s, ctx, "p.U.M")
	if gotM.ParentSymbol == nil {
		t.Fatal("p.U.M: expected ParentSymbol to be non-nil")
	}
	if *gotM.ParentSymbol != parentID {
		t.Errorf("p.U.M: expected ParentSymbol=%d, got %d", parentID, *gotM.ParentSymbol)
	}
}

// mustFindSymbol fetches a single symbol by qualified name without
// going through the queries package (which would cycle through store
// in a test). Direct SQL keeps this test self-contained.
func mustFindSymbol(t *testing.T, s *store.Store, ctx context.Context, qn string) model.Symbol {
	t.Helper()
	row := s.DB().QueryRowContext(ctx,
		`SELECT id,file_id,package_id,kind,name,qualified_name,
		        start_line,end_line,exported,COALESCE(signature,''),parent_symbol
		 FROM symbols WHERE qualified_name = ?`, qn)
	var sym model.Symbol
	var exported int
	var parent *int64
	if err := row.Scan(&sym.ID, &sym.FileID, &sym.PackageID, &sym.Kind, &sym.Name, &sym.QualifiedName,
		&sym.StartLine, &sym.EndLine, &exported, &sym.Signature, &parent); err != nil {
		t.Fatal(err)
	}
	sym.Exported = exported != 0
	sym.ParentSymbol = parent
	return sym
}
