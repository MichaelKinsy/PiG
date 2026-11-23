package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestTagSetsListEachTagAloneThenAll(t *testing.T) {
	sets := tagSets([]string{"pig_strip_a", "pig_strip_b", "pig_strip_c"}, map[string]string{"pig_strip_b": "b-api", "pig_strip_c": "c-api"})
	var got []string
	for _, set := range sets {
		got = append(got, set.String()+" strips "+strings.Join(set.apis, "+"))
	}
	want := []string{"pig_strip_a strips ", "pig_strip_b strips b-api", "pig_strip_c strips c-api", "pig_strip_a,pig_strip_b,pig_strip_c strips b-api+c-api"}
	if !slices.Equal(got, want) {
		t.Fatalf("tagSets = %q, want %q", got, want)
	}
}

func TestStrippedProvidersHaveEveryModelOnAStrippedAPI(t *testing.T) {
	models := []ai.GeneratedModel{
		{Provider: "one", API: "a"}, {Provider: "one", API: "a"},
		{Provider: "mixed", API: "a"}, {Provider: "mixed", API: "b"},
		{Provider: "two", API: "c"},
	}
	providers := []string{"one", "mixed", "two", "no-chat-models"}
	for apis, want := range map[string][]string{"": nil, "a": {"one"}, "a,b": {"one", "mixed"}, "c": {"two"}, "a,b,c": {"one", "mixed", "two"}} {
		var stripped []string
		if apis != "" {
			stripped = strings.Split(apis, ",")
		}
		if got := strippedProviders(models, providers, stripped); !slices.Equal(got, want) {
			t.Errorf("strippedProviders(%q) = %v, want %v", apis, got, want)
		}
	}
}

func TestCheckListReadsTheProviderColumn(t *testing.T) {
	list := "provider  model  context\none       m1     1K\none       m2     1K\ntwo       m3     1K\n"
	for _, tc := range []struct {
		name, output string
		stripped     []string
		kept         []string
		want         string
	}{
		{"kept and stripped as expected", list, []string{"three"}, []string{"one", "two"}, ""},
		{"a stripped provider is listed", list, []string{"one"}, []string{"two"}, "--list-models lists 2 models of stripped provider one"},
		{"a kept provider is missing", list, nil, []string{"one", "three"}, "--list-models lists no model of provider three, which this set keeps"},
		{"no table", "No models available.\n", nil, nil, "--list-models listed no model:\nNo models available.\n"},
	} {
		got := ""
		if err := checkList(tc.output, tc.stripped, tc.kept); err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestParseRedReadsNamesAndRequiresReasons(t *testing.T) {
	tests, err := parseRed("# known red\n\nTestOne expects a google-vertex row\nTestTwo\tcompares mistral requests with Pi\n")
	if err != nil || !slices.Equal(tests, []string{"TestOne", "TestTwo"}) {
		t.Fatalf("parseRed = %q, %v", tests, err)
	}
	for _, text := range []string{"TestOne\n", "notATest reason\n", "TestOne a\nTestOne b\n"} {
		if _, err := parseRed(text); err == nil {
			t.Errorf("parseRed(%q) accepted the list", text)
		}
	}
}

func TestPickSetFollowsSetsAndSet(t *testing.T) {
	sets := tagSets([]string{"pig_strip_a", "pig_strip_b"}, map[string]string{"pig_strip_b": "b-api"})
	for _, tc := range []struct {
		name   string
		only   []string
		single string
		want   []string
	}{
		{"api takes the sets that strip an API, all tags among them", nil, "api", []string{"pig_strip_b", "pig_strip_a,pig_strip_b"}},
		{"all takes every set", nil, "all", []string{"pig_strip_a", "pig_strip_b", "pig_strip_a,pig_strip_b"}},
		{"-set names win over -sets", []string{"pig_strip_a"}, "api", []string{"pig_strip_a"}},
	} {
		var got []string
		for _, set := range sets {
			if pickSet(set, tc.only, tc.single) {
				got = append(got, set.String())
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: picked %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWholePackageTestArgsNameTimeout(t *testing.T) {
	set := tagSet{tags: []string{"pig_strip_bedrock_converse_stream"}}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"ai without red", aiTestArgs(set, nil)},
		{"ai with red", aiTestArgs(set, []string{"TestOne", "TestTwo"})},
		{"whole packages", wholeTestArgs(set)},
	} {
		i := slices.Index(tc.args, "-timeout")
		if i < 0 || i+1 >= len(tc.args) || tc.args[i+1] == "" {
			t.Errorf("%s: args %q lack -timeout <duration>", tc.name, tc.args)
		}
	}
}
