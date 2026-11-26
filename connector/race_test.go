package connector

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/staysup/agentbus/blobs"
	"github.com/staysup/agentbus/workspace"
)

type slowRemote struct {
	*blobs.Store
	on   string
	hook func()
}

func (r *slowRemote) Open(ctx context.Context, h string) (io.ReadCloser, error) {
	if h == r.on && r.hook != nil {
		r.hook()
		r.hook = nil
	}
	return r.Store.Open(ctx, h)
}

func (r *slowRemote) Missing(ctx context.Context, hs []string) ([]string, error) { return nil, nil }

func TestApplyKeepsAnEditMadeWhileItFetches(t *testing.T) {
	folder := t.TempDir()
	write(t, folder, map[string]string{"a.txt": "one\n"})
	c, ref := newConnector(t, folder)
	remote, err := blobs.Open(filepath.Join(t.TempDir(), "remote"))
	if err != nil {
		t.Fatal(err)
	}
	remote.PutBytes([]byte("agent\n"))
	cs := workspace.Changeset{Space: "s", Checkpoint: "t1", Files: []workspace.FileChange{
		{Path: "a.txt", Base: fileOf("one\n", 0o644), Now: fileOf("agent\n", 0o644)},
	}}
	cb := mustJSON(t, cs)
	h, err := remote.PutBytes(cb)
	if err != nil {
		t.Fatal(err)
	}
	c.Remote = &slowRemote{Store: remote, on: workspace.TreeHash([]byte("agent\n")), hook: func() {
		write(t, folder, map[string]string{"a.txt": "mine, typed while the apply ran\n"})
	}}
	_, err = c.Apply(context.Background(), ref, "", h)
	got := text(t, filepath.Join(folder, "a.txt"))
	t.Logf("err=%v hook-left=%v got=%q", err, c.Remote.(*slowRemote).hook != nil, got)
	if err == nil && got != "mine, typed while the apply ran\n" {
		t.Fatalf("apply said applied and the person's edit is gone: folder holds %q", got)
	}
	_ = os.Stdout
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
