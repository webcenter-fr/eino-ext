package cachestab

import (
	"context"
	"github.com/goccy/go-json"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeModel records the tools it was given.
type fakeModel struct {
	gotTools []*schema.ToolInfo
}

func (f *fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return nil, nil
}
func (f *fakeModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}
func (f *fakeModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &fakeModel{gotTools: tools}, nil
}

func sampleTool(name string) *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: name,
		Desc: "d",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"zebra": {Type: schema.String, Required: true},
			"alpha": {Type: schema.Number, Required: true},
			"mid":   {Type: schema.String},
		}),
	}
}

func TestToolsSortedByName(t *testing.T) {
	base := &fakeModel{}
	m, err := NewToolCallingChatModel(base)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := m.WithTools([]*schema.ToolInfo{sampleTool("charlie"), sampleTool("alpha"), sampleTool("bravo")})
	if err != nil {
		t.Fatal(err)
	}
	got := bound.(*ToolCallingChatModel).base.(*fakeModel).gotTools
	want := []string{"alpha", "bravo", "charlie"}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("tool[%d]=%s want %s", i, got[i].Name, w)
		}
	}
}

func schemaJSON(t *testing.T, info *schema.ToolInfo) string {
	t.Helper()
	sc, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSchemaKeysDeterministic(t *testing.T) {
	out1, err := NormalizeTools([]*schema.ToolInfo{sampleTool("x")})
	if err != nil {
		t.Fatal(err)
	}
	out2, _ := NormalizeTools([]*schema.ToolInfo{sampleTool("x")})

	j1 := schemaJSON(t, out1[0])
	j2 := schemaJSON(t, out2[0])
	if j1 != j2 {
		t.Fatalf("schema not deterministic:\n%s\n%s", j1, j2)
	}

	// required must be sorted, properties in sorted order in the marshalled JSON.
	if got := j1; !contains(got, `"required":["alpha","zebra"]`) {
		t.Fatalf("required not sorted: %s", got)
	}
	// alpha property should appear before zebra in serialized properties.
	if idxAlpha, idxZebra := indexOf(j1, `"alpha"`), indexOf(j1, `"zebra"`); idxAlpha > idxZebra {
		t.Fatalf("properties not sorted: %s", j1)
	}
}

func TestSemanticsPreserved(t *testing.T) {
	in := sampleTool("t")
	out, err := NormalizeTools([]*schema.ToolInfo{in})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Name != "t" || out[0].Desc != "d" {
		t.Fatal("tool identity changed")
	}
	scIn, _ := in.ParamsOneOf.ToJSONSchema()
	scOut, _ := out[0].ParamsOneOf.ToJSONSchema()
	if scIn.Properties.Len() != scOut.Properties.Len() {
		t.Fatal("property set changed")
	}
	for _, k := range []string{"alpha", "mid", "zebra"} {
		if _, ok := scOut.Properties.Get(k); !ok {
			t.Fatalf("missing property %s", k)
		}
	}
}

func TestNilParamsUnchanged(t *testing.T) {
	in := &schema.ToolInfo{Name: "noparams"}
	out, err := NormalizeTools([]*schema.ToolInfo{in})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].ParamsOneOf != nil {
		t.Fatal("expected nil ParamsOneOf to remain nil")
	}
}

func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
