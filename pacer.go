package pioncc

import (
	"log"
	"time"

	"github.com/pion/rtp"
)

type packetToSend struct {
	writer rtpWriter
	packet *rtp.Packet
}

type rtpWriter interface {
	WriteRTP(p *rtp.Packet) error
}

type pacer struct {
	running bool
	rate    chan int
	queue   chan packetToSend
	bucket  *leakyBucket
}

func newPacer() *pacer {
	return &pacer{
		running: false,
		rate:    make(chan int, 100),
		queue:   make(chan packetToSend, 1500),
		bucket: &leakyBucket{
			burstSize:  5 * 1200,
			rate:       1_000_000,
			lastSent:   time.Time{},
			lastBudget: 0,
		},
	}
}

func (p *pacer) WriteRTP(pkt *rtp.Packet, writer rtpWriter) error {
	if !p.running {
		go p.run()
		p.running = true
	}
	select {
	case p.queue <- packetToSend{
		writer: writer,
		packet: pkt,
	}:
	default:
		log.Printf("warning: pacer dropped packet due to queue overflow")
	}
	return nil
}

func (p *pacer) setRate(rate int) {
	log.Printf("update pacing rate to %v", rate)
	p.rate <- rate
}

func (p *pacer) run() {
	ticker := time.NewTicker(5 * time.Millisecond)

	for {
		select {
		case rate := <-p.rate:
			p.bucket.setRate(rate)
		case <-ticker.C:
			for pkt := range p.queue {
				now := time.Now()
				budget := p.bucket.budget(now)
				size := pkt.packet.MarshalSize()
				if err := pkt.writer.WriteRTP(pkt.packet); err != nil {
					log.Printf("pacer failed to write RTP packet: %v", err)
				}
				p.bucket.onSent(now, size)
				// Allow overshooting last packet
				if budget < size {
					log.Printf("budget too small: %v, queuesize=%v", budget, len(p.queue))
					break
				}
			}
			ticker.Reset(5 * time.Millisecond)
		}
	}
}
