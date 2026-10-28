package graveyard

import "time"

// batchGap is how close together kills must be to count as one action
// (one stealth sweep, one `free`, one multi-kill in the app).
const batchGap = time.Minute

// LastBatch returns the most recent group of kills that haven't been revived:
// the newest such kill plus every unrevived kill by the same source within a
// minute of the one before it. es is newest first, as Load returns it.
func LastBatch(es []Entry) []Entry {
	var out []Entry
	for _, e := range es {
		if e.Revived != nil {
			continue
		}
		if len(out) == 0 {
			out = append(out, e)
			continue
		}
		last := out[len(out)-1]
		if e.By != last.By || last.Killed.Sub(e.Killed) > batchGap {
			break
		}
		out = append(out, e)
	}
	return out
}
