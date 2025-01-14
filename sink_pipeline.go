package pioncc

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
)

type sinkPipeline struct {
	pipeline *gst.Pipeline
	appsrc   *app.Source
}

func newSinkPipeline(codec, sink string, pt uint8) (*sinkPipeline, error) {
	gst.Init(nil)

	pipelineStr := "appsrc format=time is-live=true do-timestamp=true name=src ! application/x-rtp"
	switch strings.ToLower(codec) {
	case "vp8":
		pipelineStr += fmt.Sprintf(", payload=%d, encoding-name=VP8 ! rtpvp8depay ! vp8dec ! ", pt) + sink
	default:
		return nil, fmt.Errorf("unknown codec: %v", codec)
	}

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		return nil, err
	}

	appsrc, err := pipeline.GetElementByName("src")
	if err != nil {
		return nil, err
	}

	sp := &sinkPipeline{
		pipeline: pipeline,
		appsrc:   app.SrcFromElement(appsrc),
	}
	return sp, nil
}

func (p *sinkPipeline) play() error {
	return p.pipeline.SetState(gst.StatePlaying)
}

func (p *sinkPipeline) Write(data []byte) error {
	ret := p.appsrc.PushBuffer(gst.NewBufferFromBytes(data))
	switch ret {
	case gst.FlowEOS:
		return errors.New("unexpected flow return from appsrc.PushBuffer: gst.FlowEOS")
	case gst.FlowError:
		return errors.New("unexpected flow return from appsrc.PushBuffer: gst.FlowError")
	case gst.FlowFlushing:
		return errors.New("unexpected flow return from appsrc.PushBuffer: gst.FlowFlushing")
	case gst.FlowNotLinked:
		return errors.New("unexpected flow return from appsrc.PushBuffer: gst.FlowNotLinked")
	case gst.FlowNotNegotiated:
		return errors.New("unexpected flow return from appsrc.PushBuffer: gst.FlowNotNegotiated")
	case gst.FlowNotSupported:
		return errors.New("unexpected flow return from appsrc.PushBuffer: gst.FlowNotSupported")
	case gst.FlowOK:
		return nil
	default:
		panic(fmt.Sprintf("unexpected gst.FlowReturn: %#v", ret))
	}
}
