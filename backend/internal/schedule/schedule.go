// Package schedule answers one question: "for this window, what should be
// playing right now?"
//
// Nothing in this package talks to a database or the network. Every function
// here takes plain numbers and lists as input and returns an answer, with no
// hidden state. That is on purpose: it means the exact same logic can be
// re-implemented in the browser (see frontend/src/schedule/schedule.ts) and
// both sides will always agree on what should be on screen, without the
// browser ever needing to ask the server "what's next?". The server and every
// browser just look at the current time and calculate the answer themselves.
//
// All timestamps in this package are int64 milliseconds since the Unix
// epoch (the same kind of number you get from JavaScript's Date.now()). We
// use whole milliseconds instead of Go's time.Time or floating point seconds
// so that the math is exact and reproducible in both Go and TypeScript, with
// no rounding drift between the two.
package schedule

// Item is one entry in a playlist: a media clip and how long it plays for.
type Item struct {
	MediaID    string
	DurationMS int64
}

// Version is one saved copy of a window's playlist. Whenever a playlist
// changes (e.g. someone adds a media item), we don't edit the old list in
// place — we save a brand new Version and record the moment it should start
// being used (EffectiveAtMS). This is what lets us change a playlist for the
// future without yanking whatever is already on screen right now.
type Version struct {
	ID            int64
	EffectiveAtMS int64
	Items         []Item
}

// Window is one on-screen display slot that loops through its own playlist.
// CycleAnchorMS is the timestamp its very first 5-hour cycle began; every
// following cycle boundary is a fixed multiple of CycleSeconds after that.
type Window struct {
	ID            string
	CycleAnchorMS int64
}

// State describes exactly what a window should be showing at one instant:
// which item, how far into it we are, and when useful things happen next.
type State struct {
	// Ok is false when there is nothing to play (empty playlist, or the
	// window's first cycle hasn't started yet). Callers should show a
	// fallback ("No media configured") rather than treating this as an error.
	Ok bool

	VersionID  int64
	ItemIndex  int
	MediaID    string
	DurationMS int64

	// OffsetMS is how many milliseconds into the current item we are.
	OffsetMS int64

	// ItemEndsAtMS is the timestamp the current item finishes, capped at the
	// next 5-hour cycle boundary (an item can be cut short by the cycle
	// restarting from item 1, even mid-item).
	ItemEndsAtMS int64

	// CycleEndsAtMS is the timestamp the current 5-hour cycle restarts.
	CycleEndsAtMS int64

	// LoopStartMS / LoopLenMS describe the playlist loop currently in
	// effect. They are exposed (rather than kept private) because the code
	// that creates new playlist versions needs them to compute
	// NextLoopBoundary for the loop that is playing right now.
	LoopStartMS int64
	LoopLenMS   int64
}

// floorDiv is integer division that always rounds toward negative infinity,
// unlike Go's built-in "/" which rounds toward zero. This matters whenever a
// timestamp can land before the anchor it's measured from (e.g. -5 / 3 must
// be -2, the third block "below zero", not 0 — plain "/" would wrongly give
// -1 here since -5/3 truncates toward zero).
func floorDiv(a, b int64) int64 {
	q := a / b
	r := a % b
	if r != 0 && (r < 0) != (b < 0) {
		q--
	}
	return q
}

// floorMod is the remainder that matches floorDiv: its result always has the
// same sign as b (for our use, always non-negative since durations are
// positive). This is what lets "5 minutes before the cycle started" still
// map to a sensible position inside the loop instead of a negative offset.
func floorMod(a, b int64) int64 {
	r := a % b
	if r != 0 && (r < 0) != (b < 0) {
		r += b
	}
	return r
}

