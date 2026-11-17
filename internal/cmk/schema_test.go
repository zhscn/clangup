package cmk

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func yamlFieldNames(typ reflect.Type) []string {
	var names []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("yaml")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func schemaPropertyNames(t *testing.T, obj map[string]any) []string {
	t.Helper()
	props, ok := obj["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties object in %v", obj)
	}
	return slices.Sorted(maps.Keys(props))
}

func TestJSONSchemaMatchesConfigTypes(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(cmkJSONSchema, &root); err != nil {
		t.Fatalf("invalid embedded JSON schema: %v", err)
	}
	if root["$id"] != SchemaURL {
		t.Fatalf("schema $id = %v, want %s", root["$id"], SchemaURL)
	}
	defs, ok := root["definitions"].(map[string]any)
	if !ok {
		t.Fatal("missing definitions in schema")
	}
	props := root["properties"].(map[string]any)

	cases := []struct {
		name      string
		goType    reflect.Type
		schemaObj map[string]any
	}{
		{"Config", reflect.TypeFor[Config](), root},
		{"ConfigureCfg", reflect.TypeFor[ConfigureCfg](), props["cmake"].(map[string]any)},
		{"InstallCfg", reflect.TypeFor[InstallCfg](), props["install"].(map[string]any)},
		{"FmtCfg", reflect.TypeFor[FmtCfg](), props["format"].(map[string]any)},
		{"LintCfg", reflect.TypeFor[LintCfg](), props["lint"].(map[string]any)},
		{"PresetCfg", reflect.TypeFor[PresetCfg](), defs["preset"].(map[string]any)},
		{"ConfigurationCfg", reflect.TypeFor[ConfigurationCfg](), defs["configuration"].(map[string]any)},
		{"DepCfg", reflect.TypeFor[DepCfg](), defs["dependency"].(map[string]any)},
	}
	for _, tc := range cases {
		got := schemaPropertyNames(t, tc.schemaObj)
		want := yamlFieldNames(tc.goType)
		if !eqStrings(got, want) {
			t.Errorf("%s schema properties = %v, want %v", tc.name, got, want)
		}
	}

	// SourceCfg is represented as a oneOf union over archive and git variants;
	// the union of their properties must equal SourceCfg's YAML fields.
	sourceOneOf := defs["source"].(map[string]any)["oneOf"].([]any)
	seenSourceFields := map[string]bool{}
	for _, branch := range sourceOneOf {
		for _, k := range schemaPropertyNames(t, branch.(map[string]any)) {
			seenSourceFields[k] = true
		}
	}
	gotSource := slices.Sorted(maps.Keys(seenSourceFields))
	wantSource := yamlFieldNames(reflect.TypeFor[SourceCfg]())
	if !eqStrings(gotSource, wantSource) {
		t.Errorf("SourceCfg schema properties = %v, want %v", gotSource, wantSource)
	}

	// cmkOwnedCacheVariables must match cmakeVariables.propertyNames.not.enum.
	notEnum := defs["cmakeVariables"].(map[string]any)["propertyNames"].(map[string]any)["not"].(map[string]any)["enum"].([]any)
	var gotOwned []string
	for _, v := range notEnum {
		gotOwned = append(gotOwned, v.(string))
	}
	slices.Sort(gotOwned)
	wantOwned := slices.Sorted(maps.Keys(cmkOwnedCacheVariables))
	if !eqStrings(gotOwned, wantOwned) {
		t.Errorf("cmakeVariables excluded enum = %v, want %v", gotOwned, wantOwned)
	}
}

func TestSchemaCommandStdoutAndOutputFlag(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := Run([]string{"schema"}, &out, &errBuf, "test"); code != 0 {
		t.Fatalf("cmk schema exited %d: %s", code, errBuf.String())
	}
	if !bytes.Equal(out.Bytes(), cmkJSONSchema) {
		t.Fatal("cmk schema stdout differed from embedded schema")
	}

	dest := filepath.Join(t.TempDir(), "sub", "cmk.schema.json")
	out.Reset()
	errBuf.Reset()
	if code := Run([]string{"schema", "-o", dest}, &out, &errBuf, "test"); code != 0 {
		t.Fatalf("cmk schema -o exited %d: %s", code, errBuf.String())
	}
	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, cmkJSONSchema) {
		t.Fatal("cmk schema -o file differed from embedded schema")
	}
}
