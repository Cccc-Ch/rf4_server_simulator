package gameserver

import (
	"testing"
	"time"
)

// 同一尾鱼在窗口内重复推送会让客户端重建鱼实体，导致"溜的鱼"和"结算的鱼"对不上；
// claimFishPush 保证同一 FishGUID 在窗口内只放行一次。
func TestClaimFishPushDedupe(t *testing.T) {
	sess := &Session{}
	if !sess.claimFishPush("fish-a") {
		t.Fatal("首次推送应放行")
	}
	if sess.claimFishPush("fish-a") {
		t.Fatal("窗口内同 GUID 应被忽略")
	}

	// 窗口过期后同一尾鱼可以再次推送（重新抛竿后重新判到同一条鱼）。
	sess.fishPushMu.Lock()
	sess.fishPushAt = time.Now().Add(-fishPushDedupeWindow - time.Second)
	sess.fishPushMu.Unlock()
	if !sess.claimFishPush("fish-a") {
		t.Fatal("窗口过期后应放行")
	}
	if !sess.claimFishPush("fish-b") {
		t.Fatal("不同鱼应放行")
	}
	if !sess.claimFishPush("") {
		t.Fatal("空 GUID 不应抑制")
	}
}

func TestFishPushFailedClaimAllowsImmediateRetry(t *testing.T) {
	srv, _ := newRigTestServer(t)
	sess := newProbeSess(srv)
	fish := &RolledFish{FishGUID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Species: "perch", WeightG: 100}
	if sess.Push14_14(fish, "", "") {
		t.Fatal("missing tunnel reported delivery")
	}
	ticket, claimed, delivered := sess.reserveFishPush(fish.FishGUID)
	if !claimed || delivered {
		t.Fatal("failed send retained dedupe claim")
	}
	sess.finishFishPush(fish.FishGUID, ticket, false)
	connection, peer := cleanupTestTunnel(t, "dedupe-retry")
	sess.tunnel = connection
	received := make(chan struct{})
	go func() { _, _, _, _ = peer.ReadRecord(); close(received) }()
	if !sess.Push14_14(fish, "", "") {
		t.Fatal("immediate retry failed")
	}
	<-received
	if !sess.Push14_14(fish, "", "") {
		t.Fatal("delivered duplicate reported failure")
	}
	srv.callMu.Lock()
	defer srv.callMu.Unlock()
	if srv.callSeq[sess.Token] != 1 {
		t.Fatalf("duplicate sent another fish: %d", srv.callSeq[sess.Token])
	}
}

func TestFishPushClaimRollbackDoesNotClearNewerTicket(t *testing.T) {
	for _, nextGUID := range []string{"fish-a", "fish-b"} {
		t.Run(nextGUID, func(t *testing.T) {
			sess := &Session{}
			old, _, _ := sess.reserveFishPush("fish-a")
			ready, done := make(chan struct{}), make(chan struct{})
			go func() { <-ready; sess.finishFishPush("fish-a", old, false); close(done) }()
			sess.fishPushMu.Lock()
			old = time.Now().Add(-fishPushDedupeWindow - time.Second)
			sess.fishPushAt = old
			sess.fishPushMu.Unlock()
			current, claimed, delivered := sess.reserveFishPush(nextGUID)
			if !claimed || delivered {
				t.Fatal("new claim rejected")
			}
			close(ready)
			<-done
			ticket, claimed, delivered := sess.reserveFishPush(nextGUID)
			if claimed || delivered || !ticket.Equal(current) {
				t.Fatal("old failed send cleared or confirmed newer claim")
			}
			sess.finishFishPush(nextGUID, current, true)
			_, claimed, delivered = sess.reserveFishPush(nextGUID)
			if claimed || !delivered {
				t.Fatal("successful claim lost confirmation")
			}
		})
	}
}
