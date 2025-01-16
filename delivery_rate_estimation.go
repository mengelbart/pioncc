package pioncc

import (
	"log"
	"time"

	"github.com/pion/interceptor/pkg/ccfb"
)

type deliveryRateEstimator struct {
	history map[uint32][]ccfb.PacketReport
}

func (e *deliveryRateEstimator) onFeedback(feedback map[uint32]*ccfb.PacketReportList) int {
	for ssrc, prl := range feedback {
		for _, report := range prl.Reports {
			if _, ok := e.history[ssrc]; !ok {
				e.history[ssrc] = []ccfb.PacketReport{}
			}
			e.history[ssrc] = append(e.history[ssrc], report)
		}
	}

	earliest := time.Time{}
	latest := time.Time{}
	sum := 0
	for ssrc, reports := range e.history {
		for i, report := range reports {
			if time.Since(report.Departure) > time.Second {
				e.history[ssrc] = append(e.history[ssrc][:i], e.history[ssrc][i+1:]...)
			} else if report.Arrived {
				sum += int(report.Size)
				if report.Departure.Before(earliest) || earliest.IsZero() {
					earliest = report.Departure
				}
				if report.Departure.After(latest) {
					latest = report.Departure
				}
			}
		}
	}
	d := latest.Sub(earliest)
	rate := 8 * float64(sum) / d.Seconds()
	log.Printf("sum=%v, delta=%v, rate=%v bit/s", sum, d, int(rate))
	return int(rate)
}
