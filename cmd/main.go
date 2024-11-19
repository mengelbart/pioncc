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
	flag.Parse()

	if *send {
		if err := sender(*receiverAddr, *senderAddr); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := receiver(*senderAddr, *receiverAddr); err != nil {
		log.Fatal(err)
	}
}

func sender(receiverAddr, senderAddr string) error {
	signalingClient := pioncc.NewHTTPSignalingClient(receiverAddr)
	sender, err := pioncc.NewPeer(signalingClient)
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

func receiver(senderAddr, receiverAddr string) error {
	signalingClient := pioncc.NewHTTPSignalingClient(senderAddr)
	receiver, err := pioncc.NewPeer(signalingClient)
	if err != nil {
		return err
	}
	signalingServer := pioncc.NewHTTPSignalingServer(receiverAddr, receiver, receiver)
	return signalingServer.ListenAndServe()
}
