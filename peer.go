package pioncc

import (
	"log"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/bwe"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/ccfb"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/webrtc/v4"
)

type Peer struct {
	signalingClient *HTTPSignalingClient
	peerConnection  *webrtc.PeerConnection

	pendingCandidatesLock sync.Mutex
	pendingCandidates     []*webrtc.ICECandidate

	settingEngine       webrtc.SettingEngine
	interceptorRegistry *interceptor.Registry
	mediaEngine         *webrtc.MediaEngine
	config              webrtc.Configuration

	srcPipelinesLock sync.Mutex
	srcPipelines     []*sourcePipeline

	bwe   *bwe.SendSideController
	pacer *pacer
}

type PeerOption func(*Peer) error

func GCCOption() PeerOption {
	return func(p *Peer) error {
		bwe, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
			return gcc.NewSendSideBWE(gcc.SendSideBWEInitialBitrate(1_000_000))
		})
		if err != nil {
			return err
		}
		bwe.OnNewPeerConnection(func(id string, estimator cc.BandwidthEstimator) {
			go p.periodicBandwidthUpdate(estimator)
		})
		p.interceptorRegistry.Add(bwe)
		webrtc.ConfigureTWCCHeaderExtensionSender(p.mediaEngine, p.interceptorRegistry)
		return nil
	}
}

func GenericCCOption() PeerOption {
	return func(p *Peer) error {
		i, err := ccfb.NewInterceptor()
		if err != nil {
			return err
		}
		p.interceptorRegistry.Add(i)
		return nil
	}
}

func TWCC() PeerOption {
	return func(p *Peer) error {
		if err := webrtc.ConfigureTWCCSender(p.mediaEngine, p.interceptorRegistry); err != nil {
			return err
		}
		return nil
	}
}

func TWCCHdrExt() PeerOption {
	return func(p *Peer) error {
		if err := webrtc.ConfigureTWCCHeaderExtensionSender(p.mediaEngine, p.interceptorRegistry); err != nil {
			return err
		}
		return nil
	}
}

func CCFB() PeerOption {
	return func(p *Peer) error {
		if err := webrtc.ConfigureCongestionControlFeedback(p.mediaEngine, p.interceptorRegistry); err != nil {
			return err
		}
		return nil
	}
}

func NewPeer(client *HTTPSignalingClient, options ...PeerOption) (*Peer, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}
	p := &Peer{
		signalingClient:       client,
		peerConnection:        nil,
		pendingCandidatesLock: sync.Mutex{},
		pendingCandidates:     []*webrtc.ICECandidate{},
		settingEngine:         webrtc.SettingEngine{},
		interceptorRegistry:   &interceptor.Registry{},
		mediaEngine:           &webrtc.MediaEngine{},
		config:                config,
		srcPipelinesLock:      sync.Mutex{},
		srcPipelines:          []*sourcePipeline{},
		bwe:                   bwe.NewSendSideController(800_000, 100_000, 100_000_000),
		pacer:                 newPacer(),
	}
	if err := p.mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	if err := webrtc.ConfigureRTCPReports(p.interceptorRegistry); err != nil {
		return nil, err
	}

	for _, opt := range options {
		if err := opt(p); err != nil {
			return nil, err
		}
	}
	api := webrtc.NewAPI(
		webrtc.WithSettingEngine(p.settingEngine),
		webrtc.WithInterceptorRegistry(p.interceptorRegistry),
		webrtc.WithMediaEngine(p.mediaEngine),
	)

	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}
	p.peerConnection = peerConnection

	p.peerConnection.OnICECandidate(p.onICECandidate)
	p.peerConnection.OnConnectionStateChange(p.onConnectionStateChange)
	p.peerConnection.OnTrack(p.onTrack)

	return p, nil
}

func (p *Peer) AddTrack(mimeType string) error {
	localStatic, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: mimeType, ClockRate: 90_000}, "video", "pion_video")
	// localStatic, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: mimeType}, "video", "pion_video")
	if err != nil {
		return err
	}
	track, err := newTrack(localStatic.Codec(), localStatic, p.pacer)
	// track, err := newTrack(localStatic.Codec(), localStatic, nil)
	if err != nil {
		return err
	}
	// sp, err := newSourcePipeline(mimeType, "videotestsrc ", track)
	// sp, err := newSourcePipeline(mimeType, "filesrc location=/home/mathis/src/github.com/mengelbart/pion-cc-tests/pioncc/input.y4m ! decodebin ", track)
	sp, err := newSourcePipeline(mimeType, "filesrc location=/media/hdd/testvideos/sintel_4k.mov ! decodebin ", track)
	// sp, err := newSourcePipeline(mimeType, "videotestsrc ! video/x-raw,width=1280,height=720,stream-format=byte-stream,format=I420 ", track)
	if err != nil {
		return err
	}
	sp.setTargetBitrate(800_000)

	p.srcPipelinesLock.Lock()
	p.srcPipelines = append(p.srcPipelines, sp)
	p.srcPipelinesLock.Unlock()

	rtpSender, err := p.peerConnection.AddTrack(localStatic)
	if err != nil {
		return err
	}
	go p.readRTCP(rtpSender)
	return nil
}

