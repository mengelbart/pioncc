package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/mengelbart/pioncc"
	"golang.org/x/sync/errgroup"
)

func main() {
	receiverAddr := flag.String("receiver", "localhost:8080", "local http server address")
	senderAddr := flag.String("sender", "localhost:8081", "remote http server address")
	send := flag.Bool("send", false, "send media track")
	gcc := flag.Bool("gcc", false, "configure GCC")
	genericCC := flag.Bool("gencc", false, "configure generic CC")
	ccfb := flag.Bool("ccfb", false, "configure CCFB")
	twcc := flag.Bool("twcc", false, "configure TWCC")
	flag.Parse()

	if *send {
		if err := sender(*receiverAddr, *senderAddr, *gcc, *genericCC); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := receiver(*senderAddr, *receiverAddr, *twcc, *ccfb); err != nil {
		log.Fatal(err)
	}
}

func sender(receiverAddr, senderAddr string, gcc, genericCC bool) error {
	options := []pioncc.PeerOption{}
	if gcc {
		options = append(options, pioncc.GCCOption())
	}
	if genericCC {
		options = append(options, pioncc.GenericCCOption())
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
	if err := sender.AddTrack(); err != nil {
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
