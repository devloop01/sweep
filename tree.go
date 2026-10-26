//go:build darwin && arm64

package main

import (
	"cmp"
	"slices"
	"strings"
)

// node is a folder in the result tree. Leaves are the folders that get
// deleted; the others group them and carry the total size below them.
type node struct {
	name     string
	size     int64
	leaf     bool
	children []*node
}

// row is one printed line of the tree.
type row struct {
	label string
	size  int64
	leaf  bool
}

// tree lays out paths (relative, "/"-separated) with their sizes as a tree:
// grouped by folder, largest first at every level, and with chains of
// single folders collapsed into one label (agency/0xi/mockiro/).
func tree(paths []string, sizes []int64) []row {
	root := &node{}
	for i, p := range paths {
		n := root
		for _, part := range strings.Split(p, "/") {
			i := slices.IndexFunc(n.children, func(c *node) bool { return c.name == part })
			if i < 0 {
				n.children = append(n.children, &node{name: part})
				i = len(n.children) - 1
			}
			n = n.children[i]
		}
		n.leaf, n.size = true, sizes[i]
	}
	root.settle()

	var rows []row
	for _, c := range root.children {
		rows = append(rows, c.row(""))
		c.walk("", &rows)
	}
	return rows
}

// settle sums sizes up the tree, collapses single-child chains and sorts
// children by size, largest first.
func (n *node) settle() {
	for i, c := range n.children {
		for !c.leaf && len(c.children) == 1 {
			only := c.children[0]
			only.name = c.name + "/" + only.name
			c = only
		}
		c.settle()
		n.children[i] = c
	}
	if !n.leaf {
		n.size = 0
		for _, c := range n.children {
			n.size += c.size
		}
	}
	slices.SortFunc(n.children, func(a, b *node) int {
		return cmp.Or(cmp.Compare(b.size, a.size), strings.Compare(a.name, b.name))
	})
}

func (n *node) row(prefix string) row {
	label := prefix + n.name
	if !n.leaf {
		label += "/"
	}
	return row{label, n.size, n.leaf}
}

// walk adds n's children as rows, drawn with tree lines under prefix.
func (n *node) walk(prefix string, rows *[]row) {
	for i, c := range n.children {
		branch, indent := "├── ", "│   "
		if i == len(n.children)-1 {
			branch, indent = "└── ", "    "
		}
		*rows = append(*rows, c.row(prefix+branch))
		c.walk(prefix+indent, rows)
	}
}
