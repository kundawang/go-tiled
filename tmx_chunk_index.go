/*
Copyright (c) 2026 Lauris Bukšis <lauris@nix.lv>

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package tiled

import (
	"container/heap"
	"sort"
)

// This file implements the overlap check for a layer's chunks as a sweep over
// Y with an interval tree of the chunks crossing the sweep line. A pairwise
// comparison would be quadratic, which an open-world map with many chunks (or
// a malformed one built to be pathological) can make arbitrarily expensive.

// intervalNode is one chunk's x-range in the sweep-line interval tree. Nodes
// are ordered by (x, seq); maxRight is the largest right edge in the subtree,
// which lets findOverlap skip subtrees that cannot intersect the query.
type intervalNode struct {
	x, right int64
	seq      uint64
	chunk    *Chunk
	prio     uint64
	maxRight int64
	l, r     *intervalNode
}

// chunkOverlapIndex is an interval tree over chunk x-ranges.
type chunkOverlapIndex struct {
	root *intervalNode
	seq  uint64
	keys map[*Chunk]uint64
}

func newChunkOverlapIndex() *chunkOverlapIndex {
	return &chunkOverlapIndex{keys: make(map[*Chunk]uint64)}
}

// splitMix64 is a small, deterministic hash used for treap priorities. Using
// a hash of the insertion sequence avoids importing math/rand and keeps
// overlap detection reproducible.
func splitMix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func intervalLess(x1 int64, s1 uint64, x2 int64, s2 uint64) bool {
	if x1 != x2 {
		return x1 < x2
	}
	return s1 < s2
}

func intervalUpdate(n *intervalNode) {
	n.maxRight = n.right
	if n.l != nil && n.l.maxRight > n.maxRight {
		n.maxRight = n.l.maxRight
	}
	if n.r != nil && n.r.maxRight > n.maxRight {
		n.maxRight = n.r.maxRight
	}
}

// split cuts t into the nodes ordered before (x, seq) and the rest.
func intervalSplit(t *intervalNode, x int64, seq uint64) (l, r *intervalNode) {
	if t == nil {
		return nil, nil
	}
	if intervalLess(t.x, t.seq, x, seq) {
		var rest *intervalNode
		t.r, rest = intervalSplit(t.r, x, seq)
		intervalUpdate(t)
		return t, rest
	}
	var rest *intervalNode
	rest, t.l = intervalSplit(t.l, x, seq)
	intervalUpdate(t)
	return rest, t
}

// merge joins two trees where every key in a orders before every key in b.
func intervalMerge(a, b *intervalNode) *intervalNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio < b.prio {
		a.r = intervalMerge(a.r, b)
		intervalUpdate(a)
		return a
	}
	b.l = intervalMerge(a, b.l)
	intervalUpdate(b)
	return b
}

// insert adds c's x-range to the index.
func (ix *chunkOverlapIndex) insert(c *Chunk) {
	ix.seq++
	seq := ix.seq
	ix.keys[c] = seq

	n := &intervalNode{
		x:     int64(c.X),
		right: int64(c.X) + int64(c.Width),
		seq:   seq,
		chunk: c,
		prio:  splitMix64(seq),
	}
	intervalUpdate(n)

	l, r := intervalSplit(ix.root, n.x, n.seq)
	ix.root = intervalMerge(intervalMerge(l, n), r)
}

// remove drops c's x-range from the index. It is a no-op if c is absent.
func (ix *chunkOverlapIndex) remove(c *Chunk) {
	seq, ok := ix.keys[c]
	if !ok {
		return
	}
	delete(ix.keys, c)

	x := int64(c.X)
	// Split at the node's own key, then isolate it from the rest: every other
	// key is either strictly before (x, seq) or at least (x, seq+1).
	l, rest := intervalSplit(ix.root, x, seq)
	_, r := intervalSplit(rest, x, seq+1)
	ix.root = intervalMerge(l, r)
}

// findOverlap returns an indexed chunk whose x-range intersects [x0, x1), or
// nil. The caller is responsible for ensuring Y ranges overlap.
func (ix *chunkOverlapIndex) findOverlap(x0, x1 int64) *Chunk {
	return intervalFindOverlap(ix.root, x0, x1)
}

func intervalFindOverlap(n *intervalNode, x0, x1 int64) *Chunk {
	if n == nil {
		return nil
	}
	// Only the left subtree can hold a candidate with x >= x1, so it is
	// searched unconditionally (guarded by maxRight).
	if n.l != nil && n.l.maxRight > x0 {
		if c := intervalFindOverlap(n.l, x0, x1); c != nil {
			return c
		}
	}
	if n.x < x1 && n.right > x0 {
		return n.chunk
	}
	// Every key in the right subtree is >= n.x, so if n.x is already at or
	// beyond x1 none of them can intersect.
	if n.x < x1 {
		return intervalFindOverlap(n.r, x0, x1)
	}
	return nil
}

// chunkEndHeap orders chunks by the bottom edge of their Y range.
type chunkEndHeap []*Chunk

func (h chunkEndHeap) Len() int { return len(h) }
func (h chunkEndHeap) Less(i, j int) bool {
	bi := int64(h[i].Y) + int64(h[i].Height)
	bj := int64(h[j].Y) + int64(h[j].Height)
	if bi != bj {
		return bi < bj
	}
	return h[i].X < h[j].X
}
func (h chunkEndHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *chunkEndHeap) Push(x any)   { *h = append(*h, x.(*Chunk)) }
func (h *chunkEndHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return c
}

// findOverlap reports a pair of partially overlapping chunks, if any. Chunks
// must be sorted by Y then X and must already have passed validate, which
// bounds their coordinates.
func findOverlap(chunks []*Chunk) (a, b *Chunk) {
	ix := newChunkOverlapIndex()
	ends := make(chunkEndHeap, 0, len(chunks))

	for _, c := range chunks {
		for ends.Len() > 0 {
			top := ends[0]
			if int64(top.Y)+int64(top.Height) > int64(c.Y) {
				break
			}
			ix.remove(heap.Pop(&ends).(*Chunk))
		}

		x0 := int64(c.X)
		x1 := x0 + int64(c.Width)
		if other := ix.findOverlap(x0, x1); other != nil {
			return other, c
		}

		ix.insert(c)
		heap.Push(&ends, c)
	}

	return nil, nil
}

// sortChunks orders chunks by Y, then X, then size, so the sweep above and
// Layer.TileAt's lookups can rely on it.
func sortChunks(chunks []*Chunk) {
	sort.Slice(chunks, func(i, j int) bool {
		a, b := chunks[i], chunks[j]
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		if a.X != b.X {
			return a.X < b.X
		}
		if a.Width != b.Width {
			return a.Width < b.Width
		}
		return a.Height < b.Height
	})
}
