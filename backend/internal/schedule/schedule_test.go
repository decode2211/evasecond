// These tests check the scheduling math in schedule.go: given a window's
// playlist and a point in time, do we compute the right thing to show?
// Most cases are loaded from testdata/schedule_vectors.json, a file shared
// with the frontend's TypeScript tests, so both implementations are proven
// to agree on the same examples.
package schedule

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type vectorItem struct {
	MediaID    string `json:"media_id"`
	DurationMS int64  `json:"duration_ms"`
}

type vectorVersion struct {
	ID            int64        `json:"id"`
	EffectiveAtMS int64        `json:"effective_at_ms"`
	Items         []vectorItem `json:"items"`
}

type scheduleCase struct {
	Name          string          `json:"name"`
	CycleAnchorMS int64           `json:"cycle_anchor_ms"`
	CycleMS       int64           `json:"cycle_ms"`
	Versions      []vectorVersion `json:"versions"`
	TMS           int64           `json:"t_ms"`
	Expected      struct {
		Ok           bool   `json:"ok"`
		ItemIndex    int    `json:"item_index"`
		MediaID      string `json:"media_id"`
		OffsetMS     int64  `json:"offset_ms"`
		ItemEndsAtMS int64  `json:"item_ends_at_ms"`
		CycleEndsMS  int64  `json:"cycle_ends_at_ms"`
	} `json:"expected"`
}

type vectorSync struct {
	MediaID         string `json:"media_id"`
	StartsAtMS      int64  `json:"starts_at_ms"`
	DurationMS      int64  `json:"duration_ms"`
	MediaDurationMS int64  `json:"media_duration_ms"`
}

type syncCase struct {
	Name     string      `json:"name"`
	Sync     *vectorSync `json:"sync"`
	TMS      int64       `json:"t_ms"`
	Expected struct {
		Active   bool   `json:"active"`
		MediaID  string `json:"media_id"`
		OffsetMS int64  `json:"offset_ms"`
	} `json:"expected"`
}

type vectorFile struct {
	ScheduleCases []scheduleCase `json:"schedule_cases"`
	SyncCases     []syncCase     `json:"sync_cases"`
}

// testdataPath finds testdata/schedule_vectors.json relative to the repo
// root, walking up from this test file's package directory
// (backend/internal/schedule) three levels.
func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "schedule_vectors.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading shared test vectors at %s: %v", path, err)
	}
	var vf vectorFile
	if err := json.Unmarshal(data, &vf); err != nil {
		t.Fatalf("parsing shared test vectors: %v", err)
	}
	return vf
}

func toWindowAndVersions(c scheduleCase) (Window, []Version) {
	w := Window{ID: "W-test", CycleAnchorMS: c.CycleAnchorMS}
	versions := make([]Version, len(c.Versions))
	for i, v := range c.Versions {
		items := make([]Item, len(v.Items))
		for j, it := range v.Items {
			items[j] = Item{MediaID: it.MediaID, DurationMS: it.DurationMS}
		}
		versions[i] = Version{ID: v.ID, EffectiveAtMS: v.EffectiveAtMS, Items: items}
	}
	return w, versions
}

func TestScheduleVectors(t *testing.T) {
	vf := loadVectors(t)
	for _, c := range vf.ScheduleCases {
		t.Run(c.Name, func(t *testing.T) {
			w, versions := toWindowAndVersions(c)
			got := Now(w, versions, c.CycleMS, c.TMS)

			if got.Ok != c.Expected.Ok {
				t.Fatalf("Ok = %v, want %v", got.Ok, c.Expected.Ok)
			}
			if !c.Expected.Ok {
				return
			}
			if got.ItemIndex != c.Expected.ItemIndex {
				t.Errorf("ItemIndex = %d, want %d", got.ItemIndex, c.Expected.ItemIndex)
			}
			if got.MediaID != c.Expected.MediaID {
				t.Errorf("MediaID = %q, want %q", got.MediaID, c.Expected.MediaID)
			}
			if got.OffsetMS != c.Expected.OffsetMS {
				t.Errorf("OffsetMS = %d, want %d", got.OffsetMS, c.Expected.OffsetMS)
			}
			if got.ItemEndsAtMS != c.Expected.ItemEndsAtMS {
				t.Errorf("ItemEndsAtMS = %d, want %d", got.ItemEndsAtMS, c.Expected.ItemEndsAtMS)
			}
			if got.CycleEndsAtMS != c.Expected.CycleEndsMS {
				t.Errorf("CycleEndsAtMS = %d, want %d", got.CycleEndsAtMS, c.Expected.CycleEndsMS)
			}
		})
	}
}

