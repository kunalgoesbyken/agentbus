//go:build integration && (linux || darwin)

package connector

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/staysup/agentbus/workspace"
)

// The look again measures from when the conflict check began, not from when
// the look again does: a fetch that outlasts Racy leaves a write made as it
// began older than Racy by then, and a look again measured from its own start
// would trust the file's old size, time and mode. A file whose change time is
// older than Racy before the check is kept as it was, written to or not.
func TestApplyMeasuresFromTheStartOfTheCheck(t *testing.T) {
	racy := workspace.Racy + 300*time.Millisecond
	for _, tc := range []struct {
		name string
		// settle is how long the files rest before the apply begins.
		settle time.Duration
		// fetch is how long the fetch goes on after the person writes.
		fetch     time.Duration
		person    func(folder string)
		conflicts []ConflictFile
		want      string
	}{
		{
			name: "a write with the time put back, early in a slow fetch", settle: racy, fetch: racy,
			person:    func(f string) { putBack(t, filepath.Join(f, "a.txt"), "own\n") },
			conflicts: []ConflictFile{{"a.txt", WhyChanged}}, want: "own\n",
		},
		{
			name: "a write with the time put back, to a file not touched for longer than Racy", settle: racy,
			person:    func(f string) { putBack(t, filepath.Join(f, "a.txt"), "own\n") },
			conflicts: []ConflictFile{{"a.txt", WhyChanged}}, want: "own\n",
		},
		{
			name: "a file not touched for longer than Racy", settle: racy,
			person: func(string) {}, want: "agent\n",
		},
		{
			name: "a file not touched for longer than Racy, its time moved and put back", settle: racy,
			person: func(f string) {
				p := filepath.Join(f, "a.txt")
				fi, err := os.Lstat(p)
				if err != nil {
					t.Fatal(err)
				}
				os.Chtimes(p, time.Now(), time.Now())
				os.Chtimes(p, fi.ModTime(), fi.ModTime())
			},
			want: "agent\n",
		},
		{
			name: "a file not touched for longer than Racy, a slow fetch and nobody wrote", settle: racy, fetch: racy,
			person: func(string) {}, want: "agent\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			folder := t.TempDir()
			write(t, folder, map[string]string{"a.txt": "one\n"})
			agedBy(t, folder, -time.Hour)
			time.Sleep(tc.settle)
			cs := workspace.Changeset{Space: "s", Checkpoint: "t1", Files: []workspace.FileChange{
				{Path: "a.txt", Base: fileOf("one\n", 0o644), Now: fileOf("agent\n", 0o644)},
			}}
			after := shot(t, folder)
			got, err := applyWhile(t, folder, cs, []string{"agent\n"}, "agent\n", func() {
				tc.person(folder)
				after = shot(t, folder)
				time.Sleep(tc.fetch)
			})
			if tc.conflicts != nil {
				refused(t, got, err, tc.conflicts)
				untouched(t, folder, after)
			} else if err != nil {
				t.Fatalf("apply refused: %v", err)
			}
			if g := text(t, filepath.Join(folder, "a.txt")); g != tc.want {
				t.Errorf("a.txt holds %q, want %q", g, tc.want)
			}
		})
	}
}
