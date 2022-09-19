package main

import (
	"bytes"
	"cloud.google.com/go/pubsub"
	"context"
	"encoding/json"
	"fmt"
	gst "rtp-audio-processor/gstreamer-src"
	"time"
)

type DatatrackStatePayload struct {
	Sid      string   `json:"sid"`
	Speakers []string `json:"speakers"`
}

func datatrackHandler(_ context.Context, msg *pubsub.Message) {
	if msg.Attributes["eventName"] != "state" {
		msg.Ack()
		return
	}

	fmt.Printf("got message from datatrack: data=%v, attributes=%v\n", string(msg.Data), msg.Attributes)
	if time.Now().Sub(msg.PublishTime) < time.Second*10 {
		var state DatatrackStatePayload
		if err := json.NewDecoder(bytes.NewReader(msg.Data)).Decode(&state); err != nil {
			fmt.Printf("can not decode state payload, err = %v\n", err)
			msg.Nack()
			return
		}
		if err := gst.UpdatePipeline(state.Sid, nil, state.Speakers); err != nil {
			fmt.Printf("can not update pipeline, err = %v\n", err)
		}
	} else {
		fmt.Printf("ignore old message")
	}
	msg.Ack()
}
