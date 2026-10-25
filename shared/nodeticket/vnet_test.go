package nodeticket

import "testing"

func TestVnetTicket(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	tk := SignVnet(key, "n1", "room-1", 7, 100, 200)
	if room, slot, err := VerifyVnet(key, "n1", tk, 150); err != nil || room != "room-1" || slot != 7 {
		t.Fatalf("%v %v %v", room, slot, err)
	}
	if _, _, err := VerifyVnet(key, "n2", tk, 150); err == nil {
		t.Fatal("别的节点不认")
	}
	if _, _, err := VerifyVnet(key, "n1", tk, 300); err == nil {
		t.Fatal("过期不认")
	}
}
