package main

import (
	"reflect"
	"testing"
)

func TestSplitCommaSeparatedTrimsWhitespaceAndDropsEmptyEntries(t *testing.T) {
	t.Parallel()

	got := splitCommaSeparated(" pods, deployments ,, services ")
	want := []string{"pods", "deployments", "services"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitCommaSeparated() = %#v, want %#v", got, want)
	}
}

func TestSplitCommaSeparatedEmptyStringReturnsNil(t *testing.T) {
	t.Parallel()

	got := splitCommaSeparated("   ")
	if len(got) != 0 {
		t.Fatalf("splitCommaSeparated() = %#v, want empty slice", got)
	}
}

func TestSplitCommaSeparatedPreserveEmptyKeepsCoreAPIGroupMarker(t *testing.T) {
	t.Parallel()

	got := splitCommaSeparatedPreserveEmpty(",apps,batch")
	want := []string{"", "apps", "batch"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitCommaSeparatedPreserveEmpty() = %#v, want %#v", got, want)
	}
}
