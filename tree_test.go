//go:build darwin && arm64

package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestTree(t *testing.T) {
	rows := tree(
		[]string{
			"web/a/.next",
			"agency/0xi/mockiro/node_modules",
			"agency/0xi/mockiro/apps/mockup/.vercel/output",
			"agency/0xi/mockiro/apps/mockup/.svelte-kit",
			"agency/0xi/stlflix/x/node_modules",
			"b/node_modules",
			"a/node_modules",
		},
		[]int64{1, 3, 2, 1, 5, 4, 4},
	)
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprintf("%s %d %v", r.label, r.size, r.leaf))
	}
	want := []string{
		"agency/0xi/ 11 false",         // groups: total below, trailing /
		"├── mockiro/ 6 false",         // largest first at every level
		"│   ├── apps/mockup/ 3 false", // single-folder chains collapse; ties go by name
		"│   │   ├── .vercel/output 2 true",
		"│   │   └── .svelte-kit 1 true",
		"│   └── node_modules 3 true",
		"└── stlflix/x/node_modules 5 true", // a chain ending in a target is one row
		"a/node_modules 4 true",             // equal sizes: by name
		"b/node_modules 4 true",
		"web/a/.next 1 true",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
