package gameserver

import (
	"testing"
	"time"
)

func TestFightTimeExcludesWaitingDisconnectAndNewFish(t *testing.T) {
	now := time.Unix(100, 0)
	st := &FishState{CastActive: true, Pending: &RolledFish{FishGUID: "first"}}
	st.sampleFightTime(now, 1)
	st.sampleFightTime(now.Add(time.Minute), 1)
	if st.Pending.FightSeconds != 0 {
		t.Fatal("waiting for a hook counted as fighting")
	}
	st.Hooked = true
	st.sampleFightTime(now.Add(61*time.Second), 1)
	st.sampleFightTime(now.Add(63*time.Second), 1)
	if st.Pending.FightSeconds != 2 {
		t.Fatal("continuous fight time missing")
	}
	st.sampleFightTime(now.Add(80*time.Second), 1)
	st.sampleFightTime(now.Add(81*time.Second), 2)
	st.sampleFightTime(now.Add(82*time.Second), 2)
	if st.Pending.FightSeconds != 3 {
		t.Fatalf("disconnect or session change counted as fighting: %g", st.Pending.FightSeconds)
	}
	st.Pending = &RolledFish{FishGUID: "second"}
	st.sampleFightTime(now.Add(83*time.Second), 2)
	if st.Pending.FightSeconds != 0 {
		t.Fatal("new fish inherited previous interval")
	}
	st.Resolved = true
	st.sampleFightTime(now.Add(84*time.Second), 2)
	st.Resolved = false
	st.sampleFightTime(now.Add(85*time.Second), 2)
	if st.Pending.FightSeconds != 0 {
		t.Fatal("resolved interval counted as fighting")
	}
}
