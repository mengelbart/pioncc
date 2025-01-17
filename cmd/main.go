package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/mengelbart/pioncc"
	"github.com/pion/webrtc/v4"
	"golang.org/x/sync/errgroup"
)

func main() {
	log.SetFlags(log.Lmicroseconds | log.LUTC | log.Ldate)

	receiverAddr := flag.String("receiver", "localhost:8080", "local http server address")
	senderAddr := flag.String("sender", "localhost:8081", "remote http server address")
	send := flag.Bool("send", false, "send media track")
	gcc := flag.Bool("gcc", false, "configure GCC")
	genericCC := flag.Bool("gencc", false, "configure generic CC")
	ccfb := flag.Bool("ccfb", false, "configure CCFB")
	twcc := flag.Bool("twcc", false, "configure TWCC feedback (receiver) or header extension (sender)")
	codec := flag.String("codec", "h264", "codec")
	flag.Parse()

	if *send {
		if err := sender(*receiverAddr, *senderAddr, *gcc, *genericCC, *twcc, *codec); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := receiver(*senderAddr, *receiverAddr, *twcc, *ccfb); err != nil {
		log.Fatal(err)
	}
}

func sender(receiverAddr, senderAddr string, gcc, genericCC, twcc bool, codec string) error {
	options := []pioncc.PeerOption{}
	if gcc {
		options = append(options, pioncc.GCCOption())
	}
	if genericCC {
		options = append(options, pioncc.GenericCCOption())
	}
	if twcc {
		options = append(options, pioncc.TWCCHdrExt())
	}
	signalingClient := pioncc.NewHTTPSignalingClient(receiverAddr)
	sender, err := pioncc.NewPeer(signalingClient, options...)
	if err != nil {
		return err
	}

	signalingServer := pioncc.NewHTTPSignalingServer(senderAddr, sender, sender)
	g := new(errgroup.Group)
	g.Go(func() error {
		if err := signalingServer.ListenAndServe(); err == http.ErrServerClosed {
			slog.Info("server shutting down with http.ErrServerClosed")
			return nil
		}
		return err
	})
	mt, err := codecToMimeType(codec)
	if err != nil {
		return err
	}
	if err := sender.AddTrack(mt); err != nil {
		return err
	}

	if err := sender.Offer(); err != nil {
		slog.Error("offer failed", "err", err)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err = signalingServer.Shutdown(ctx); err != nil {
			return err
		}
	}

	return g.Wait()
}

func codecToMimeType(codec string) (string, error) {
	switch codec {
	case "vp8":
		return webrtc.MimeTypeVP8, nil
	case "h264":
		return webrtc.MimeTypeH264, nil
	}
	return "", errors.New("unknown codec")
}

func receiver(senderAddr, receiverAddr string, twcc, ccfb bool) error {
	options := []pioncc.PeerOption{}
	if twcc {
		options = append(options, pioncc.TWCC())
	}
	if ccfb {
		options = append(options, pioncc.CCFB())
	}

	signalingClient := pioncc.NewHTTPSignalingClient(senderAddr)
	receiver, err := pioncc.NewPeer(signalingClient, options...)
	if err != nil {
		return err
	}
	signalingServer := pioncc.NewHTTPSignalingServer(receiverAddr, receiver, receiver)
	return signalingServer.ListenAndServe()
}
