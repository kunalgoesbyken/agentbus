package isolation

import (
	"strings"
	"testing"
	"time"
)

// A changeset says when its Base began to read the person's folder, so an
// apply can tell a file the person changed since from one nobody touched. A
// copy with its own git repo has a Base git hashed, which says nothing more.
func TestChangesetSaysWhenTheBaseBeganToRead(t *testing.T) {
	src := t.TempDir()
	fixture(t, src, map[string]string{"big.bin": strings.Repeat("x", MaxFileDiff+10)})
	age(t, src)
	for _, name := range fast(t) {
		t.Run(name, func(t *testing.T) {
			before := time.Now()
			b, s := open(t, name, src)
			after := time.Now()
			fixture(t, s.Dir, map[string]string{"big.bin": strings.Repeat("y", MaxFileDiff+20)})
			if _, err := b.Checkpoint(s, "t1"); err != nil {
				t.Fatal(err)
			}
			cs, err := ChangesetOf(s, "t1")
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case s.hasShadow():
				if cs.Taken != 0 {
					t.Fatalf("a copy with a git Base has Taken %d", cs.Taken)
				}
			case cs.Taken < before.UnixNano() || cs.Taken > after.UnixNano():
				t.Fatalf("Taken %d is not between the open's start %d and end %d", cs.Taken, before.UnixNano(), after.UnixNano())
			}
		})
	}
}
