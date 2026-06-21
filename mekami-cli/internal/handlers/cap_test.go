package handlers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Wolf258/mekami-cli/internal/core/model"
	"github.com/Wolf258/mekami-cli/internal/core/store"
	"github.com/Wolf258/mekami-cli/internal/format"
	"github.com/Wolf258/mekami-cli/internal/naming"
)

type capTestStore struct {
	*store.Store
}

func newCapStore(t *testing.T) *capTestStore {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "cap.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &capTestStore{Store: s}
}

// seedSymbolsAndRefs inserts a module, a package, a file, N symbols
// and M refs from each symbol to the first one. Returns the package
// id and the qualified name of the target symbol (the one being
// referenced).
func seedSymbolsAndRefs(t *testing.T, s *store.Store, nSyms, nRefsPerSym int) (pkgID, qnTarget string) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.UpsertModule("github.com/foo/bar"); err != nil {
		t.Fatalf("upsert module: %v", err)
	}
	pkgID = "github.com/foo/bar/p"
	pid, err := tx.UpsertPackage(model.Package{ModuleID: "github.com/foo/bar", PackageID: pkgID, Name: "p", Dir: "p"})
	if err != nil {
		t.Fatalf("upsert package: %v", err)
	}
	fid, err := tx.UpsertFile(model.File{Path: "p/p.go", Hash: "h", Mtime: 0, Size: 0, Lang: "go"})
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	// Target: the first symbol, named "Target".
	targetSym := model.Symbol{
		FileID:        fid,
		PackageID:     pid,
		Kind:          "func",
		Name:          "Target",
		QualifiedName: "github.com/foo/bar/p.Target",
		StartLine:     1,
		EndLine:       2,
		Exported:      true,
	}
	targetID, err := tx.InsertSymbol(targetSym)
	if err != nil {
		t.Fatalf("insert target symbol: %v", err)
	}
	qnTarget = targetSym.QualifiedName

	// Source symbols: each calls Target.
	for i := 0; i < nSyms; i++ {
		name := "Caller" + itoa(i)
		sym := model.Symbol{
			FileID:        fid,
			PackageID:     pid,
			Kind:          "func",
			Name:          name,
			QualifiedName: "github.com/foo/bar/p." + name,
			StartLine:     10 + i,
			EndLine:       12 + i,
		}
		symID, err := tx.InsertSymbol(sym)
		if err != nil {
			t.Fatalf("insert source symbol %d: %v", i, err)
		}
		for j := 0; j < nRefsPerSym; j++ {
			if err := tx.InsertRef(model.Ref{
				FromSymbol:  symID,
				ToQualified: qnTarget,
				Kind:        "call",
				Line:        11 + i,
			}); err != nil {
				t.Fatalf("insert ref %d/%d: %v", i, j, err)
			}
		}
	}
	_ = targetID
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return pkgID, qnTarget
}

// itoa is a tiny integer-to-string helper to avoid pulling strconv
// into this test file. We only need positive integers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestCapFor_NoHead(t *testing.T) {
	c := capFor(10, naming.ArgMap{}, format.KindRefs)
	if c.Truncated || c.Shown != 10 || c.Total != 10 {
		t.Fatalf("unexpected cap: %+v", c)
	}
}

func TestCapFor_HeadZeroDisables(t *testing.T) {
	c := capFor(10000, naming.ArgMap{"head": 0}, format.KindRefs)
	if c.Truncated {
		t.Fatalf("head=0 should disable truncation, got %+v", c)
	}
}

func TestCapFor_HeadLargerThanTotal(t *testing.T) {
	c := capFor(5, naming.ArgMap{"head": 100}, format.KindRefs)
	if c.Truncated {
		t.Fatalf("head>total should not truncate, got %+v", c)
	}
}

func TestCapFor_Truncated(t *testing.T) {
	c := capFor(130, naming.ArgMap{"head": 30}, format.KindRefs)
	if !c.Truncated {
		t.Fatalf("expected truncated, got %+v", c)
	}
	if c.Shown != 30 || c.Total != 130 {
		t.Fatalf("expected shown=30 total=130, got shown=%d total=%d", c.Shown, c.Total)
	}
	if c.Hint == "" {
		t.Fatalf("expected non-empty hint")
	}
}

