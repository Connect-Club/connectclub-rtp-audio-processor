package main

import (
	"cloud.google.com/go/pubsub"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"
)

func main() {
	closeCh := make(chan struct{})

	httpDone, err := startHttp(closeCh)
	if err != nil {
		close(closeCh)
		fmt.Printf("can not start http server %#v\n", err)
		return
	}

	pubsubDone, err := startPubSub(closeCh)
	if err != nil {
		close(closeCh)
		fmt.Printf("can not start pubsub client %#v\n", err)
		return
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	select {
	case <-httpDone:
		close(closeCh)
		<-pubsubDone
	case <-pubsubDone:
		close(closeCh)
		<-httpDone
	case <-sig:
		fmt.Printf("graceful shutdown, signal = %v\n", sig)
		close(closeCh)
		<-pubsubDone
		<-httpDone
	}
}

func startHttp(closeCh <-chan struct{}) (<-chan struct{}, error) {
	ln, err := net.Listen("tcp", ":8888")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/pipeline", pipelineHandler)
	mux.HandleFunc("/speech-to-text", speechToTextHandler)
	srv := &http.Server{Handler: mux}

	done := make(chan struct{})
	go func() {
		defer close(done)
		err := srv.Serve(ln)
		fmt.Printf("http server closed, reason = %v\n", err)
	}()
	go func() {
		<-closeCh
		if err := srv.Shutdown(context.Background()); err != nil {
			fmt.Printf("http server shutdown err = %v\n", err)
		}
	}()
	return done, nil
}

func startPubSub(closeCh <-chan struct{}) (<-chan struct{}, error) {
	projectId := os.Getenv("GCLOUD_PROJECT_ID")
	if projectId == "" {
		panic("environment variable GCLOUD_PROJECT_ID is not set")
	}
	cctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	client, err := pubsub.NewClient(cctx, projectId)
	cancel() //avoid context leak
	if err != nil {
		return nil, err
	}
	subId := os.Getenv("SUBSCRIPTION_ID")
	if subId == "" {
		subId = "rtp-audio-processor"
	}
	sub := client.Subscription(subId)
	sctx, stopReceive := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer client.Close()
		defer close(done)
		fmt.Println("start receiving messages from subscription")
		err := sub.Receive(sctx, datatrackHandler)
		fmt.Printf("pubsub client closed, reason = %v\n", err)
	}()
	go func() {
		<-closeCh
		stopReceive()
	}()
	return done, nil
}