// ceilDiv is integer division rounding toward positive infinity, used to
// find "how many whole loops fit before we reach or pass this timestamp".
func ceilDiv(a, b int64) int64 {
	return -floorDiv(-a, b)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// selectVersion picks the playlist version that governs playback at time T:
// the most recent version whose EffectiveAtMS is at or before T. If two
// versions somehow share the same EffectiveAtMS, the one with the larger ID
// (created later) wins, since it reflects the newer decision.
func selectVersion(versions []Version, tMS int64) (Version, bool) {
	best := Version{}
	found := false
	for _, v := range versions {
		if v.EffectiveAtMS > tMS {
			continue
		}
		if !found ||
			v.EffectiveAtMS > best.EffectiveAtMS ||
			(v.EffectiveAtMS == best.EffectiveAtMS && v.ID > best.ID) {
			best = v
			found = true
		}
	}
	return best, found
}

// SelectVersion picks the playlist version that governs playback at time
// T: the most recent version whose EffectiveAtMS is at or before T, tied
// broken in favor of the higher id (the more recently created one). It is
// exposed so callers outside this package (e.g. the HTTP layer building the
// "current vs. upcoming version" view for GET /api/state) can reuse the
// exact same selection rule Now() uses internally, instead of
// re-implementing it and risking the two disagreeing.
func SelectVersion(versions []Version, tMS int64) (Version, bool) {
	return selectVersion(versions, tMS)
}

// Now computes what a window should be showing at time T.
//
// Worked example: a playlist is 10s + 20s + 30s (60s total, one "loop").
// 75 seconds have passed since the loop started. 75 mod 60 = 15, so we are
// 15 seconds into the loop's second pass... no wait, we are 15 seconds into
// THIS pass of the loop (the loop has completed once and is 15s into its
// second run). Walking the items: the first item covers 0-10s, the second
// covers 10-30s. 15s falls in the second item, 5 seconds into it ("15 - 10
// = 5"). So the answer is: item 2 ("M2"), 5 seconds in.
func Now(window Window, versions []Version, cycleMS int64, tMS int64) State {
	if tMS < window.CycleAnchorMS {
		// The window's first cycle hasn't started yet.
		return State{Ok: false}
	}

	v, found := selectVersion(versions, tMS)
	if !found || len(v.Items) == 0 {
		return State{Ok: false}
	}

	elapsedSinceAnchor := tMS - window.CycleAnchorMS
	cycleStart := window.CycleAnchorMS + floorDiv(elapsedSinceAnchor, cycleMS)*cycleMS
	cycleEndsAt := cycleStart + cycleMS

	// The loop can't start playing before the version says it should, even
	// if the 5-hour cycle technically started earlier — that's how a
	// playlist change waits for the right moment instead of retroactively
	// rewriting what already played.
	loopStart := maxInt64(cycleStart, v.EffectiveAtMS)

	var loopLen int64
	for _, item := range v.Items {
		loopLen += item.DurationMS
	}
	if loopLen <= 0 {
		return State{Ok: false}
	}

	pos := floorMod(tMS-loopStart, loopLen)

	var acc int64
	for i, item := range v.Items {
		if pos < acc+item.DurationMS {
			offset := pos - acc
			remaining := item.DurationMS - offset
			itemEndsAt := tMS + remaining
			if itemEndsAt > cycleEndsAt {
				itemEndsAt = cycleEndsAt
			}
			return State{
				Ok:            true,
				VersionID:     v.ID,
				ItemIndex:     i,
				MediaID:       item.MediaID,
				DurationMS:    item.DurationMS,
				OffsetMS:      offset,
				ItemEndsAtMS:  itemEndsAt,
				CycleEndsAtMS: cycleEndsAt,
				LoopStartMS:   loopStart,
				LoopLenMS:     loopLen,
			}
		}
		acc += item.DurationMS
	}

	// Unreachable: pos is always < loopLen (floorMod guarantees it) and the
	// items' durations sum to loopLen, so the loop above always returns.
	return State{Ok: false}
}

// NextLoopBoundary finds the next timestamp at or after T when the given
// loop restarts from its first item. If T already lands exactly on a
// boundary, that same timestamp is returned (the loop is considered to be
// restarting right now, not one full loop later).
func NextLoopBoundary(loopStartMS, loopLenMS, tMS int64) int64 {
	if loopLenMS <= 0 {
		return tMS
	}
	delta := tMS - loopStartMS
	if delta <= 0 {
		// The loop hasn't started yet, so its first boundary is its start.
		return loopStartMS
	}
	// ceilDiv rounds an exact multiple up to itself (not one loop further),
	// so a T that already sits on a boundary comes back unchanged.
	n := ceilDiv(delta, loopLenMS)
	return loopStartMS + n*loopLenMS
}

// NextCycleBoundary finds the next timestamp at or after T when the
// window's fixed 5-hour (CycleSeconds) cycle restarts from item 1.
func NextCycleBoundary(cycleAnchorMS, cycleMS, tMS int64) int64 {
	elapsed := tMS - cycleAnchorMS
	if elapsed < 0 {
		return cycleAnchorMS
	}
	cycleStart := cycleAnchorMS + floorDiv(elapsed, cycleMS)*cycleMS
	return cycleStart + cycleMS
}

// NextVersionEffectiveAt decides when a newly-created playlist version
// should take over, given the window's current playback and any
// already-scheduled (pending) version changes.
//
// The rule: never interrupt what is already playing mid-item, and never
// schedule a change earlier than one that was already promised. So the
// answer is the LATER of (a) the next moment the currently-playing loop or
// 5-hour cycle would restart from item 1 anyway, and (b) the effective time
// of the newest version already on file (which may itself be a still-future
// pending change). If the window has no playlist at all yet, the new
// version applies immediately.
func NextVersionEffectiveAt(window Window, versions []Version, cycleMS int64, tMS int64) int64 {
	state := Now(window, versions, cycleMS, tMS)

	var candidate int64
	if !state.Ok {
		candidate = tMS
	} else {
		loopBoundary := NextLoopBoundary(state.LoopStartMS, state.LoopLenMS, tMS)
		cycleBoundary := NextCycleBoundary(window.CycleAnchorMS, cycleMS, tMS)
		candidate = loopBoundary
		if cycleBoundary < candidate {
			candidate = cycleBoundary
		}
	}

	hasVersions := false
	var latestKnown int64
	for _, v := range versions {
		if !hasVersions || v.EffectiveAtMS > latestKnown {
			latestKnown = v.EffectiveAtMS
			hasVersions = true
		}
	}
	if hasVersions && latestKnown > candidate {
		candidate = latestKnown
	}
	return candidate
}

// ActiveSync describes a sync-playback event: "show this one media item on
// every window at once". MediaDurationMS is the actual length of that media
// item; if the sync's own DurationMS runs longer than the media, the media
// simply loops (wraps back to its start) for the rest of the sync — that is
// what keeps every window's video at the exact same playback position.
type ActiveSync struct {
	MediaID         string
	StartsAtMS      int64
	DurationMS      int64
	MediaDurationMS int64
}

// SyncState describes what the sync overlay should show, if anything.
type SyncState struct {
	Active   bool
	MediaID  string
	OffsetMS int64
}

// ApplySync decides whether a sync event is currently controlling playback
// at time T, and if so, how far into the (possibly looping) media it is.
//
// Worked example: sync media M2 is 15s long, sync duration is 40s, and sync
// started 32 seconds ago. 32 mod 15 = 2, so every window shows M2 exactly
// 2 seconds into its playback (M2 has already looped twice).
func ApplySync(sync *ActiveSync, tMS int64) SyncState {
	if sync == nil {
		return SyncState{Active: false}
	}
	if tMS < sync.StartsAtMS || tMS >= sync.StartsAtMS+sync.DurationMS {
		return SyncState{Active: false}
	}
	elapsed := tMS - sync.StartsAtMS
	offset := elapsed
	if sync.MediaDurationMS > 0 {
		offset = floorMod(elapsed, sync.MediaDurationMS)
	}
	return SyncState{Active: true, MediaID: sync.MediaID, OffsetMS: offset}
}