func TestPayloadOrString_PlainWhenNotTruncated(t *testing.T) {
	items := []string{"a", "b"}
	out := payloadOrString(items, format.Cap{Total: 2, Shown: 2})
	if !reflect.DeepEqual(out, items) {
		t.Fatalf("expected plain slice when not truncated, got %#v", out)
	}
}

func TestPayloadOrString_WrappedWhenTruncated(t *testing.T) {
	items := []string{"a"}
	out := payloadOrString(items, format.Cap{Total: 100, Shown: 1, Truncated: true, Hint: "x"})
	lp, ok := out.(listPayload)
	if !ok {
		t.Fatalf("expected listPayload when truncated, got %T", out)
	}
	if lp.Cap.Total != 100 || lp.Cap.Shown != 1 {
		t.Fatalf("cap not preserved: %+v", lp.Cap)
	}
	if !reflect.DeepEqual(lp.Items, items) {
		t.Fatalf("items not preserved")
	}
}

func TestWhoCalls_Truncated(t *testing.T) {
	s := newCapStore(t)
	_, qn := seedSymbolsAndRefs(t, s.Store, 50, 1)
	args := naming.ArgMap{
		"qualified_name": qn,
		"head":           5,
	}
	out, err := WhoCalls(context.Background(), s.Store, args)
	if err != nil {
		t.Fatalf("WhoCalls: %v", err)
	}
	// After the Result refactor: extract the data side and
	// check the listPayload shape that --json would emit.
	data := ExtractData(out)
	lp, ok := data.(listPayload)
	if !ok {
		t.Fatalf("expected listPayload data, got %T", data)
	}
	if !lp.Cap.Truncated {
		t.Fatalf("expected truncated cap, got %+v", lp.Cap)
	}
	if lp.Cap.Total < 50 {
		t.Fatalf("expected total>=50, got %d", lp.Cap.Total)
	}
	if lp.Cap.Shown != 5 {
		t.Fatalf("expected shown=5, got %d", lp.Cap.Shown)
	}
	items, ok := lp.Items.([]model.RefSite)
	if !ok {
		t.Fatalf("expected []model.RefSite, got %T", lp.Items)
	}
	if len(items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(items))
	}
	// JSON shape sanity: encoding the data should include the
	// cap block. format.JSON knows about Result and unwraps
	// it for serialization (via the helpers above).
	enc, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !contains(enc, `"cap"`) || !contains(enc, `"truncated":true`) {
		t.Fatalf("json missing cap/truncated: %s", enc)
	}
}

func TestWhoCalls_NotTruncatedReturnsText(t *testing.T) {
	s := newCapStore(t)
	_, qn := seedSymbolsAndRefs(t, s.Store, 3, 1)
	out, err := WhoCalls(context.Background(), s.Store, naming.ArgMap{
		"qualified_name": qn,
		"head":           30,
	})
	if err != nil {
		t.Fatalf("WhoCalls: %v", err)
	}
	// When the result is not truncated, the handler returns the
	// formatted text on the Text side; the Data side carries
	// the slice for --json. The text must still mention the
	// qualified name and at least one of the seeded caller
	// names.
	if _, ok := ExtractData(out).(listPayload); ok {
		t.Fatalf("expected plain slice on data side when total<=head, got listPayload")
	}
	str := TextView(out)
	if str == "" {
		t.Fatalf("expected non-empty text view, got empty (out=%T)", out)
	}
	if !contains([]byte(str), qn) {
		t.Fatalf("expected output to contain %q, got: %s", qn, str)
	}
	if !contains([]byte(str), "Caller0") {
		t.Fatalf("expected output to contain seeded caller name, got: %s", str)
	}
}

