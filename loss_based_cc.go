package pioncc

import (
	"log"

	"github.com/pion/interceptor/pkg/ccfb"
)

type lossBasedCC struct {
	highestAcked int64
	bitrate      int
	min, max     int
}

func (l *lossBasedCC) onFeedback(feedback map[uint32]*ccfb.PacketReportList, latestDeliveryRate int) {
	numPackets := 0
	arrived := 0
	lost := 0
	for _, prl := range feedback {
		for _, report := range prl.Reports {
			if report.SeqNr > l.highestAcked {
				l.highestAcked = report.SeqNr
				numPackets++
				if report.Arrived {
					arrived++
				} else {
					lost++
				}
			}
		}
	}
	lossRate := float64(lost) / float64(numPackets)
	canIncrease := float64(latestDeliveryRate) >= 0.95*float64(l.bitrate)
	if lossRate > 0.1 {
		l.bitrate = int(float64(l.bitrate) * (1 - 0.5*lossRate))
	} else if lossRate < 0.02 && canIncrease {
		l.bitrate = int(float64(l.bitrate) * 1.05)
	}
	l.bitrate = max(min(l.bitrate, l.max), l.min)
	log.Printf("received acks for %v packets, %v arrived, %v lost, (%v %%)", numPackets, arrived, lost, 100*lossRate)
}
