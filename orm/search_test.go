package orm_test

import (
	"slices"
	"strings"
	"testing"

	"liteorm.org/dialect"
	"liteorm.org/orm"
)

func indexByName(s *orm.Schema, name string) (orm.SearchIndex, bool) {
	for _, ix := range s.SearchIndexes {
		if ix.Name == name {
			return ix, true
		}
	}
	return orm.SearchIndex{}, false
}

// --- tag-declared indexes (dedicated vec:/fts: tags; gosqlite-compatible) ---

type vecTagModel struct {
	ID        int64
	Title     string
	Embedding []float32 `vec:"dim=8;metric=cosine"`
}

func (vecTagModel) TableName() string { return "vec_t" }

func TestSearchTags_Vector(t *testing.T) {
	s, err := orm.SchemaOf[vecTagModel]()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.SearchIndexes) != 1 {
		t.Fatalf("want 1 index, got %d", len(s.SearchIndexes))
	}
	ix := s.SearchIndexes[0]
	if ix.Kind != dialect.SearchVector {
		t.Errorf("kind = %v, want SearchVector", ix.Kind)
	}
	if ix.Name != "vec_t_vec" {
		t.Errorf("name = %q, want vec_t_vec", ix.Name)
	}
	if len(ix.Fields) != 1 || ix.Fields[0] != "Embedding" {
		t.Errorf("fields = %v, want [Embedding]", ix.Fields)
	}
	if ix.Dim != 8 {
		t.Errorf("dim = %d, want 8", ix.Dim)
	}
	if ix.Metric != orm.Cosine {
		t.Errorf("metric = %v, want Cosine", ix.Metric)
	}
}

// gosqlite used the `fts5:` tag with `+`-joined tokenizers; liteorm accepts that
// form unchanged so a model ports across.
type fts5AliasModel struct {
	ID   int64
	Body string `fts5:"tokenize=porter+unicode61"`
}

func (fts5AliasModel) TableName() string { return "fts5_alias" }

func TestSearchTags_FTS5Alias(t *testing.T) {
	s, err := orm.SchemaOf[fts5AliasModel]()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.SearchIndexes) != 1 || s.SearchIndexes[0].Kind != dialect.SearchFullText {
		t.Fatalf("want 1 full-text index, got %v", s.SearchIndexes)
	}
	if got := s.SearchIndexes[0].Tokenizer; got != "porter unicode61" {
		t.Errorf("tokenizer = %q, want %q (the + should normalize to a space)", got, "porter unicode61")
	}
}

type ftsTagModel struct {
	ID   int64
	Body string `fts:"tokenize=porter unicode61;prefix=2 3 4"`
}

func (ftsTagModel) TableName() string { return "fts_tag" }

func TestSearchTags_FullText(t *testing.T) {
	s, err := orm.SchemaOf[ftsTagModel]()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.SearchIndexes) != 1 {
		t.Fatalf("want 1 index, got %d", len(s.SearchIndexes))
	}
	ix := s.SearchIndexes[0]
	if ix.Kind != dialect.SearchFullText || ix.Name != "fts_tag_fts" {
		t.Errorf("got kind=%v name=%q", ix.Kind, ix.Name)
	}
	if ix.Tokenizer != "porter unicode61" {
		t.Errorf("tokenizer = %q, want %q", ix.Tokenizer, "porter unicode61")
	}
	if want := []int{2, 3, 4}; !slices.Equal(ix.Prefix, want) {
		t.Errorf("prefix = %v, want %v", ix.Prefix, want)
	}
}

func TestSearchTags_VectorMetricDefaultsCosine(t *testing.T) {
	type m struct {
		ID  int64
		Emb []float32 `vec:"dim=4"`
	}
	s, err := orm.SchemaOf[m]()
	if err != nil {
		t.Fatal(err)
	}
	if s.SearchIndexes[0].Metric != orm.Cosine {
		t.Errorf("default metric = %v, want Cosine", s.SearchIndexes[0].Metric)
	}
}

// --- method-declared indexes (full power: multi-field FTS, weights, sync) ---

type methodModel struct {
	ID        int64
	Title     string
	Body      string
	Embedding []float32 `orm:"-"` // sidecar-only; not a base column
}

func (methodModel) TableName() string { return "mm" }

func (methodModel) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{
		orm.FullText("Title", "Body").WithTokenizer("porter unicode61").WithWeights(2, 1),
		orm.Vector("Embedding", 8).WithMetric(orm.Cosine).WithSync(orm.SyncHooks),
	}
}

func TestSearchMethod(t *testing.T) {
	s, err := orm.SchemaOf[methodModel]()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.SearchIndexes) != 2 {
		t.Fatalf("want 2 indexes, got %d", len(s.SearchIndexes))
	}
	fts, ok := indexByName(s, "mm_fts")
	if !ok {
		t.Fatal("missing mm_fts")
	}
	if !slices.Equal(fts.Fields, []string{"Title", "Body"}) {
		t.Errorf("fts fields = %v", fts.Fields)
	}
	if !slices.Equal(fts.Weights, []float64{2, 1}) {
		t.Errorf("fts weights = %v", fts.Weights)
	}
	vec, ok := indexByName(s, "mm_vec")
	if !ok {
		t.Fatal("missing mm_vec")
	}
	if vec.Sync != orm.SyncHooks {
		t.Errorf("vec sync = %v, want SyncHooks", vec.Sync)
	}
}

