package store

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func goldenWhere() WhereNode {
	return WhereNode{Op: WhereAnd, Nodes: []WhereNode{
		{Op: WhereEq, Column: "naïve<&>", Value: "héllo \"</script>\u2028\" ☃"},
		{Op: WhereNot, Nodes: []WhereNode{{Op: WhereIsNull, Column: "c>d"}}},
		{Op: WhereInArray, Column: "k", Values: []Scalar{int64(1), "ü&", 2.5, true}},
	}}
}

func goldenExpr() ExprNode {
	cond := goldenWhere()
	return ExprNode{Op: ExprWhen, Cond: &cond, Args: []ExprNode{
		{Op: ExprReplacePrefix, From: "a<", To: "é&", Args: []ExprNode{{Op: ExprCol, Column: "ç<col>"}}},
		{Op: ExprAdd, Args: []ExprNode{{Value: int64(3)}, {Value: "x\u2029y"}}},
	}}
}

func goldenJSON() delta.JsonValue {
	return delta.JsonObjectOf("kind", "weird", "list", []any{"<ü>", nil, float64(1.5), delta.JsonObjectOf("a&b", "☃")})
}

func TestMarshalJSONGolden(t *testing.T) {
	jsonNode := ExprNode{Op: ExprJSON, Json: goldenJSON()}
	cases := []struct {
		name string
		got  any
		want string
	}{
		{"manifest", &ManifestPart{Format: 1, Schema: 2, LastSeq: 9007199254740991, NextID: 42}, "{\"kind\":\"manifest\",\"format\":1,\"schema\":2,\"lastSeq\":9007199254740991,\"nextId\":42}"},
		{"end", &EndPart{Writes: 7}, "{\"kind\":\"end\",\"writes\":7}"},
		{"indexes", IndexRecords{{Name: "ïdx<1>", Columns: []string{"a&b", "ç"}}, {Name: "empty"}}, "{\"ïdx<1>\":[\"a&b\",\"ç\"],\"empty\":[]}"},
		{"columns", ColumnRecords{
			{Name: "naïve<&>", Record: ColumnRecord{Type: ColStr, Optional: true, Default: "dé<f>&"}},
			{Name: "j", Record: ColumnRecord{Type: ColJson, Version: func() *int { v := 3; return &v }(), Fingerprint: "fp☃"}},
		}, "{\"naïve<&>\":{\"type\":\"str\",\"optional\":true,\"default\":\"dé<f>&\"},\"j\":{\"type\":\"json\",\"version\":3,\"fingerprint\":\"fp☃\"}}"},
		{"unknown", &UnknownWrite{Value: goldenJSON()}, "{\"kind\":\"weird\",\"list\":[\"<ü>\",null,1.5,{\"a&b\":\"☃\"}]}"},
		{"where", goldenWhere(), "{\"and\":[{\"eq\":[\"naïve<&>\",\"héllo \\\"</script>\u2028\\\" ☃\"]},{\"not\":{\"isNull\":\"c>d\"}},{\"inArray\":[\"k\",[1,\"ü&\",2.5,true]]}]}"},
		{"expr", goldenExpr(), "{\"when\":[{\"and\":[{\"eq\":[\"naïve<&>\",\"héllo \\\"</script>\u2028\\\" ☃\"]},{\"not\":{\"isNull\":\"c>d\"}},{\"inArray\":[\"k\",[1,\"ü&\",2.5,true]]}]},{\"replacePrefix\":[{\"col\":\"ç<col>\"},\"a<\",\"é&\"]},{\"add\":[3,\"x\u2029y\"]}]}"},
		{"exprjson", jsonNode, "{\"json\":{\"kind\":\"weird\",\"list\":[\"<ü>\",null,1.5,{\"a&b\":\"☃\"}]}}"},
		{"set", SetNode{{Column: "ä<", Node: goldenExpr()}, {Column: "b", Node: ExprNode{Op: ExprNull}}}, "{\"ä<\":{\"when\":[{\"and\":[{\"eq\":[\"naïve<&>\",\"héllo \\\"</script>\u2028\\\" ☃\"]},{\"not\":{\"isNull\":\"c>d\"}},{\"inArray\":[\"k\",[1,\"ü&\",2.5,true]]}]},{\"replacePrefix\":[{\"col\":\"ç<col>\"},\"a<\",\"é&\"]},{\"add\":[3,\"x\u2029y\"]}]},\"b\":null}"},
	}
	for _, c := range cases {
		got, err := jsstring.MarshalJSON(c.got)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}
