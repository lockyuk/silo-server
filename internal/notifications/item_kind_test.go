package notifications

import "testing"

func TestFlatItemKindToggleAndSelectionMapping(t *testing.T) {
	// Each kind's ingest-scope flag must read its own field, not a neighbor's.
	selections := map[string]AvailabilityKinds{
		EventKindMovie:     {Movies: true},
		EventKindAudiobook: {Audiobooks: true},
		EventKindEbook:     {Ebooks: true},
	}
	for _, k := range flatItemKinds {
		for kind, sel := range selections {
			if got, want := k.Selected(sel), kind == k.Kind; got != want {
				t.Errorf("kind %q Selected(scope with only %q on) = %v, want %v",
					k.Kind, kind, got, want)
			}
		}
	}
}
