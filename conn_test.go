package tomp2p

import "testing"

func TestPeer(t *testing.T) {
	peer := New()
	peer.Listen()
	peer.Close()
}