func TestFindSymbol_Truncated(t *testing.T) {
	s := newCapStore(t)
	_, _ = seedSymbolsAndRefs(t, s.Store, 50, 1)
	// Search for "Caller" which matches all 50 caller symbols (and
	// not Target which is "Target" — substring match, so the
	// query string "Caller" is exact enough for the seeded names).
	out, err := FindSymbol(context.Background(), s.Store, naming.ArgMap{
		"query": "Caller",
		"head":  7,
	})
	if err != nil {
		t.Fatalf("FindSymbol: %v", err)
	}
	lp, ok := ExtractData(out).(listPayload)
	if !ok {
		t.Fatalf("expected listPayload data, got %T", ExtractData(out))
	}
	if !lp.Cap.Truncated || lp.Cap.Shown != 7 {
		t.Fatalf("cap wrong: %+v", lp.Cap)
	}
}

func TestListPackage_Truncated(t *testing.T) {
	s := newCapStore(t)
	pkgID, _ := seedSymbolsAndRefs(t, s.Store, 50, 1)
	out, err := ListPackage(context.Background(), s.Store, naming.ArgMap{
		"package_id": pkgID,
		"head":       4,
	})
	if err != nil {
		t.Fatalf("ListPackage: %v", err)
	}
	lp, ok := ExtractData(out).(listPayload)
	if !ok {
		t.Fatalf("expected listPayload data, got %T", ExtractData(out))
	}
	// 50 callers + 1 target = 51 symbols, head=4 → truncated.
	if !lp.Cap.Truncated || lp.Cap.Shown != 4 {
		t.Fatalf("cap wrong: %+v", lp.Cap)
	}
}

func TestListPackage_NotTruncated(t *testing.T) {
	s := newCapStore(t)
	pkgID, _ := seedSymbolsAndRefs(t, s.Store, 2, 1)
	out, err := ListPackage(context.Background(), s.Store, naming.ArgMap{
		"package_id": pkgID,
		"head":       30,
	})
	if err != nil {
		t.Fatalf("ListPackage: %v", err)
	}
	if _, ok := ExtractData(out).(listPayload); ok {
		t.Fatalf("expected plain slice when total<=head")
	}
}

// contains is a tiny strings.Contains shim that avoids importing
// strings in this test file (the test file does not otherwise
// need it).
func contains(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestCountFileLeaves(t *testing.T) {
	tree := &model.FileNode{Name: "root", Type: "dir", Children: []*model.FileNode{
		{Name: "a.go", Type: "file"},
		{Name: "sub", Type: "dir", Children: []*model.FileNode{
			{Name: "b.go", Type: "file"},
			{Name: "c.go", Type: "file"},
		}},
		{Name: "d.go", Type: "file"},
	}}
	if got := countFileLeaves(tree); got != 4 {
		t.Fatalf("expected 4 leaves, got %d", got)
	}
	if got := countFileLeaves(nil); got != 0 {
		t.Fatalf("expected 0 for nil, got %d", got)
	}
}

func TestTrimFileTree_KeepsScaffold(t *testing.T) {
	tree := &model.FileNode{Name: "root", Type: "dir", Children: []*model.FileNode{
		{Name: "a.go", Type: "file"},
		{Name: "sub", Type: "dir", Children: []*model.FileNode{
			{Name: "b.go", Type: "file"},
			{Name: "c.go", Type: "file"},
		}},
	}}
	trimmed := trimFileTree(tree, 2)
	if trimmed == nil {
		t.Fatal("expected non-nil result")
	}
	if got := countFileLeaves(trimmed); got != 2 {
		t.Fatalf("expected 2 leaves after trim, got %d", got)
	}
	// The "sub" directory should still appear as a scaffold even
	// though it now contains only one file.
	found := false
	for _, c := range trimmed.Children {
		if c.Name == "sub" {
			found = true
			if len(c.Children) == 0 {
				t.Fatalf("expected sub to keep a child file as scaffold")
			}
		}
	}
	if !found {
		t.Fatalf("expected sub directory to remain as scaffold")
	}
}

func TestTrimFileTree_NoOpWhenSmall(t *testing.T) {
	tree := &model.FileNode{Name: "root", Type: "dir", Children: []*model.FileNode{
		{Name: "a.go", Type: "file"},
	}}
	trimmed := trimFileTree(tree, 10)
	if trimmed != tree {
		t.Fatalf("expected same pointer when leaves < max, got %v", trimmed)
	}
}