// --- merge: method wins on a sidecar-name collision ---

type mergeModel struct {
	ID        int64
	Body      string    `fts:""`
	Embedding []float32 `vec:"dim=4"` // default name mm2_vec, dim 4
}

func (mergeModel) TableName() string { return "mm2" }

func (mergeModel) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{orm.Vector("Embedding", 16).Named("mm2_vec")} // overrides the tag's dim 4
}

func TestSearchMerge_MethodWins(t *testing.T) {
	s, err := orm.SchemaOf[mergeModel]()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.SearchIndexes) != 2 {
		t.Fatalf("want 2 indexes (fts from tag + vec from method), got %d", len(s.SearchIndexes))
	}
	if _, ok := indexByName(s, "mm2_fts"); !ok {
		t.Error("tag-declared mm2_fts should survive the merge")
	}
	vec, ok := indexByName(s, "mm2_vec")
	if !ok {
		t.Fatal("missing mm2_vec")
	}
	if vec.Dim != 16 {
		t.Errorf("dim = %d, want 16 (method should win over the tag's 4)", vec.Dim)
	}
}

func TestSearch_NoIndexes(t *testing.T) {
	type plain struct {
		ID   int64
		Name string
	}
	s, err := orm.SchemaOf[plain]()
	if err != nil {
		t.Fatal(err)
	}
	if s.SearchIndexes != nil {
		t.Errorf("want nil SearchIndexes, got %v", s.SearchIndexes)
	}
}

// --- validation failures ---

func TestSearchValidation(t *testing.T) {
	t.Run("missing dim", func(t *testing.T) {
		type m struct {
			ID  int64
			Emb []float32 `vec:""`
		}
		mustSchemaErr(t, func() (*orm.Schema, error) { return orm.SchemaOf[m]() }, "dim")
	})
	t.Run("unknown metric", func(t *testing.T) {
		type m struct {
			ID  int64
			Emb []float32 `vec:"dim=4;metric=bogus"`
		}
		mustSchemaErr(t, func() (*orm.Schema, error) { return orm.SchemaOf[m]() }, "metric")
	})
	t.Run("vec and fts on one field", func(t *testing.T) {
		type m struct {
			ID  int64
			Emb []float32 `vec:"dim=4" fts:""`
		}
		mustSchemaErr(t, func() (*orm.Schema, error) { return orm.SchemaOf[m]() }, "both")
	})
	t.Run("two unnamed fts tags collide", func(t *testing.T) {
		type m struct {
			ID    int64
			Title string `fts:""`
			Body  string `fts:""`
		}
		mustSchemaErr(t, func() (*orm.Schema, error) { return orm.SchemaOf[m]() }, "explicit name")
	})
}

type badFieldModel struct {
	ID   int64
	Body string
}

func (badFieldModel) TableName() string { return "bad_field" }
func (badFieldModel) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{orm.FullText("Nope")}
}

type badWeightsModel struct {
	ID    int64
	Title string
	Body  string
}

func (badWeightsModel) TableName() string { return "bad_weights" }
func (badWeightsModel) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{orm.FullText("Title", "Body").WithWeights(1, 2, 3)}
}

type badVecFieldsModel struct {
	ID int64
	A  []float32 `orm:"-"`
	B  []float32 `orm:"-"`
}

func (badVecFieldsModel) TableName() string { return "bad_vec" }
func (badVecFieldsModel) SearchIndexes() []orm.SearchIndex {
	return []orm.SearchIndex{{Kind: dialect.SearchVector, Fields: []string{"A", "B"}, Dim: 4}}
}

type strPKFullText struct {
	Code  string `orm:"code,pk"`
	Title string `fts:""`
}

func (strPKFullText) TableName() string { return "str_pk_fts" }

func TestSearch_FullTextRequiresInt64PK(t *testing.T) {
	// A string-PK model with full-text must fail when its spec is resolved (at
	// AutoMigrate), not silently provision a sidecar that breaks on first write.
	_, err := orm.SearchSpecs[strPKFullText]()
	if err == nil {
		t.Fatal("want an error for string-PK full-text, got nil")
	}
	if !strings.Contains(err.Error(), "integer primary key") {
		t.Errorf("error %q should explain the integer-PK requirement", err.Error())
	}
}

func TestSearchMethodValidation(t *testing.T) {
	mustSchemaErr(t, func() (*orm.Schema, error) { return orm.SchemaOf[badFieldModel]() }, "unknown field")
	mustSchemaErr(t, func() (*orm.Schema, error) { return orm.SchemaOf[badWeightsModel]() }, "weights")
	mustSchemaErr(t, func() (*orm.Schema, error) { return orm.SchemaOf[badVecFieldsModel]() }, "exactly one field")
}

// --- helpers ---

func mustSchemaErr(t *testing.T, fetch func() (*orm.Schema, error), want string) {
	t.Helper()
	_, err := fetch()
	if err == nil {
		t.Fatalf("want error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}
