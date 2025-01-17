package pioncc

import (
	"errors"
	"strings"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type track struct {
	pacer          *pacer
	localStaticRTP *webrtc.TrackLocalStaticRTP
	packetizer     rtp.Packetizer
	clockRate      float64
}

func newTrack(codec webrtc.RTPCodecCapability, localStaticRTP *webrtc.TrackLocalStaticRTP, pacer *pacer) (*track, error) {
	var payloader rtp.Payloader
	switch strings.ToLower(codec.MimeType) {
	case strings.ToLower(webrtc.MimeTypeVP8):
		payloader = &codecs.VP8Payloader{EnablePictureID: true}
	case strings.ToLower(webrtc.MimeTypeH264):
		payloader = &codecs.H264Payloader{}
	default:
		return nil, errors.New("unknown codec")
	}
	packetizer := rtp.NewPacketizer(1200, 0, 0, payloader, rtp.NewFixedSequencer(1), codec.ClockRate)
	return &track{
		pacer:          pacer,
		localStaticRTP: localStaticRTP,
		packetizer:     packetizer,
		clockRate:      float64(codec.ClockRate),
	}, nil
}

// WriteSample implements sampleWriter.
func (t *track) WriteSample(sample media.Sample) error {
	samples := uint32(sample.Duration.Seconds() * t.clockRate)
	packets := t.packetizer.Packetize(sample.Data, samples)
	for _, p := range packets {
		if t.pacer != nil {
			if err := t.pacer.WriteRTP(p, t.localStaticRTP); err != nil {
				return err
			}
		} else {
			if err := t.localStaticRTP.WriteRTP(p); err != nil {
				return err
			}
		}
	}
	return nil
}