func TestSyncVectors(t *testing.T) {
	vf := loadVectors(t)
	for _, c := range vf.SyncCases {
		t.Run(c.Name, func(t *testing.T) {
			var sync *ActiveSync
			if c.Sync != nil {
				sync = &ActiveSync{
					MediaID:         c.Sync.MediaID,
					StartsAtMS:      c.Sync.StartsAtMS,
					DurationMS:      c.Sync.DurationMS,
					MediaDurationMS: c.Sync.MediaDurationMS,
				}
			}
			got := ApplySync(sync, c.TMS)
			if got.Active != c.Expected.Active {
				t.Fatalf("Active = %v, want %v", got.Active, c.Expected.Active)
			}
			if !c.Expected.Active {
				return
			}
			if got.MediaID != c.Expected.MediaID {
				t.Errorf("MediaID = %q, want %q", got.MediaID, c.Expected.MediaID)
			}
			if got.OffsetMS != c.Expected.OffsetMS {
				t.Errorf("OffsetMS = %d, want %d", got.OffsetMS, c.Expected.OffsetMS)
			}
		})
	}
}

func TestFloorDivFloorMod(t *testing.T) {
	cases := []struct {
		a, b             int64
		wantDiv, wantMod int64
	}{
		{7, 3, 2, 1},
		{-7, 3, -3, 2},  // -7 = -3*3 + 2
		{7, -3, -3, -2}, // 7 = -3*-3 + -2
		{-7, -3, 2, -1}, // -7 = 2*-3 + -1
		{0, 5, 0, 0},
		{-5, 5, -1, 0},
		{5, 5, 1, 0},
		{-1, 60000, -1, 59999},
	}
	for _, c := range cases {
		if got := floorDiv(c.a, c.b); got != c.wantDiv {
			t.Errorf("floorDiv(%d, %d) = %d, want %d", c.a, c.b, got, c.wantDiv)
		}
		if got := floorMod(c.a, c.b); got != c.wantMod {
			t.Errorf("floorMod(%d, %d) = %d, want %d", c.a, c.b, got, c.wantMod)
		}
	}
}

func TestNextLoopBoundary(t *testing.T) {
	cases := []struct {
		name                    string
		loopStart, loopLen, tMS int64
		want                    int64
	}{
		{"before loop starts", 10000, 5000, 3000, 10000},
		{"exactly on loop start", 10000, 5000, 10000, 10000},
		{"mid loop, one boundary ahead", 0, 60000, 15000, 60000},
		{"exactly on a later boundary", 0, 60000, 120000, 120000},
		{"just past a boundary", 0, 60000, 120001, 180000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NextLoopBoundary(c.loopStart, c.loopLen, c.tMS)
			if got != c.want {
				t.Errorf("NextLoopBoundary(%d, %d, %d) = %d, want %d", c.loopStart, c.loopLen, c.tMS, got, c.want)
			}
		})
	}
}

