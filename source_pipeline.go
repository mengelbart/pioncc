package pioncc

import (
	"errors"
	"strings"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/pion/webrtc/v4/pkg/media"
)

type sampleWriter interface {
	WriteSample(media.Sample) error
}

type sourcePipeline struct {
	codec    string
	pipeline *gst.Pipeline
	encoder  *gst.Element
}

func newSourcePipeline(codec, source string, writer sampleWriter) (*sourcePipeline, error) {
	gst.Init(nil)

	pipelineStr := "appsink name=appsink"
	switch codec {
	case "vp8":
		pipelineStr = source + " ! vp8enc name=encoder ! " + pipelineStr
	default:
		return nil, errors.New("unknown codec")
	}

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		return nil, err
	}

	appsink, err := pipeline.GetElementByName("appsink")
	if err != nil {
		return nil, err
	}
	app.SinkFromElement(appsink).SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: func(sink *app.Sink) gst.FlowReturn {
			sample := sink.PullSample()
			if sample == nil {
				return gst.FlowEOS
			}
			buffer := sample.GetBuffer()
			if buffer == nil {
				return gst.FlowError
			}

			samples := buffer.Map(gst.MapRead).Bytes()
			defer buffer.Unmap()

			if err := writer.WriteSample(media.Sample{
				Data:     samples,
				Duration: *buffer.Duration().AsDuration(),
			}); err != nil {
				return gst.FlowError
			}

			return gst.FlowOK
		},
	})

	encoder, err := pipeline.GetElementByName("encoder")
	if err != nil {
		return nil, err
	}
	sp := &sourcePipeline{
		codec:    strings.ToLower(codec),
		pipeline: pipeline,
		encoder:  encoder,
	}
	return sp, nil
}

func (p *sourcePipeline) setTargetBitrate(bps int) {
	switch p.codec {
	case "vp8":
		p.encoder.SetProperty("target-bitrate", bps)
	}
}

func (p *sourcePipeline) play() error {
	return p.pipeline.SetState(gst.StatePlaying)
}

func (p *sourcePipeline) pause() error {
	return p.pipeline.SetState(gst.StatePaused)
}
