package checker

import (
	"reflect"
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
