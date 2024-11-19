package pioncc

import (
	"encoding/base64"
	"encoding/json"

	"github.com/pion/webrtc/v4"
)

func encodeSessionDescription(sd webrtc.SessionDescription) (string, error) {
	b, err := json.Marshal(sd)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func decodeSessionDescription(data string) (webrtc.SessionDescription, error) {
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	var d webrtc.SessionDescription
	if err := json.Unmarshal(b, &d); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return d, nil
}

func encodeCandidate(c webrtc.ICECandidate) (string, error) {
	b, err := json.Marshal(c.ToJSON())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeCandidate(data []byte) (webrtc.ICECandidateInit, error) {
	var ci webrtc.ICECandidateInit
	if err := json.Unmarshal(data, &ci); err != nil {
		return webrtc.ICECandidateInit{}, err
	}
	return ci, nil
}
