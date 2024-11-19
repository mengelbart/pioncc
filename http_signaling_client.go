package pioncc

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/pion/webrtc/v4"
)

type HTTPSignalingClient struct {
	addr string
}

func NewHTTPSignalingClient(addr string) *HTTPSignalingClient {
	return &HTTPSignalingClient{
		addr: addr,
	}
}

func (c *HTTPSignalingClient) postSessionDescription(sd webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	payload, err := encodeSessionDescription(sd)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	resp, err := c.postJSON("/offer", []byte(payload))
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	answer, err := decodeSessionDescription(string(body))
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	return answer, nil
}

func (c *HTTPSignalingClient) postICECandidate(candidate *webrtc.ICECandidate) error {
	payload, err := encodeCandidate(*candidate)
	if err != nil {
		return err
	}
	_, err = c.postJSON("/candidate", []byte(payload))
	return err
}

func (c *HTTPSignalingClient) postJSON(path string, payload []byte) (*http.Response, error) {
	slog.Info("posting request", "path", path)
	resp, err := http.Post(fmt.Sprintf("http://%v%v", c.addr, path), "application/json; charset=utf-8", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("unexpected response code: %v, failed to read body: %v", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("unexpected response code: %v, body: %v", resp.StatusCode, string(body))
	}
	slog.Info("got status ok")
	return resp, nil
}
