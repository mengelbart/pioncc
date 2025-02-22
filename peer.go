package pioncc

import (
	"log"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pion/interceptor"
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

	dre         *deliveryRateEstimator
	lossBasedCC *lossBasedCC
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
		dre: &deliveryRateEstimator{
			history: map[uint32][]ccfb.PacketReport{},
		},
		lossBasedCC: &lossBasedCC{
			highestAcked: 0,
			bitrate:      100_000,
			min:          100_000,
			max:          5_000_000,
		},
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

func (p *Peer) AddTrack() error {
	vp8Track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "video/vp8"}, "video", "pion_video")
	if err != nil {
		return err
	}
	sp, err := newSourcePipeline("vp8", "videotestsrc", vp8Track)
	if err != nil {
		return err
	}
	sp.setTargetBitrate(100_000)

	p.srcPipelinesLock.Lock()
	p.srcPipelines = append(p.srcPipelines, sp)
	p.srcPipelinesLock.Unlock()

	rtpSender, err := p.peerConnection.AddTrack(vp8Track)
	if err != nil {
		return err
	}
	go p.readRTCP(rtpSender)

	return sp.play()
}

// TODO: Exit when peer is done
func (p *Peer) readRTCP(rtpSender *webrtc.RTPSender) {
	rtcpBuf := make([]byte, 1500)
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
		delivered := p.dre.onFeedback(reports)
		p.lossBasedCC.onFeedback(reports, delivered)
		p.updateTargetBitrate(p.lossBasedCC.bitrate)
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
	share := float64(target) / float64(len(p.srcPipelines))
	for _, sp := range p.srcPipelines {
		sp.setTargetBitrate(int(share))
		log.Printf("setting target rate for pipeline to %v", share)
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
	codec := strings.Split(track.Codec().RTPCodecCapability.MimeType, "/")[1]
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
