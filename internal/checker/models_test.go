package checker

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseModelIDs(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"id":"gpt-4o-mini"},{"id":"claude-sonnet-4-5"},{"id":"gpt-4o-mini"}]}`)
	got, err := parseModelIDs(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gpt-4o-mini", "claude-sonnet-4-5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseModelIDs() = %#v, want %#v", got, want)
	}
}

func TestParseModelIDsAllowsEmptyStandardList(t *testing.T) {
	got, err := parseModelIDs([]byte(`{"object":"list","data":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("parseModelIDs() = %#v, want empty list", got)
	}
}

func TestParseModelIDsRejectsMissingData(t *testing.T) {
	_, err := parseModelIDs([]byte(`{"object":"list"}`))
	if err == nil || !strings.Contains(err.Error(), "does not contain data[]") {
		t.Fatalf("parseModelIDs() error = %v, want missing data[] error", err)
	}
}
