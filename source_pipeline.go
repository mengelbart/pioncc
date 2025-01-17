package pioncc

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/pion/webrtc/v4"
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
	source = source + " ! clocksync "
	switch codec {
	case webrtc.MimeTypeVP8:
		pipelineStr = source + " ! vp8enc name=encoder keyframe-max-dist=10 auto-alt-ref=true cpu-used=5 deadline=1 ! " + pipelineStr
	case webrtc.MimeTypeH264:
		pipelineStr = source + " ! x264enc name=encoder speed-preset=ultrafast tune=zerolatency key-int-max=200 ! " + pipelineStr
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
	var propertyName string
	var scaledBitrate any
	switch p.codec {
	case strings.ToLower(webrtc.MimeTypeVP8):
		propertyName = "target-bitrate"
		scaledBitrate = bps
	case strings.ToLower(webrtc.MimeTypeH264):
		propertyName = "bitrate"
		scaledBitrate = uint(float64(bps) / 1000.0)
	default:
		panic(fmt.Sprintf("invalid codec: %v", p.codec))
	}
	if err := p.encoder.SetProperty(propertyName, scaledBitrate); err != nil {
		log.Printf("failed to update bitrate: %v", err)
		return
	}
	// rate, err := p.encoder.GetProperty(propertyName)
	// if err != nil {
	// 	panic(err)
	// }
	// log.Printf("new bitrate=%v", rate)
}

func (p *sourcePipeline) play() error {
	return p.pipeline.SetState(gst.StatePlaying)
}

func (p *sourcePipeline) pause() error {
	return p.pipeline.SetState(gst.StatePaused)
}