// TODO: Exit when peer is done
func (p *Peer) readRTCP(rtpSender *webrtc.RTPSender) {
	rtcpBuf := make([]byte, 1500)
	lastRate := 0
	for {
		_, attr, err := rtpSender.Read(rtcpBuf)
		if err != nil {
			log.Printf("error while reading RTCP: %v", err)
		}
		reports, ok := attr.Get(ccfb.CCFBAttributesKey).(map[uint32]*ccfb.PacketReportList)
		if !ok {
			log.Print("failed to type assert packet report list")
			continue
		}

		target := p.bwe.OnFeedbackReport(reports)
		if target != lastRate {
			lastRate = target
			p.updateTargetBitrate(target)
		}
	}
}

// TODO: Exit when peer is done
func (p *Peer) periodicBandwidthUpdate(estimator cc.BandwidthEstimator) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		target := estimator.GetTargetBitrate()
		p.updateTargetBitrate(target)
	}
}

func (p *Peer) updateTargetBitrate(target int) {
	p.srcPipelinesLock.Lock()
	defer p.srcPipelinesLock.Unlock()
	if len(p.srcPipelines) == 0 {
		return
	}
	p.pacer.setRate(int(1.5 * float64(target)))
	share := float64(target) / float64(len(p.srcPipelines))
	for _, sp := range p.srcPipelines {
		sp.setTargetBitrate(int(share))
	}
}

func (p *Peer) Offer() error {
	offer, err := p.peerConnection.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err = p.peerConnection.SetLocalDescription(offer); err != nil {
		return err
	}
	answer, err := p.signalingClient.postSessionDescription(offer)
	if err != nil {
		return err
	}
	p.peerConnection.SetRemoteDescription(answer)
	return nil
}

func (p *Peer) onTrack(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	codec := strings.Split(track.Codec().MimeType, "/")[1]
	pipeline, err := newSinkPipeline(codec, "autovideosink", uint8(track.PayloadType()))
	if err != nil {
		panic(err)
	}
	if err := pipeline.play(); err != nil {
		panic(err)
	}
	buf := make([]byte, 1500)
	for {
		i, _, err := track.Read(buf)
		if err != nil {
			panic(err)
		}
		if err = pipeline.Write(buf[:i]); err != nil {
			panic(err)
		}
	}
}

func (p *Peer) onICECandidate(c *webrtc.ICECandidate) {
	if c == nil {
		return
	}
	p.pendingCandidatesLock.Lock()
	defer p.pendingCandidatesLock.Unlock()
	desc := p.peerConnection.RemoteDescription()
	if desc == nil {
		p.pendingCandidates = append(p.pendingCandidates, c)
	} else {
		if err := p.signalingClient.postICECandidate(c); err != nil {
			panic(err)
		}
	}
}

func (p *Peer) onConnectionStateChange(connectionState webrtc.PeerConnectionState) {
	slog.Info("Connection State has changed", "state", connectionState.String())
	if connectionState == webrtc.PeerConnectionStateConnected {
		for _, p := range p.srcPipelines {
			if err := p.play(); err != nil {
				slog.Warn("failed to set pipeline to playing", "error", err)
			}
		}
	}
}

func (p *Peer) HandleSessionDescription(sd webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if err := p.peerConnection.SetRemoteDescription(sd); err != nil {
		return nil, err
	}
	answer, err := p.peerConnection.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}
	if err := p.peerConnection.SetLocalDescription(answer); err != nil {
		return nil, err
	}

	p.pendingCandidatesLock.Lock()
	defer p.pendingCandidatesLock.Unlock()
	for _, c := range p.pendingCandidates {
		if err := p.signalingClient.postICECandidate(c); err != nil {
			return nil, err
		}
	}
	return p.peerConnection.LocalDescription(), nil
}

func (p *Peer) HandleCandidate(candidate webrtc.ICECandidateInit) error {
	return p.peerConnection.AddICECandidate(candidate)
}
