package hljs

import "testing"

// registerSnapshot materializes each snapshot node as its own object at the node's index. A mode keeps the snapshot's Object.isFrozen bit: expandOrCloneMode clones a frozen mode before compiling it (highlight.js core.js expandOrCloneMode), so a frozen mode that lost the bit would be compiled in place and shared between parents.
func TestRegisterSnapshotMaterializesEveryNodeWithItsFrozenBit(t *testing.T) {
	r := &Registry{languages: map[string]*Mode{}, aliases: map[string]string{}}
	for _, data := range [][]byte{eagerGrammars, allGrammars} {
		s := decodeSnapshot(data, snapshotGrammars)
		r.registerSnapshot(s)
		frozen, modes := 0, 0
		seen := map[any]bool{}
		for i, node := range s.Nodes {
			target := r.nodes[s.FirstNode+i]
			switch node.Kind {
			case "mode":
				mode, ok := target.(*Mode)
				if !ok {
					t.Fatalf("node %d is %T, want *Mode", s.FirstNode+i, target)
				}
				if seen[mode] {
					t.Fatalf("node %d shares its mode with an earlier node", s.FirstNode+i)
				}
				seen[mode] = true
				modes++
				if mode.frozen != node.Frozen {
					t.Fatalf("node %d frozen %v, snapshot says %v", s.FirstNode+i, mode.frozen, node.Frozen)
				}
				if node.Frozen {
					frozen++
				}
			case "keywords":
				keywords, ok := target.(*keywordsObject)
				if !ok {
					t.Fatalf("node %d is %T, want *keywordsObject", s.FirstNode+i, target)
				}
				if seen[keywords] {
					t.Fatalf("node %d shares its keywords object with an earlier node", s.FirstNode+i)
				}
				seen[keywords] = true
			}
		}
		if modes == 0 || frozen == 0 {
			t.Fatalf("snapshot at node %d has %d modes, %d frozen: the check proves nothing", s.FirstNode, modes, frozen)
		}
	}
}
