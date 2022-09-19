package main

import (
	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/storage"
	"context"
	"encoding/json"
	"fmt"
	speechpb "google.golang.org/genproto/googleapis/cloud/speech/v1"
	"math/rand"
	"net/http"
	"os"
	gst "rtp-audio-processor/gstreamer-src"
	"strconv"
	"sync"
	"time"
)

type Result struct {
	RequestId          string
	Time               time.Time
	PipelineId         string
	Endpoint           string
	LanguageCode       string
	AudioUri           string
	RecognitionResults []*speechpb.SpeechRecognitionResult
	Error              string
}

var results map[string]*Result
var resultsMutex sync.RWMutex
var audioBucket string

func init() {
	results = make(map[string]*Result)

	var isEnvSet bool
	audioBucket, isEnvSet = os.LookupEnv("AUDIO_BUCKET")
	if !isEnvSet {
		panic("environment variable AUDIO_BUCKET not set")
	}
}

func recognize(ctx context.Context, audioUri, languageCode string) (*speechpb.LongRunningRecognizeResponse, error) {
	speechClient, err := speech.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("can not create speech client: %w", err)
	}
	defer func(speechClient *speech.Client) {
		err := speechClient.Close()
		if err != nil {
			fmt.Printf("Can not close speech client: %v", err.Error())
		}
	}(speechClient)

	req := &speechpb.LongRunningRecognizeRequest{
		Config: &speechpb.RecognitionConfig{
			Encoding:          speechpb.RecognitionConfig_LINEAR16,
			SampleRateHertz:   48000,
			LanguageCode:      languageCode,
			AudioChannelCount: 1,
		},
		Audio: &speechpb.RecognitionAudio{
			AudioSource: &speechpb.RecognitionAudio_Uri{Uri: audioUri},
		},
	}

	op, err := speechClient.LongRunningRecognize(ctx, req)
	if err != nil {
		return nil, err
	}
	return op.Wait(ctx)
}

func saveToCloudStorage(ctx context.Context, bucketName, objectName string, objectContent []byte) (string, error) {
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("can not create storage client: %w", err)
	}
	defer func(storageClient *storage.Client) {
		err := storageClient.Close()
		if err != nil {
			fmt.Printf("Can not close storage client: %v", err.Error())
		}
	}(storageClient)

	object := storageClient.Bucket(bucketName).Object(objectName)
	objectWriter := object.NewWriter(ctx)

	doneChan := make(chan struct{})
	errChan := make(chan error)
	go func() {
		_, err = objectWriter.Write(objectContent)
		if err != nil {
			errChan <- err
			return
		}
		err = objectWriter.Close()
		if err != nil {
			errChan <- err
			return
		}
		close(doneChan)
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-errChan:
		return "", err
	case <-doneChan:
		return fmt.Sprintf("gs://%v/%v", bucketName, objectName), nil
	}
}

func speechToTextHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		requestId, err := getRequestParam(r, "requestId")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		result := getRecognitionResult(requestId)
		marshalResult(result, w)
	case http.MethodPost:
		pipelineId, err := getRequestParam(r, "pipelineId")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		endpoint, err := getRequestParam(r, "endpoint")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		languageCode, err := getRequestParam(r, "languageCode")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		result, err := postRecognitionRequest(r.Context(), pipelineId, endpoint, languageCode)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		marshalResult(result, w)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func getRecognitionResult(requestId string) *Result {
	resultsMutex.RLock()
	defer resultsMutex.RUnlock()

	if result, ok := results[requestId]; ok {
		return result
	}
	return nil
}

func postRecognitionRequest(ctx context.Context, pipelineId, endpointId, languageCode string) (*Result, error) {
	exportCtx, exportCancel := context.WithTimeout(ctx, time.Second*5)
	defer exportCancel()
	pcmBuf, err := gst.ExportPipeline(exportCtx, pipelineId, endpointId)
	if err != nil {
		return nil, fmt.Errorf("export pipeline error: %w", err)
	}

	resultsMutex.Lock()

	var requestId string
	for {
		requestId = strconv.FormatUint(rand.Uint64(), 10)
		if _, ok := results[requestId]; !ok {
			break
		}
	}
	result := &Result{
		RequestId:    requestId,
		Time:         time.Now(),
		PipelineId:   pipelineId,
		Endpoint:     endpointId,
		LanguageCode: languageCode,
	}

	results[requestId] = result
	resultsMutex.Unlock()

	go func() {
		storeCtx, storeCancel := context.WithTimeout(context.Background(), time.Minute*5)
		defer storeCancel()
		audioUri, err := saveToCloudStorage(storeCtx, audioBucket, fmt.Sprintf("r%v-p%v-e%v.pcm", requestId, pipelineId, endpointId), pcmBuf.Bytes())
		if err != nil {
			result.Error = fmt.Sprintf("Save audio to cloud storage error: %v", err.Error())
			return
		}
		result.AudioUri = audioUri

		recognizeCtx, recognizeCancel := context.WithTimeout(context.Background(), time.Minute*30)
		defer recognizeCancel()
		resp, err := recognize(recognizeCtx, audioUri, languageCode)
		if err != nil {
			result.Error = fmt.Sprintf("Recognition error: %v", err)
			return
		}
		result.RecognitionResults = resp.Results
	}()
	return result, nil
}

func marshalResult(result *Result, w http.ResponseWriter) {
	if result == nil {
		http.Error(w, "Result not found", http.StatusNotFound)
		return
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		http.Error(w, fmt.Sprintf("Result marshal error: %v", err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Add("Content-Type", "application/json; charset=utf-8")
	_, err = w.Write(resultBytes)
	if err != nil {
		fmt.Printf("Can not write response: %v", err.Error())
	}
}
