package pioncc

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/pion/interceptor"
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
}

type PeerOption func(*Peer)

func GCCOption() PeerOption {
	return func(p *Peer) {
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
	}
	if err := p.mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	for _, opt := range options {
		opt(p)
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
	_, err = p.peerConnection.AddTrack(vp8Track)
	if err != nil {
		return err
	}
	slog.Info("track added")
	return sp.play()
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
