package tomp2p

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type peer struct {
	port            uint16
	listening       map[string]PeerConn
	inbound         chan Packet
	outboundDefault chan Packet
	open            bool
	mu              *sync.Mutex
	done            chan bool
}

type PeerConn struct {
	Address  *net.Addr
	Conn     *net.UDPConn
	outbound chan Packet
}

type Packet struct {
	addr     *net.UDPAddr
	data     []byte
	outbound chan Packet
}

const (
	maxUPDPacketSize    = 1500
	inboundChannelSize  = 1000
	outboundChannelSize = 1000
)

func (p *peer) Listen() {
	p.ListenOnce()
	go func() {
		ticker := time.NewTicker(time.Second * 30)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.ListenOnce()
			case <-p.done:
				fmt.Println("done")
				return
			}
		}
	}()
}

func (p *peer) ListenOnce() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.open {
		return errors.New("cannot listen on a closed peer connection")
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	if p.inbound == nil {
		p.inbound = make(chan Packet, inboundChannelSize)
	}
	if p.listening == nil {
		p.listening = make(map[string]PeerConn)
	}
	for _, iface := range ifaces {

		if iface.Flags&net.FlagUp == 0 {
			continue // interface down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // loopback interface
		}

		addrs, err := iface.Addrs()
		if err != nil {
			log.Printf("error in address %v, ignoring", addrs)
			continue
		}
		for _, addr := range addrs {
			if p.listening[addr.String()] == (PeerConn{}) {

				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
					continue
				}
				inet := &net.UDPAddr{ip, int(p.port), ""}
				udpConn, err := net.ListenUDP("udp", inet)
				if err != nil {
					log.Printf("error in listening /%v/ /%v/, ignoring", addr, err)
					continue
				}

				outbound := make(chan Packet, outboundChannelSize)

				if p.outboundDefault == nil {
					p.outboundDefault = outbound
				}
				p.listening[addr.String()] = PeerConn{&addr, udpConn, outbound}
				log.Printf("Debug: listening on: %v", addr)

				p.setupOutbound(outbound, udpConn)
				p.setupInbound(outbound, udpConn)
			} else {
				//find new default outbound channel
				if p.outboundDefault == nil {
					p.outboundDefault = p.listening[addr.String()].outbound
				}
			}
		}
	}
	log.Printf("we are listening on %d addresses.", len(p.listening))
	return nil
}

func (p *peer) setupOutbound(outbound chan Packet, udpConn *net.UDPConn) {
	go func() {
		for packet := range outbound {
			_, err := udpConn.WriteToUDP(packet.data, packet.addr)
			if err != nil {
				log.Printf("Error out: UDP write error: %v", err)
				break
			}
		}
		p.mu.Lock()
		defer p.mu.Unlock()

		if p.open {
			//either outbound channel is closed or connection error, mark default outbound as not usable
			if p.outboundDefault == outbound {
				p.outboundDefault = nil
			}
			p.Listen()
		}
	}()
}

func (p *peer) setupInbound(outbound chan Packet, udpConn *net.UDPConn) {
	go func() {
		for {
			b := make([]byte, maxUPDPacketSize)
			n, addr, err := udpConn.ReadFromUDP(b)
			if err != nil {
				p.mu.Lock()
				defer p.mu.Unlock()
				if p.open {
					log.Printf("Error in: UDP read error: %v", err)
				}
				delete(p.listening, addr.String())
				return
			}
			//idea: set the default outbound to the one that just received a packet
			p.inbound <- Packet{addr, b[:n], outbound}
		}
	}()
}

func (p *peer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.open = false
	for _, v := range p.listening {
		err := v.Conn.Close()
		if err != nil {
			log.Printf("closing connection failed %v, ignoring", v.Address)
			continue
		}
		close(v.outbound)
	}
	close(p.inbound)
	close(p.done)
	log.Printf("closed peers")
}

func New() peer {
	peer := peer{}
	peer.port = 1135
	peer.open = true
	peer.mu = &sync.Mutex{}
	peer.done = make(chan bool, 1)
	return peer
}

func NewPort(port uint16) peer {
	peer := peer{}
	peer.port = port
	peer.open = true
	peer.mu = &sync.Mutex{}
	peer.done = make(chan bool, 1)
	return peer
}