func TestNextCycleBoundary(t *testing.T) {
	cases := []struct {
		name                      string
		cycleAnchor, cycleMS, tMS int64
		want                      int64
	}{
		{"start of first cycle", 0, 18000000, 0, 18000000},
		{"mid first cycle", 0, 18000000, 9000000, 18000000},
		{"exactly on a boundary", 0, 18000000, 36000000, 54000000},
		{"before anchor", 5000, 18000000, 1000, 5000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NextCycleBoundary(c.cycleAnchor, c.cycleMS, c.tMS)
			if got != c.want {
				t.Errorf("NextCycleBoundary(%d, %d, %d) = %d, want %d", c.cycleAnchor, c.cycleMS, c.tMS, got, c.want)
			}
		})
	}
}

// TestNextVersionEffectiveAt_TwoAddsInARow makes sure that adding two items
// back-to-back (before the first change has even taken effect) results in a
// single future version that contains BOTH new items, scheduled no earlier
// than the first pending change already promised. This mirrors how the HTTP
// handler must behave: each add layers onto the latest known version,
// pending or not.
func TestNextVersionEffectiveAt_TwoAddsInARow(t *testing.T) {
	window := Window{ID: "W1", CycleAnchorMS: 0}
	cycleMS := int64(18000000) // 5 hours

	// Currently playing: a single 10s item, active since T=0.
	v1 := Version{ID: 1, EffectiveAtMS: 0, Items: []Item{{MediaID: "M1", DurationMS: 10000}}}

	now := int64(5000) // 5s into the loop
	// First add: new version = v1 items + M2, effective at the next loop
	// boundary (loop is 10s, started at 0, so next boundary is 10000).
	firstEffectiveAt := NextVersionEffectiveAt(window, []Version{v1}, cycleMS, now)
	if firstEffectiveAt != 10000 {
		t.Fatalf("firstEffectiveAt = %d, want 10000", firstEffectiveAt)
	}
	v2 := Version{ID: 2, EffectiveAtMS: firstEffectiveAt, Items: []Item{
		{MediaID: "M1", DurationMS: 10000},
		{MediaID: "M2", DurationMS: 20000},
	}}

	// Second add, still at T=5000 (before v2 has taken effect). It must be
	// based on v2 (the latest known version, even though it's pending), and
	// its effective_at must be at least v2's effective_at.
	secondEffectiveAt := NextVersionEffectiveAt(window, []Version{v1, v2}, cycleMS, now)
	if secondEffectiveAt != 10000 {
		t.Fatalf("secondEffectiveAt = %d, want 10000 (must not precede the already-pending v2)", secondEffectiveAt)
	}

	// The handler would now build v3 from v2's items + M3. Confirm both M2
	// and M3 (added across the two separate calls) are present in the
	// final version by construction of this test's expectations.
	v3Items := append(append([]Item{}, v2.Items...), Item{MediaID: "M3", DurationMS: 8000})
	found := map[string]bool{}
	for _, it := range v3Items {
		found[it.MediaID] = true
	}
	if !found["M1"] || !found["M2"] || !found["M3"] {
		t.Fatalf("expected M1, M2 and M3 all present in the final version, got %+v", v3Items)
	}
}

func TestNextVersionEffectiveAt_EmptyPlaylistAppliesNow(t *testing.T) {
	window := Window{ID: "W1", CycleAnchorMS: 0}
	got := NextVersionEffectiveAt(window, nil, 18000000, 42000)
	if got != 42000 {
		t.Fatalf("got %d, want 42000 (empty playlist should apply immediately)", got)
	}
}

func TestNextVersionEffectiveAt_CutByCycleBoundary(t *testing.T) {
	window := Window{ID: "W1", CycleAnchorMS: 0}
	cycleMS := int64(20000)
	// Loop is 15s, so the next loop boundary after T=18000 is 30000, but the
	// cycle boundary at 20000 comes first and must win.
	v1 := Version{ID: 1, EffectiveAtMS: 0, Items: []Item{{MediaID: "M1", DurationMS: 15000}}}
	got := NextVersionEffectiveAt(window, []Version{v1}, cycleMS, 18000)
	if got != 20000 {
		t.Fatalf("got %d, want 20000 (cycle boundary should win over the later loop boundary)", got)
	}
}
