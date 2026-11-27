package replay

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// A history that fits the budget carries the notes of empty replies like any
// other message: whole while the turn is whole, and as AgentBus's own note in
// the turn's log once the turn is summarised, never as the model's last words
// and never as an empty message. The budget measures and shortens what was
// built, so it must not count, trim or drop the note, and the history it
// returns must still hold nothing the vendor refuses.
func TestEmptyRepliesUnderTheBudget(t *testing.T) {
	const turns = 14
	empties := map[int]bool{3: true, 6: true, 9: true, 12: true}
	byName := map[string]emptyFormat{}
	for _, f := range emptyFormats() {
		byName[f.name] = f
	}
	replies := map[string]map[string]string{
		"claude": {
			"nothing":        `{"role":"assistant","content":[]}`,
			"reasoning only": `{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"U0lHLXR3bw=="}]}`,
		},
		"codex": {
			"nothing":        items(),
			"reasoning only": items(reasoning1),
		},
		"opencode": {
			"nothing":        `{"role":"assistant","content":""}`,
			"reasoning only": `{"role":"assistant","content":null,"reasoning_content":"secret"}`,
		},
	}
	for _, s := range formats() {
		fm := byName[s.name]
		for shape, reply := range replies[s.name] {
			t.Run(s.name+"/"+shape, func(t *testing.T) {
				s := s
				plain := s.add
				s.add = func(t *testing.T, src *fakeSource, turn, x, req string, n int) {
					if empties[n] {
						fm.store(t, src, turn, x, req, reply, fm.stop)
						return
					}
					plain(t, src, turn, x, req, n)
				}
				summarised, logged := 0, 0
				for _, budget := range []int{9000, 14000, 20000} {
					ps := s.run(t, turns, budget)
					within(t, s, ps, budget)
					for i := 2; i < len(ps); i++ {
						for _, msgs := range [][]json.RawMessage{ps[i].first, ps[i].loop} {
							if why := fm.refused(msgs); why != "" {
								t.Fatalf("t%d: the vendor refuses this history: %s", i, why)
							}
						}
						text, whole := "", 0
						for _, m := range ps[i].first {
							if strings.Contains(string(m), "Earlier turns of this session") {
								text = summaryOf(t, s, m)
							} else {
								whole += strings.Count(string(m), emptyWords)
							}
						}
						upTo := 0
						if text != "" {
							left, logs := parseSummary(text)
							upTo = left.n
							for _, e := range logs {
								n, err := strconv.Atoi(strings.TrimPrefix(e.turn, "t"))
								if err != nil {
									t.Fatalf("t%d: a log names turn %q", i, e.turn)
								}
								upTo = max(upTo, n)
								if !empties[n] {
									continue
								}
								logged++
								if !strings.Contains(e.text, "AgentBus noted: The model's reply here was empty.") {
									t.Errorf("t%d: turn %d's log lacks the note: %q", i, n, e.text)
								}
								if strings.Contains(e.text, "The model's last words") {
									t.Errorf("t%d: turn %d's log gives the note as the model's words: %q", i, n, e.text)
								}
							}
						}
						summarised = max(summarised, upTo)
						want := 0
						for k := upTo + 1; k < i; k++ {
							if empties[k] {
								want++
							}
						}
						if whole != want {
							t.Errorf("t%d: %d notes in the whole turns, want %d (turns up to %d are summarised)", i, whole, want, upTo)
						}
					}
				}
				if logged == 0 {
					t.Fatalf("no summary ever held an empty reply's turn, so its log was never checked")
				}
				if summarised == 0 {
					t.Fatalf("%s: the budget never summarised a turn, so this proves nothing", fmt.Sprint(s.name))
				}
			})
		}
	}
}
