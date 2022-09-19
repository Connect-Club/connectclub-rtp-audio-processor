package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	gst "rtp-audio-processor/gstreamer-src"
)

type PipelineInfo struct {
	Ssrcs    map[ /*ssrc*/ int] /*endpointId*/ string
	Speakers [] /*endpointId*/ string
}

func pipelineHandler(w http.ResponseWriter, r *http.Request) {
	id, err := getRequestParam(r, "id")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost:
		sinkHost, err := getRequestParam(r, "sinkHost")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		sinkPort, err := getRequestParamInt(r, "sinkPort")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		seqNum, err := getRequestParamInt(r, "seqNum")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		log.Printf("CreatePipeline(id=%s, sinkHost=%s, sinkPort=%d, seqNum=%d)\n", id, sinkHost, sinkPort, seqNum)
		srcPort, justCreated, err := gst.CreatePipeline(id, sinkHost, sinkPort, seqNum)
		if err == nil {
			if justCreated {
				w.WriteHeader(http.StatusCreated)
			}
			fmt.Fprintf(w, "%d", srcPort)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	case http.MethodPut:
		log.Printf("UpdatePipeline(id=%s)\n", id)
		var pipelineInfo PipelineInfo
		err := json.NewDecoder(r.Body).Decode(&pipelineInfo)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("UpdatePipeline(id=%s, Ssrcs=%#v, Speakers=%#v)\n", id, pipelineInfo.Ssrcs, pipelineInfo.Speakers)
		err = gst.UpdatePipeline(id, pipelineInfo.Ssrcs, pipelineInfo.Speakers)
		if err == nil {
			fmt.Fprintf(w, "OK")
		} else {
			code := http.StatusInternalServerError
			if _, ok := err.(*gst.NotFoundError); ok {
				code = http.StatusNotFound
			}
			http.Error(w, err.Error(), code)
		}
	case http.MethodDelete:
		log.Printf("DeletePipeline(id=%s)\n", id)
		err := gst.DeletePipeline(id)
		if err == nil {
			fmt.Fprintf(w, "OK")
		} else {
			code := http.StatusInternalServerError
			if _, ok := err.(*gst.NotFoundError); ok {
				code = http.StatusNotFound
			}
			http.Error(w, err.Error(), code)
		}
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
