package connector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/staysup/agentbus/workspace"
)

// unhashedChange is a turn's changeset of old.bin as the overlay records it
// for a big file: the Base by size, time and mode with no hash, and the file
// the turn left (nil: the turn removed it). taken is when the Base began to
// read the folder.
func unhashedChange(t *testing.T, folder string, removed bool, taken int64) (c *Connector, ref, changes string) {
	t.Helper()
	fi, err := os.Lstat(filepath.Join(folder, "old.bin"))
	if err != nil {
		t.Fatal(err)
	}
	base := &workspace.File{Size: fi.Size(), Mode: fi.Mode(), ModTime: fi.ModTime().UnixNano()}
	var now *workspace.File
	contents := map[string]string{}
	if !removed {
		now = &workspace.File{Size: 6, Mode: fi.Mode(), Hash: workspace.TreeHash([]byte("agent\n"))}
		contents["n"] = "agent\n"
	}
	c, ref = newConnector(t, folder)
	return c, ref, putChangeset(t, c, workspace.Changeset{Space: "s", Checkpoint: "t1", Taken: taken, Files: []workspace.FileChange{
		{Path: "old.bin", Base: base, Now: now}}}, contents)
}

// A base file recorded without a hash is as the Base saw it while nothing
// changed it since the Base began: nobody touched it, so the apply writes it
// without reading it.
func TestApplyUnhashedBaseOfAFileNobodyTouched(t *testing.T) {
	folder := t.TempDir()
	write(t, folder, map[string]string{"old.bin": "one\n"})
	then := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(folder, "old.bin"), then, then); err != nil {
		t.Fatal(err)
	}
	c, ref, h := unhashedChange(t, folder, false, time.Now().Add(time.Minute).UnixNano())
	if _, err := c.Apply(context.Background(), ref, "", h); err != nil {
		t.Fatal(err)
	}
	if got := text(t, filepath.Join(folder, "old.bin")); got != "agent\n" {
		t.Fatalf("old.bin is %q", got)
	}
}

// A changeset written before it carried a base time has none to compare a
// change time with: the times it has are trusted, as they were.
func TestApplyUnhashedBaseWithNoTakenTrustsTheTimes(t *testing.T) {
	folder := t.TempDir()
	write(t, folder, map[string]string{"old.bin": "one\n"})
	c, ref, h := unhashedChange(t, folder, false, 0)
	if _, err := c.Apply(context.Background(), ref, "", h); err != nil {
		t.Fatal(err)
	}
}

// A file whose time changed is a conflict, a base time or none.
func TestApplyUnhashedBaseRefusesAnEditedFile(t *testing.T) {
	for _, taken := range []int64{0, time.Now().Add(time.Minute).UnixNano()} {
		folder := t.TempDir()
		write(t, folder, map[string]string{"old.bin": "one\n"})
		c, ref, h := unhashedChange(t, folder, false, taken)
		then := time.Now().Add(-time.Hour)
		write(t, folder, map[string]string{"old.bin": "two\n"})
		if err := os.Chtimes(filepath.Join(folder, "old.bin"), then, then); err != nil {
			t.Fatal(err)
		}
		var cf *Conflict
		if _, err := c.Apply(context.Background(), ref, "", h); !errors.As(err, &cf) {
			t.Fatalf("taken %d: %v", taken, err)
		}
	}
}
